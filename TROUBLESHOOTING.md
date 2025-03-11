# Troubleshooting

* [Tools](#tools)
* [Installation](#installation)
  * [InternalError when calling webhook: context deadline exceeded](#internalerror-when-calling-webhook-context-deadline-exceeded)
  * [Invalid header field value](#invalid-header-field-value)
  * [Helm chart install failure: certificate signed by unknown authority](#helm-chart-install-failure-certificate-signed-by-unknown-authority)
* [Operations](#operations)
  * [Stuck runner kind or backing pod](#stuck-runner-kind-or-backing-pod)
  * [Delay in jobs being allocated to runners](#delay-in-jobs-being-allocated-to-runners)
  * [Runner coming up before network available](#runner-coming-up-before-network-available)
  * [Outgoing network action hangs indefinitely](#outgoing-network-action-hangs-indefinitely)
  * [Unable to scale to zero with TotalNumberOfQueuedAndInProgressWorkflowRuns](#unable-to-scale-to-zero-with-totalnumberofqueuedandinprogressworkflowruns)
  * [Slow / failure to boot dind sidecar (default runner)](#slow--failure-to-boot-dind-sidecar-default-runner)

## Tools

A list of tools which are helpful for troubleshooting

* [Kubernetes resources hierarchy parsing tool `kubectl-fields`](https://github.com/rewanthtammana/kubectl-fields)
* [Multi pod and container log tailing for Kubernetes `stern`](https://github.com/stern/stern)

## Installation

Troubeshooting runbooks that relate to ARC installation problems

### InternalError when calling webhook: context deadline exceeded

**Problem**

This issue can come up for various reasons like leftovers from previous installations or not being able to access the K8s service's clusterIP associated with the admission webhook server (of ARC).

```text
Internal error occurred: failed calling webhook "mutate.runnerdeployment.actions.summerwind.dev":
Post "https://actions-runner-controller-webhook.actions-runner-system.svc:443/mutate-actions-summerwind-dev-v1alpha1-runnerdeployment?timeout=10s": context deadline exceeded
```

**Solution**

First we will try the common solution of checking webhook leftovers from previous installations:

1. ```bash
   kubectl get validatingwebhookconfiguration -A
   kubectl get mutatingwebhookconfiguration -A
   ```

2. If you see any webhooks related to actions-runner-controller, delete them:

    ```bash
    kubectl delete mutatingwebhookconfiguration actions-runner-controller-mutating-webhook-configuration
    kubectl delete validatingwebhookconfiguration actions-runner-controller-validating-webhook-configuration
    ```

If that didn't work then probably your K8s control-plane is somehow unable to access the K8s service's clusterIP associated with the admission webhook server:

1. You're running apiserver as a binary and you didn't make service cluster IPs available to the host network. 
2. You're running the apiserver in the pod but your pod network (i.e. CNI plugin installation and config) is not good so your pods(like kube-apiserver) in the K8s control-plane nodes can't access ARC's admission webhook server pod(s) in probably data-plane nodes.

Another reason could be due to GKEs firewall settings you may run into the following errors when trying to deploy runners on a private GKE cluster:

To fix this, you may either:

1. Configure the webhook to use another port, such as 443 or 10250, [each of
   which allow traffic by default](https://cloud.google.com/kubernetes-engine/docs/how-to/private-clusters#add_firewall_rules).

   ```sh
   # With helm, you'd set `webhookPort` to the port number of your choice
   # See https://github.com/actions/actions-runner-controller/pull/1410/files for more information
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

### Invalid header field value

**Problem**

```json
2020-11-12T22:17:30.693Z ERROR controller-runtime.controller Reconciler error 
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

### Helm chart install failure: certificate signed by unknown authority

**Problem**

```text
Error: UPGRADE FAILED: failed to create resource: Internal error occurred: failed calling webhook "webhook.cert-manager.io": failed to call webhook: Post "https://cert-manager-webhook.cert-manager.svc:443/mutate?timeout=10s": x509: certificate signed by unknown authority
```

Apparently, it's failing while `helm` is creating one of resources defined in the ARC chart and the cause was that cert-manager's webhook is not working correctly, due to the missing or the invalid CA certficate.

You'd try to tail logs from the `cert-manager-cainjector` and see it's failing with an error like:

```text
$ kubectl -n cert-manager logs cert-manager-cainjector-7cdbb9c945-g6bt4
I0703 03:31:55.159339       1 start.go:91] "starting" version="v1.1.1" revision="3ac7418070e22c87fae4b22603a6b952f797ae96"
I0703 03:31:55.615061       1 leaderelection.go:243] attempting to acquire leader lease  kube-system/cert-manager-cainjector-leader-election...
I0703 03:32:10.738039       1 leaderelection.go:253] successfully acquired lease kube-system/cert-manager-cainjector-leader-election
I0703 03:32:10.739941       1 recorder.go:52] cert-manager/controller-runtime/manager/events "msg"="Normal"  "message"="cert-manager-cainjector-7cdbb9c945-g6bt4_88e4bc70-eded-4343-a6fb-0ddd6434eb55 became leader" "object"={"kind":"ConfigMap","namespace":"kube-system","name":"cert-manager-cainjector-leader-election","uid":"942a021e-364c-461a-978c-f54a95723cdc","apiVersion":"v1","resourceVersion":"1576"} "reason"="LeaderElection"
E0703 03:32:11.192128       1 start.go:119] cert-manager/ca-injector "msg"="manager goroutine exited" "error"=null
I0703 03:32:12.339197       1 request.go:645] Throttling request took 1.047437675s, request: GET:https://10.96.0.1:443/apis/storage.k8s.io/v1beta1?timeout=32s
E0703 03:32:13.143790       1 start.go:151] cert-manager/ca-injector "msg"="Error registering certificate based controllers. Retrying after 5 seconds." "error"="no matches for kind \"MutatingWebhookConfiguration\" in version \"admissionregistration.k8s.io/v1beta1\""
Error: error registering secret controller: no matches for kind "MutatingWebhookConfiguration" in version "admissionregistration.k8s.io/v1beta1"
```

**Solution**

Your cluster is based on a new enough Kubernetes of version 1.22 or greater which does not support the legacy `admissionregistration.k8s.io/v1beta1` API anymore, and your `cert-manager` is not up-to-date hence it's still trying to use the leagcy Kubernetes API.

In many cases, it's not an option to downgrade Kubernetes. So, just upgrade `cert-manager` to a more recent version that does have have the support for the specific Kubernetes version you're using.

See <https://cert-manager.io/docs/installation/supported-releases/> for the list of available cert-manager versions.

## Operations

Troubeshooting runbooks that relate to ARC operational problems

### Stuck runner kind or backing pod

**Problem**

Sometimes either the runner kind (`kubectl get runners`) or it's underlying pod can get stuck in a terminating state for various reasons. You can get the kind unstuck by removing its finaliser using something like this:

**Solution**

Remove the finaliser from the relevent runner kind or pod

```text
# Get all kind runners and remove the finalizer
$ kubectl get runners --no-headers | awk {'print $1'} | xargs kubectl patch runner --type merge -p '{"metadata":{"finalizers":null}}'

# Get all pods that are stuck terminating and remove the finalizer
$ kubectl -n get pods | grep Terminating | awk {'print $1'} | xargs kubectl patch pod -p '{"metadata":{"finalizers":null}}'
```

_Note the code assumes you have already selected the namespace your runners are in and that they 
are in a namespace not shared with anything else_

### Delay in jobs being allocated to runners

**Problem**

ARC isn't involved in jobs actually getting allocated to a runner. ARC is responsible for orchestrating runners and the runner lifecycle. Why some people see large delays in job allocation is not clear however it has been confirmed https://github.com/actions/actions-runner-controller/issues/1387#issuecomment-1122593984 that this is caused from the self-update process somehow.

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

```text
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

**Solution**

> Added originally to help users with older istio instances.
> Newer Istio instances can use Istio's `holdApplicationUntilProxyStarts` attribute ([istio/istio#11130](https://github.com/istio/istio/issues/11130)) to avoid having to delay starting up the runner.
> Please read the discussion in [#592](https://github.com/actions/actions-runner-controller/pull/592) for more information.

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

### Outgoing network action hangs indefinitely

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

If the issue still persists, you can set the `ARC_DOCKER_MTU_PROPAGATION` to propagate the host MTU to networks created
by the GitHub Runner. For instance:

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
      env:
        - name: ARC_DOCKER_MTU_PROPAGATION
          value: "true"
```

You can read the discussion regarding this issue in
[#1406](https://github.com/actions/actions-runner-controller/issues/1046).

### Unable to scale to zero with TotalNumberOfQueuedAndInProgressWorkflowRuns

**Problem**

HRA doesn't scale the RunnerDeployment to zero, even though you did configure HRA correctly, to have a pull-based scaling metric `TotalNumberOfQueuedAndInProgressWorkflowRuns`, and set `minReplicas: 0`.

**Solution**

You very likely have some dangling workflow jobs stuck in `queued` or `in_progress` as seen in [#1057](https://github.com/actions/actions-runner-controller/issues/1057#issuecomment-1133439061).

Manually call [the "list workflow runs" API](https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-repository), and [remove the dangling workflow job(s)](https://docs.github.com/en/rest/actions/workflow-runs#delete-a-workflow-run).

### Slow / failure to boot dind sidecar (default runner)

**Problem**

If you noticed that it takes several minutes for sidecar dind container to be created or it exits with with error just after being created it might indicate that you are experiencing disk performance issue. You might see message `failed to reserve container name` when scaling up multiple runners at once. When you ssh on kubernetes node that problematic pods were scheduled on you can use tools like `atop`, `htop` or `iotop` to check IO usage and cpu time percentage used on iowait. If you see that disk usage is high (80-100%) and iowaits are taking a significant chunk of you cpu time (normally it should not be higher than 10%) it means that performance is being bottlenecked by slow disk.

**Solution**

The solution is to switch to using faster storage, if you are experiencing this issue you are probably using HDD storage. Switching to SSD storage fixed the problem in my case. Most cloud providers have a list of storage options to use just pick something faster that your current disk, for on prem clusters you will need to invest in some SSDs.

### Dockerd no space left on device

**Problem**

If you are running many containers on your runner you might encounter an issue where docker daemon is unable to start new containers and you see error `no space left on device`.  

**Solution**

Add a `dockerVarRunVolumeSizeLimit` key in your runner's spec with a higher size limit (the default is 1M) For instance:

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
      dockerVarRunVolumeSizeLimit: 50M
      env: []
```

### Runner pods terminate immediately after starting

**Problem**

If your runner-controller-listener pods are functioning correctly but the runner pods terminate immediately after starting, you may need to debug the issue using the default Docker image. Use the ghcr.io/actions/actions-runner:latest image and check the runner logs before the pods terminate.

You can use the following command as a boilerplate to retrieve the logs:`kubectl logs $(kubectl get pods --namespace=<your-namespace> | grep '<pod-name-prefix>' | awk '{print $1}') --namespace=<your-namespace>`

If you see anything like this `POST request to https://pipelinesghubeus23.actions.githubusercontent.com/#TOKEN#/_apis/oauth2/token failed. HTTP Status: BadRequest` this may apply.

**Solution**

Ensure that the system time on all individual Kubernetes nodes is synchronized. The GitHub API that creates the JIT tokens checks the HTTP headers' time, and even a 10-minute clock skew can cause the API to fail.

You can synchronize the time on your nodes using `timedatectl set-ntp yes`. This command will enable NTP (Network Time Protocol) on your node, ensuring that the system time is accurate.
