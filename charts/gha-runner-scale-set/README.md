# Prosper Runner Scale set installation steps


```bash
GCP Porject : prosper-nonprod-platform
cluster : gke_prosper-nonprod-platform_us-west1_nonprod-us-west1-util02
namespace : prosper-runners
```


## Helm Release names

* helm ls

❯ helm ls
```bash
NAME                     	NAMESPACE      	REVISION	UPDATED                             	STATUS  	CHART                                	APP VERSION
prosper-linux-np         	prosper-runners	1       	2024-04-18 13:19:02.480947 -0700 PDT	deployed	gha-runner-scale-set-0.9.1           	0.9.1
prosper-runner-controller	prosper-runners	1       	2024-04-18 13:18:00.067012 -0700 PDT	deployed	gha-runner-scale-set-controller-0.9.1	0.9.1
```


# TODO fill in the above with copy and paste style of commands. 


## Restart Sequence 


* helm install prosper-runner-controller . from controller directory
* helm install prosper-linux-np . from scale set directory
* helm uninstall ... from controller directory
* helm uninstall ... from scale set directory

# TODO fill in the above with copy and paste style of commands. 



