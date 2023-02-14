
# Context

Terraform templates to quickly create an EKS cluster with a managed node group. This is not a reference setup! It's a vanilla setup to be used when attempting to replicate issues and/or to test new features.

⚠️ Do not use this setup in production.

## Pre-requisites

- Terraform v1.3+ installed locally.
- an AWS account
- the AWS CLI v2.7.0/v1.24.0 or newer, installed and configured
- AWS IAM Authenticator
- kubectl v1.24.0 or newer

<details>
    <summary>Download & Authenticate</summary>

```bash
brew install awscli aws-iam-authenticator terraform
```

```bash
# Configure & authenticate AWS CLI
# This will vary based on your AWS account and IAM setup
```

</details>

## Setup

```bash
# Export AWS region & profile env variables
export AWS_REGION="eu-west-2"           # Replace with your region
export AWS_PROFILE="actions-compute"    # Replace with your profile
```

```bash
# You're free to use terraform cloud but you need to update main.tf first
terraform init
```

```bash
# Run terraform plan
terraform plan
```

```bash
# Verify the plan output from the previous step
# Run terraform apply
terraform apply
```

```bash
# Retrieve access credentials for the cluster and configure kubectl
aws eks --region "${AWS_REGION}" update-kubeconfig \
    --name "$(terraform output -raw cluster_name)" \
    --profile "${AWS_PROFILE}"

# If you get this error: 'NoneType' object is not iterable
# Remove the ~/.kube/config file and try again
# https://github.com/aws/aws-cli/issues/4843
```

```bash
# Verify your installation
kubectl cluster-info
```

```bash
# Setup ARC by following this guide:
# https://github.com/actions/actions-runner-controller/tree/master/docs/preview/actions-runner-controller-2
```