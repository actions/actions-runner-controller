# Create helm packages
```bash
git clone https://github.com/prosperllc/actions-runner-controller.git
cd charts/gha-runner-scale-set-controller
helm package .
cd charts/gha-runner-scale-set
helm package

push to helm repo
helm push gha-runner-scale-set-0.9.1.tgz oci://us-docker.pkg.dev/prosper-nonprod-registry/helm/action-runner-controller/gha-runner-scale-set
helm push gha-runner-scale-set-controller-0.9.1.tgz oci://us-docker.pkg.dev/prosper-nonprod-registry/helm/action-runner-controller/gha-runner-scale-set-controller
```




# Prosper Runner Scale set installation steps


```bash
GCP Porject : prosper-nonprod-platform
cluster : gke_prosper-nonprod-platform_us-west1_nonprod-us-west1-util02
namespace : prosper-runners

module "gke_runner" {
  source                 = "github.com/prosperllc/iac-modules.git//module_gcp_actions_runners/?ref=main--2024-08-24--21-59"
  #source                 = "../../../../../../iac-modules/module_gcp_actions_runners"
  project_id             = var.project_id
  cluster_name           = var.cluster_name
  host                   = module.gke_cluster.cluster_endpoint
  full_cluster_name      = module.gke_cluster.cluster_name
  cluster_ca_certificate = base64decode(module.gke_cluster.cluster_cert)
  runners_name           = var.runners_name
}
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



