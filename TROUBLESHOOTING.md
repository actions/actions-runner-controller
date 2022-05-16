# Troubleshooting

* [Tools](#tools)
* [Installation](#installation)
  * [Invalid header field value](#invalid-header-field-value)
  * [Deployment fails on GKE due to webhooks](#deployment-fails-on-gke-due-to-webhooks)
* [Operations](#operations)
  * [Stuck runner kind or backing pod](#stuck-runner-kind-or-backing-pod)
  * [Delay in jobs being allocated to runners](#delay-in-jobs-being-allocated-to-runners)
  * [Runner coming up before network available](#runner-coming-up-before-network-available)
  * [Outgoing network action hangs indefinitely](#outgoing-network-action-hangs-indefinitely)


## Tools

A list of tools which are helpful for troubleshooting

* https://github.com/rewanthtammana/kubectl-fields Kubernetes resources hierarchy parsing tool
* https://github.com/stern/stern Multi pod and container log tailing for Kubernetes

## Installation

Troubeshooting runbooks that relate to ARC installation problems

### Invalid header field value

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


### Deployment fails on GKE due to webhooks

**Problem**

Due to GKEs firewall settings you may run into the following errors when trying to deploy runners on a private GKE cluster:

```
Internal error occurred: failed calling webhook "mutate.runner.actions.summerwind.dev": 
Post https://webhook-service.actions-runner-system.svc:443/mutate-actions-summerwind-dev-v1alpha1-runner?timeout=10s: 
context deadline exceeded
```

**Solution**<br />

To fix this, you may either:

1. Configure the webhook to use another port, such as 443 or 10250, [each of
   which allow traffic by default](https://cloud.google.com/kubernetes-engine/docs/how-to/private-clusters#add_firewall_rules).

   ```sh
   # With helm, you'd set `webhookPort` to the port number of your choice
   # See https://github.com/actions-runner-controller/actions-runner-controller/pull/1410/files for more information
   helm upgrade --install --namespace actions-runner-system --create-namespace \
                --wait actions-runner-controller actions-runner-controller/actions-runner-controller \
                --set webhookPort=10250
   ```

2. Set up a firewall rule to allow the master node to connect to the default
   webhook port. The exact way to do this may vary, but the following script
   should point you in the right direction:

   ```sh
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

## Operations

Troubeshooting runbooks that relate to ARC operational problems

### Stuck runner kind or backing pod

**Problem**

Sometimes either the runner kind (`kubectl get runners`) or it's underlying pod can get stuck in a terminating state for various reasons. You can get the kind unstuck by removing its finaliser using something like this:

**Solution**

Remove the finaliser from the relevent runner kind or pod

```
# Get all kind runners and remove the finalizer
$ kubectl get runners --no-headers | awk {'print $1'} | xargs kubectl patch runner --type merge -p '{"metadata":{"finalizers":null}}'

# Get all pods that are stuck terminating and remove the finalizer
$ kubectl -n get pods | grep Terminating | awk {'print $1'} | xargs kubectl patch pod -p '{"metadata":{"finalizers":null}}'
```

_Note the code assumes you have already selected the namespace your runners are in and that they 
are in a namespace not shared with anything else_

### Delay in jobs being allocated to runners

**Problem**

ARC isn't involved in jobs actually getting allocated to a runner. ARC is responsible for orchestrating runners and the runner lifecycle. Why some people see large delays in job allocation is not clear however it has been https://github.com/actions-runner-controller/actions-runner-controller/issues/1387#issuecomment-1122593984 that this is caused from the self-update process somehow.

**Solution**

Disable the self-update process in your runner manifests

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example-runnerdeployment-with-sleep
spec:
  template:
    spec:
      ...
      env:
        - name: DISABLE_RUNNER_UPDATE
          value: "true"
```

### Runner coming up before network available

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
      ...
      env:
        - name: STARTUP_DELAY_IN_SECONDS
          value: "5"
```

## Outgoing network action hangs indefinitely

**Problem**

Some random outgoing network actions hangs indefinitely. This could be because your cluster does not give Docker the standard MTU of 1500, you can check this out by running `ip link` in a pod that encounters the problem and reading the outgoing interface's MTU value. If it is smaller than 1500, then try the following.

**Solution**

Add a `dockerMTU` key in your runner's spec with the value you read on the outgoing interface. For instance:

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: github-runner
  namespace: github-system
spec:
  replicas: 6
  template:
    spec:
      dockerMTU: 1400
      repository: $username/$repo
      env: []
```

There may be more places you need to tweak for MTU.
Please consult issues like #651 for more information.
