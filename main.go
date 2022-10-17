package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"net"
	"os"
	"path/filepath"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

const (
	cpuinfoTablePrefix = "cpuinfo-table-"
)

type CPUStats struct {
	CPUModel       string
	ProcessorCount int
}

type MemStats struct {
	MemTotalGB int
}

func setupSocket(socketPath string) (net.Listener, error) {
	os.RemoveAll(filepath.Dir(socketPath))
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %v", filepath.Dir(socketPath), err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %q: %v", socketPath, err)
	}

	log.Printf("Listening on: unix://%s", socketPath)
	return listener, nil
}

func setupSignals(socketPath string) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupt
		os.RemoveAll(filepath.Dir(socketPath))
		os.Exit(0)
	}()
}

func main() {
	// We put the socket in a sub-directory to have more control on the permissions
	const socketPath = "/var/run/scope/plugins/cpuinfo/cpuinfo.sock"
	hostID, _ := os.Hostname()

	// Handle the exit signal
	setupSignals(socketPath)

	log.Printf("Starting on %s...\n", hostID)

	_, err := getCPUStats()
	if err != nil {
		log.Fatal(err)
	}

	listener, err := setupSocket(socketPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		listener.Close()
		os.RemoveAll(filepath.Dir(socketPath))
	}()

	plugin := &Plugin{HostID: hostID}
	http.HandleFunc("/report", plugin.Report)
	if err := http.Serve(listener, nil); err != nil {
		log.Printf("error: %v", err)
	}
}

// Plugin groups the methods a plugin needs
type Plugin struct {
	HostID string

	lock        sync.Mutex
	cpuinfoMode bool
}

type request struct {
	NodeID  string
	Control string
}

type response struct {
	ShortcutReport *report `json:"shortcutReport,omitempty"`
}

type report struct {
	Host    topology
	Plugins []pluginSpec
}

type topology struct {
	Nodes             map[string]node             `json:"nodes"`
	MetadataTemplates map[string]metadataTemplate `json:"metadata_templates,omitempty"`
	TableTemplates    map[string]tableTemplate    `json:"table_templates,omitempty"`
}

type tableTemplate struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Prefix string `json:"prefix"`
}

type metadataTemplate struct {
	ID       string  `json:"id"`
	Label    string  `json:"label,omitempty"`    // Human-readable descriptor for this row
	Truncate int     `json:"truncate,omitempty"` // If > 0, truncate the value to this length.
	Datatype string  `json:"dataType,omitempty"`
	Priority float64 `json:"priority,omitempty"`
	From     string  `json:"from,omitempty"` // Defines how to get the value from a report node
}

type node struct {
	Latest map[string]stringEntry `json:"latest,omitempty"`
}

type stringEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Value     string    `json:"value"`
}

type pluginSpec struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Interfaces  []string `json:"interfaces"`
	APIVersion  string   `json:"api_version,omitempty"`
}

func (p *Plugin) makeReport() (*report, error) {
	metrics, err := p.metrics()
	if err != nil {
		return nil, err
	}
	rpt := &report{
		Host: topology{
			Nodes: map[string]node{
				p.getTopologyHost(): metrics,
			},
			TableTemplates:    getTableTemplate(),
			MetadataTemplates: getMetadataTemplate(),
		},
		Plugins: []pluginSpec{
			{
				ID:          "cpuinfo",
				Label:       "cpuinfo",
				Description: "Adds a graph of CPU and memory info to hosts",
				Interfaces:  []string{"reporter", "controller"},
				APIVersion:  "1",
			},
		},
	}
	return rpt, nil
}

func (p *Plugin) metrics() (node, error) {
	cpuInfo, err := getCPUStats()
	if err != nil {
		return node{}, err
	}

	memInfo, err := getMemStats()
	if err != nil {
		return node{}, err
	}

	n := node{}
	tnot := time.Now()
	n.Latest = map[string]stringEntry{
		"cpu_model": {
			Timestamp: tnot,
			Value:     cpuInfo.CPUModel,
		},
		"processor_count": {
			Timestamp: tnot,
			Value:     fmt.Sprintf("%d", cpuInfo.ProcessorCount),
		},
		"platform_memory": {
			Timestamp: tnot,
			Value:     fmt.Sprintf("%d", memInfo.MemTotalGB),
		},
	}

	return n, nil
}

func getMetadataTemplate() map[string]metadataTemplate {
	return map[string]metadataTemplate{
		"cpu_model": {
			ID:       "cpu_model",
			Label:    "CPU Model",
			Truncate: 0,
			Datatype: "",
			Priority: 13.5,
			From:     "latest",
		},
		"processor_count": {
			ID:       "processor_count",
			Label:    "Processor Count",
			Truncate: 0,
			Datatype: "integer",
			Priority: 13.5,
			From:     "latest",
		},
		"platform_memory": {
			ID:       "platform_memory",
			Label:    "Platform Memory",
			Truncate: 0,
			Datatype: "filesize",
			Priority: 13.5,
			From:     "latest",
		},
	}
}

func getTableTemplate() map[string]tableTemplate {
	return map[string]tableTemplate{
		"cpuinfo-table": {
			ID:     "cpuinfo-table",
			Label:  "Host CPU and RAM Info",
			Prefix: cpuinfoTablePrefix,
		},
	}
}

// Report is called by scope when a new report is needed. It is part of the
// "reporter" interface, which all plugins must implement.
func (p *Plugin) Report(w http.ResponseWriter, r *http.Request) {
	p.lock.Lock()
	defer p.lock.Unlock()

	rpt, err := p.makeReport()
	if err != nil {
		log.Printf("error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	raw, err := json.Marshal(*rpt)
	if err != nil {
		log.Printf("error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

func (p *Plugin) getTopologyHost() string {
	return fmt.Sprintf("%s;<host>", p.HostID)
}

func getMemStats() (MemStats, error) {
	memory, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("err=%s", err.Error())
		return MemStats{}, err
	}

	var gb int
	gb = int(memory.Total / 1024 / 1024 / 1024)
	if !isPowerOfTwo(uint64(gb)) {
		gb = int(gb + 1)
	}

	memStats := MemStats{MemTotalGB: gb}
	return memStats, nil
}

func getCPUStats() (CPUStats, error) {
	cpus, err := cpu.Info()
	if err != nil {
		log.Printf("err=%s", err.Error())
		return CPUStats{}, err
	}
	stats := CPUStats{CPUModel: cpus[0].ModelName, ProcessorCount: len(cpus)}
	return stats, nil
}

func isPowerOfTwo(x uint64) bool {
	return (x & (x - 1)) == 0
}
