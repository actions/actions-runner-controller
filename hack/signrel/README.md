# signrel

`signrel` is the utility command for downloading `actions-runner-controller` release assets, sigining those, and uploading the signature files.

## Verifying Release Assets

For users, browse https://keys.openpgp.org/search?q=D8078411E3D8400B574EDB0441B69B728F095A87 and download the public key, or refer to [the instruction](https://keys.openpgp.org/about/usage#gnupg-retrieve) to import the key onto your machine.

Next, you'll want to verify the signature of the download asset somehow.

With `gpg`, you would usually do that by downloading both the asset and the signature files from our specific release page, and run `gpg --verify` like:

```console
# Download the asset
curl -LO https://github.com/actions/actions-runner-controller/releases/download/v0.23.0/actions-runner-controller.yaml

# Download the signature file
curl -LO https://github.com/actions/actions-runner-controller/releases/download/v0.23.0/actions-runner-controller.yaml.asc

# Verify
gpg --verify actions-runner-controller.yaml{.asc,}
```

On succesful verification, the gpg command would output:

```
gpg: Signature made Tue 10 May 2022 04:15:32 AM UTC
gpg:                using RSA key D8078411E3D8400B574EDB0441B69B728F095A87
gpg: checking the trustdb
gpg: marginals needed: 3  completes needed: 1  trust model: pgp
gpg: depth: 0  valid:   1  signed:   0  trust: 0-, 0q, 0n, 0m, 0f, 1u
gpg: next trustdb check due at 2024-05-09
gpg: Good signature from "Yusuke Kuoka <ykuoka@gmail.com>" [ultimate]
```

## Signing Release Assets

Assuming you are a maintainer of the project who has admin permission, run the command like the below to sign assets and upload the signature files:

```console
$ cd hack/signrel

$ for v in v0.23.0 actions-runner-controller-0.18.0 v0.22.3 v0.22.2 actions-runner-controller-0.17.2; do TAG=$v go run . sign; done

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

To retrieve all the available release tags, run:

```
$ go run . tags | jq -r .[].tag_name
```
