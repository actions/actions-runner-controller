## Upgrading

This project makes extensive use of CRDs to provide much of its functionality. Helm unfortunately does not support [managing](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/) CRDs by design:

```
There is no support at this time for upgrading or deleting CRDs using Helm. This was an explicit decision after much community discussion due to the danger for unintentional data loss. Furthermore, there is currently no community consensus around how to handle CRDs and their lifecycle. As this evolves, Helm will add support for those use cases.
```

Helm will do an initial install of CRDs but it will not touch them afterwards (update or delete).

Additionally, because the project leverages CRDs so extensively you **MUST** run the matching controller app container with its matching CRDs i.e. always redeploy your CRDs if you are changing the app version.

Due to the above you can't just do a `helm upgrade` to release the latest version of the chart, the best practice steps are recorded below:

## Steps

1. Uninstall your runners, if you are doing this manually ensure you delete your `RunnerDeployment`, `Runner` and `HorizontalRunnerAutoscaler` setup first:

```shell
# Delete your runners
kubectl delete runner %RUNNER_NAME%
kubectl delete runnerdeployment %RUNNER_DEPLOYMENT_NAME%
kubectl delete horizontalrunnerautoscaler %HRA_NAME%
```

If your `Runner` kinds get stuck you may need to remove the finalizers (this can happen if you delete the pods directly instead of the projects CRD kinds):

```shell
# Check if any runners are stuck after the uninstall
kubectl get runner
# Remove the finalizers from the spec and merge the change
kubectl patch runner %RUNNER_NAME% -p '{"metadata":{"finalizers":null}}' --type=merge
```

2. Uninstall the chart
3. Manually delete the CRDs:

```shell
# Delete the CRDs
kubectl get crds | grep actions.summerwind. | awk '{print $1}' | xargs kubectl delete crd
# Confirm the CRDs are gone
kubectl get crds | grep actions.summerwind.
```

4. Install the chart following the documentation
