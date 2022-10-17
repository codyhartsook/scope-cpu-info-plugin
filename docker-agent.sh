printf "[1] Installing weavescope probe...\n"

sudo curl -L git.io/scope -o /usr/local/bin/scope
sudo chmod a+x /usr/local/bin/scope

source values.txt

scope launch

printf "\n[2] Installing green-optimizer metadata plugin...\n"

sudo docker run \
	  --privileged --net=host --pid=host \
	  -v /var/run/scope/plugins:/var/run/scope/plugins \
	  --name weavescope-cpuinfo-plugin cil/scope-cpuinfo:latest

printf "\n[3] Agent successfully installed.\n"