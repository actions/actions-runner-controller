# Prosper Runner Scale set installation steps


GCP Porject : prosper-nonprod-platform
cluster : gke_prosper-nonprod-platform_us-west1_nonprod-us-west1-util02
namespace : prosper-runners


## Restart Sequence 

* helm uninstall ... from controller directory
* helm uninstall ... from scale set directory
* helm install ... from controller directory
* helm install ... from scale set directory

# TODO fill in the above with copy and paste style of commands. 



