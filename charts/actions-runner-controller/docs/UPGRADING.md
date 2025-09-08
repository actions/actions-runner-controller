## Upgrading

This project makes extensive use of CRDs to provide much of its functionality. Helm unfortunately does not support [managing](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/) CRDs by design:

_The full breakdown as to how they came to this decision and why they have taken the approach they have for dealing with CRDs can be found in [Helm Improvement Proposal 11](https://github.com/helm/community/blob/main/hips/hip-0011.md)_

```
There is no support at this time for upgrading or deleting CRDs using Helm. This was an explicit decision after much 
community discussion due to the danger for unintentional data loss. Furthermore, there is currently no community 
consensus around how to handle CRDs and their lifecycle. As this evolves, Helm will add support for those use cases.
```

Helm will do an initial install of CRDs but it will not touch them afterwards (update or delete).

Additionally, because the project leverages CRDs so extensively you **MUST** run the matching controller app container with its matching CRDs i.e. always redeploy your CRDs if you are changing the app version.

Due to the above you can't just do a `helm upgrade` to release the latest version of the chart, the best practice steps are recorded below:

## Steps

1. Upgrade CRDs, this isn't optional, the CRDs you are using must be those that correspond with the version of the controller you are installing

```shell
# REMEMBER TO UPDATE THE CHART_VERSION TO RELEVANT CHART VERISON!!!!
CHART_VERSION=0.18.0

curl -L https://github.com/actions/actions-runner-controller/releases/download/actions-runner-controller-${CHART_VERSION}/actions-runner-controller-${CHART_VERSION}.tgz | tar zxv --strip 1 actions-runner-controller/crds

kubectl replace -f crds/
```

Note that in case you're going to create prometheus-operator `ServiceMonitor` resources via the chart, you'd need to deploy prometheus-operator-related CRDs as well.

2. Upgrade the Helm release

```shell
# helm repo [command]
helm repo update

# helm upgrade [RELEASE] [CHART] [flags]
helm upgrade actions-runner-controller \
  actions-runner-controller/actions-runner-controller \
  --install \
  --namespace actions-runner-system \
  --version ${CHART_VERSION}
```
