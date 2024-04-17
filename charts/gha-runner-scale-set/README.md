Runner Scale set installation steps


GCP Porject : prosper-nonprod-platform
cluster : gke_prosper-nonprod-platform_us-west1_nonprod-us-west1-util02
namespace : prosper-runners


helm ls 

❯ helm ls
NAME                     	NAMESPACE      	REVISION	UPDATED                             	STATUS  	CHART                                	APP VERSION
prosper-linux-np         	prosper-runners	5       	2024-04-17 14:10:37.697746 -0700 PDT	deployed	gha-runner-scale-set-0.9.0           	0.9.0
prosper-runner-controller	prosper-runners	1       	2024-04-15 13:09:45.65406 -0700 PDT 	deployed	gha-runner-scale-set-controller-0.9.0	0.9.0



Clone the actions runner controller repo


Goto --> Charts --- gha-runner-scale-set-controller

                helm upgrade prosper-runner-controller .

Goto --> Charts --- gha-runner-scale-set-controller

                helm upgrade prosper-linux-np .


