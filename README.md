# GO-client-probe
Client environment integration with Greeb Optimizer SaaS to enable client  
power and carbon emissions monitoring.  

The GO probe consists of the the weave-scope probe and custom weave-scope  
plugin for host cpu and memory metadata. The probe exports host resource  
resource usage and platform metadata. 

## building the custom plugin

## installing the custom plugin

## installation scope for vm
`./docker/launch-scope.sh`

## insallation scope for k8s
The `SERVER-ADDR` arg must be updated in `kubernetes/deploy.yaml` to reflect  
the GO SaaS ip.

`./kubernetes/launch-scope.sh`
