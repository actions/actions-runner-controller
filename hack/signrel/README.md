# signrel

`signrel` is the utility command for downloading `actions-runner-controller` release assets, sigining those, and uploading the signature files.

## Usage

```console
$ cd hack/signrel

$ for v in v0.23.0 actions-runner-controller-0.18.0 v0.22.3 v0.22.2 actions-runner-controller-0.17.2; do TAG=$v go run .; done

Downloading actions-runner-controller.yaml to downloads/v0.23.0/actions-runner-controller.yaml
Uploading downloads/v0.23.0/actions-runner-controller.yaml.asc
downloads/v0.23.0/actions-runner-controller.yaml.asc has been already uploaded
Downloading actions-runner-controller-0.18.0.tgz to downloads/actions-runner-controller-0.18.0/actions-runner-controller-0.18.0.tgz
Uploading downloads/actions-runner-controller-0.18.0/actions-runner-controller-0.18.0.tgz.asc
Upload completed: *snip*
Downloading actions-runner-controller.yaml to downloads/v0.22.3/actions-runner-controller.yaml
Uploading downloads/v0.22.3/actions-runner-controller.yaml.asc
Upload completed: *snip*
Downloading actions-runner-controller.yaml to downloads/v0.22.2/actions-runner-controller.yaml
Uploading downloads/v0.22.2/actions-runner-controller.yaml.asc
Upload completed: *snip*
Downloading actions-runner-controller-0.17.2.tgz to downloads/actions-runner-controller-0.17.2/actions-runner-controller-0.17.2.tgz
Uploading downloads/actions-runner-controller-0.17.2/actions-runner-controller-0.17.2.tgz.asc
Upload completed: *snip*
actions-runner-controller-0.17.2.tgz.asc"}
```
