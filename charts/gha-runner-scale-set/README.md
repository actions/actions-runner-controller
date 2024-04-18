# Prosper Runner Scale set installation steps


GCP Porject : prosper-nonprod-platform
cluster : gke_prosper-nonprod-platform_us-west1_nonprod-us-west1-util02
namespace : prosper-runners


## Pods 
The scale set consists of two managing pods make sure to run the helm command from the Charts --- gha-runner-scale-set-controller directory

```bash
❯ helm ls

NAME                     	NAMESPACE      	REVISION	UPDATED                             	STATUS  	CHART                                	APP VERSION
prosper-linux-np         	prosper-runners	5       	2024-04-17 14:10:37.697746 -0700 PDT	deployed	gha-runner-scale-set-0.9.0           	0.9.0
prosper-runner-controller	prosper-runners	1       	2024-04-15 13:09:45.65406 -0700 PDT 	deployed	gha-runner-scale-set-controller-0.9.0	0.9.0
```


Clone the actions runner controller repo


Goto --> Charts --- gha-runner-scale-set-controller

                helm upgrade prosper-runner-controller .

Goto --> Charts --- gha-runner-scale-set-controller

                helm upgrade prosper-linux-np .

# TODO 

1. I am a little confused about the above as you list two pods both prosper-runner-controller and prosper-linux-np  as these two different scale sets? I thought the scale set consisted of a listener and a controller.   Maybe this is still a work in progress.   the next phase of this will be pointing terraform at this helm chart over in ica-devops-gcp?  

