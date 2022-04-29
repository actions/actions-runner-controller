---
name: Bug report
about: Create a report to help us improve
title: ''
assignees: ''

---

**Describe the bug**
A clear and concise description of what the bug is.

**Environment (please complete the following information):**
 - Controller Version [e.g. 0.18.2, or git commit ID] (Refer to semver-like release tags for controller versions. Any release tags prefixed with `actions-runner-controller-` are for chart releases)
 - Deployment Method [e.g. Helm and Kustomize ]
 - Helm Chart Version [e.g. 0.11.0, if applicable]

**Checks**

- [ ] This isn't a question or user support case (For Q&A and community support, go to [Discussions](https://github.com/actions-runner-controller/actions-runner-controller/discussions). It might also be a good idea to contract with any of contributors and maintainers if your business is so critical and therefore you need priority support
- [ ] I've read [releasenotes](https://github.com/actions-runner-controller/actions-runner-controller/tree/master/docs/releasenotes) before submitting this issue and I'm sure it's not due to any recently-introduced backward-incompatible changes
- [ ] My actions-runner-controller version (v0.x.y) does support the feature
- [ ] I've already upgraded ARC to the latest and it didn't fix the issue

**To Reproduce**
Steps to reproduce the behavior:
1. Go to '...'
2. Click on '....'
3. Scroll down to '....'
4. See error

**Expected behavior**
A clear and concise description of what you expected to happen.

**Resource definitions**
Add a copy of your resourec definition (RunnerDeployment or RunnerSet, and HorizontalRunnerAutoscaler. If RunnerSet, also include the StorageClass being used)

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: example
spec:
  #snip
```

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerSet
metadata:
  name: example
 spec:
   #snip
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: example
provisioner: ...
reclaimPolicy: ...
volumeBindingMode: ...
```

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: HorizontalRunnerAutoscaler
metadata:
  name:
spec:
  #snip
```

**Logs**:
Include logs from `actions-runner-controller`'s controller-manager pod and runner pods.

To grab controller logs, run:

```console
# Set NS according to your setup
$ NS=actions-runner-system

$ kubectl -n $NS get po
# Grab the pod name and set it to $POD_NAME

$ kubectl -n $NS $POD_NAME > arc.log
```

upload it to e.g. https://gist.github.com/ and paste the link to it here.

To grab the runner pod logs, run:

```console
# Set NS according to your setup. It should match your RunnerDeployment's metadata.namespace.
$ NS=default

$ kubectl -n $NS get po
# Grab the name of the problematic runner pod and set it to $POD_NAME

$ kubectl -n $NS $POD_NAME > runnerpod.log
```

upload it to e.g. https://gist.github.com/ and paste the link to it here.

**Screenshots**
If applicable, add screenshots to help explain your problem.

**Additional context**
Add any other context about the problem here.
