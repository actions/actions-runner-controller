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

1. Uninstall the chart
2. Manually delete the CRDs:

```shell
# Delete the CRDs
kubectl get crds | grep actions.summerwind. | awk '{print $1}' | xargs kubectl delete crd
# Confirm the CRDs are gone
kubectl get crds | grep actions.summerwind.
```

3. Install the chart following the documentation
