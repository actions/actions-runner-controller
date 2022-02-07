# Troubleshooting

* [Invalid header field value](#invalid-header-field-value)
* [Runner coming up before network available](#runner-coming-up-before-network-available)
* [Deployment fails on GKE due to webhooks](#deployment-fails-on-gke-due-to-webhooks)

## Invalid header field value

**Problem**

```json
2020-11-12T22:17:30.693Z	ERROR	controller-runtime.controller	Reconciler error	
{
  "controller": "runner",
  "request": "actions-runner-system/runner-deployment-dk7q8-dk5c9",
  "error": "failed to create registration token: Post \"https://api.github.com/orgs/$YOUR_ORG_HERE/actions/runners/registration-token\": net/http: invalid header field value \"Bearer $YOUR_TOKEN_HERE\\n\" for key Authorization"
}
```

**Solution**

Your base64'ed PAT token has a new line at the end, it needs to be created without a `\n` added, either:
* `echo -n $TOKEN | base64`
* Create the secret as described in the docs using the shell and documented flags

## Runner coming up before network available

**Problem**

If you're running your action runners on a service mesh like Istio, you might
have problems with runner configuration accompanied by logs like:

```
....
runner Starting Runner listener with startup type: service
runner Started listener process
runner An error occurred: Not configured
runner Runner listener exited with error code 2
runner Runner listener exit with retryable error, re-launch runner in 5 seconds.
....
```

This is because the `istio-proxy` has not completed configuring itself when the
configuration script tries to communicate with the network.

More broadly, there are many other circumstances where the runner pod coming up first can cause issues.

**Solution**<br />

> Added originally to help users with older istio instances.
> Newer Istio instances can use Istio's `holdApplicationUntilProxyStarts` attribute ([istio/istio#11130](https://github.com/istio/istio/issues/11130)) to avoid having to delay starting up the runner.
> Please read the discussion in [#592](https://github.com/actions-runner-controller/actions-runner-controller/pull/592) for more information.

You can add a delay to the runner's entrypoint script by setting the `STARTUP_DELAY_IN_SECONDS` environment variable for the runner pod. This will cause the script to sleep X seconds, this works with any runner kind.

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeployment-with-sleep
spec:
  template:
    spec:
      env:
        # This runner's entrypoint script will have a 5 seconds delay 
        # as a first action within the entrypoint script
        - name: STARTUP_DELAY_IN_SECONDS
          value: "5"
```

## Deployment fails on GKE due to webhooks

**Problem**

Due to GKEs firewall settings you may run into the following errors when trying to deploy runners on a private GKE cluster:

```
Internal error occurred: failed calling webhook "mutate.runner.actions.summerwind.dev": 
Post https://webhook-service.actions-runner-system.svc:443/mutate-actions-summerwind-dev-v1alpha1-runner?timeout=10s: 
context deadline exceeded
```

**Solution**<br />

To fix this, you need to set up a firewall rule to allow the master node to connect to the webhook port.
The exact way to do this may wary, but the following script should point you in the right direction:

```
# 1) Retrieve the network tag automatically given to the worker nodes
# NOTE: this only works if you have only one cluster in your GCP project. You will have to manually inspect the result of this command to find the tag for the cluster you want to target
WORKER_NODES_TAG=$(gcloud compute instances list --format='text(tags.items[0])' --filter='metadata.kubelet-config:*' | grep tags | awk '{print $2}' | sort | uniq)

# 2) Take note of the VPC network in which you deployed your cluster
# NOTE this only works if you have only one network in which you deploy your clusters
NETWORK=$(gcloud compute instances list --format='text(networkInterfaces[0].network)' --filter='metadata.kubelet-config:*' | grep networks | awk -F'/' '{print $NF}' | sort | uniq)

# 3) Get the master source ip block
SOURCE=$(gcloud container clusters describe <cluster-name> --region <region> | grep masterIpv4CidrBlock| cut -d ':' -f 2 | tr -d ' ')
gcloud compute firewall-rules create k8s-cert-manager --source-ranges $SOURCE --target-tags $WORKER_NODES_TAG  --allow TCP:9443 --network $NETWORK
```
