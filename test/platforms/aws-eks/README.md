# Setup

## Pre-requisites

- Terraform v1.3+ installed locally.
- an AWS account
- the AWS CLI v2.7.0/v1.24.0 or newer, installed and configured
- AWS IAM Authenticator
- kubectl v1.24.0 or newer

<details>
    <summary>Codespaces</summary>

```bash
brew install awscli aws-iam-authenticator terraform
```

```bash
# Configure AWS CLI
# Assumes key and secret are stored in pass
aws configure set region eu-west-2 --profile gh # or your preferred region and profile
aws configure set aws_access_key_id "***" --profile gh # replace with your access key value
aws configure set aws_secret_access_key "***" --profile gh # replace with your secret key value
```

</details>

