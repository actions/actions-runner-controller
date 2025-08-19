# Local Development Environment Setup - Runner Scale Set Controller

This guide sets up a local development environment for the **NEW** GitHub Actions Runner Scale Set Controller (not the legacy mode).

## Important Notes

- **NO cert-manager required** - The new mode doesn't use webhooks
- **NO legacy controller** - We only work with the new `actions.github.com` API group
- Uses separate Helm charts: `gha-runner-scale-set-controller` and `gha-runner-scale-set`
- GitHub username: `justanotherspy`
- Docker Hub account: `danielschwartzlol`

## Prerequisites

### Required Tools

1. **Docker** - For running containers and Kind cluster

   ```bash
   # Ubuntu/Debian
   sudo apt-get update
   sudo apt-get install docker.io
   sudo usermod -aG docker $USER
   # Log out and back in for group changes to take effect
   ```

2. **Kind** - Kubernetes in Docker

   ```bash
   # Install Kind
   curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
   chmod +x ./kind
   sudo mv ./kind /usr/local/bin/kind
   ```

3. **kubectl** - Kubernetes CLI

   ```bash
   curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
   chmod +x kubectl
   sudo mv kubectl /usr/local/bin/
   ```

4. **Helm** - Kubernetes package manager

   ```bash
   curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
   ```

5. **Go** - For building the controller (1.21+)

   ```bash
   # Install Go 1.21
   wget https://go.dev/dl/go1.21.5.linux-amd64.tar.gz
   sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.21.5.linux-amd64.tar.gz
   export PATH=$PATH:/usr/local/go/bin
   echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
   ```

### Environment Variables

Add these to your `.bashrc` or `.zshrc`:

```bash
# Docker Hub Configuration
export DOCKER_USER="danielschwartzlol"
export CONTROLLER_IMAGE="${DOCKER_USER}/gha-runner-scale-set-controller"
export RUNNER_IMAGE="ghcr.io/actions/actions-runner"  # Official runner image

# GitHub Configuration
export GITHUB_TOKEN="your-github-pat-token-here"
export GITHUB_USERNAME="justanotherspy"

# Or for GitHub App authentication (recommended):
# export APP_ID="your-app-id"
# export INSTALLATION_ID="your-installation-id"
# export PRIVATE_KEY_FILE_PATH="/path/to/private-key.pem"

# Test Repository Configuration
export TEST_REPO="${GITHUB_USERNAME}/test-runner-repo"
export TEST_ORG=""  # Optional: Your test organization

# Development Settings
export VERSION="dev"
export CLUSTER_NAME="arc-dev"
```

## Step 1: Build the Controller Image

```bash
# Build the controller image with scale set mode
make docker-build

# Tag it for our use
docker tag ${DOCKER_USER}/actions-runner-controller:${VERSION} \
           ${CONTROLLER_IMAGE}:${VERSION}
```

## Step 2: Create Kind Cluster

Create a simple Kind cluster (no special config needed for new mode):

```bash
# Create Kind cluster
cat <<EOF | kind create cluster --name ${CLUSTER_NAME} --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
EOF

# Verify cluster is running
kubectl cluster-info --context kind-${CLUSTER_NAME}
```

## Step 3: Load Controller Image into Kind

```bash
# Load the controller image
kind load docker-image ${CONTROLLER_IMAGE}:${VERSION} --name ${CLUSTER_NAME}

# Verify image is loaded
docker exec -it ${CLUSTER_NAME}-control-plane crictl images | grep ${DOCKER_USER}
```

## Step 4: Create GitHub Authentication Secret

```bash
# Create namespace
kubectl create namespace arc-systems

# For PAT authentication
kubectl create secret generic github-auth \
  --namespace=arc-systems \
  --from-literal=github_token=${GITHUB_TOKEN}

# For GitHub App authentication (if using App instead)
kubectl create secret generic github-auth \
  --namespace=arc-systems \
  --from-file=github_app_id=${APP_ID} \
  --from-file=github_app_installation_id=${INSTALLATION_ID} \
  --from-file=github_app_private_key=${PRIVATE_KEY_FILE_PATH}
```

## Step 5: Install Runner Scale Set Controller

### Option A: Using Helm (Recommended)

```bash
# Install the controller
helm install arc-controller \
  --namespace arc-systems \
  --create-namespace \
  oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller \
  --version 0.12.1 \
  --set image.repository=${CONTROLLER_IMAGE} \
  --set image.tag=${VERSION} \
  --set imagePullPolicy=Never

# Verify controller is running
kubectl -n arc-systems get pods -l app.kubernetes.io/name=gha-runner-scale-set-controller
```

### Option B: Manual Deployment (for development)

```bash
# Run the controller locally (for debugging)
CONTROLLER_MANAGER_POD_NAMESPACE=arc-systems \
CONTROLLER_MANAGER_CONTAINER_IMAGE="${CONTROLLER_IMAGE}:${VERSION}" \
make run-scaleset
```

## Step 6: Deploy Runner Scale Set

Create a runner scale set for your repository:

```bash
# Install runner scale set
helm install arc-runner-set \
  --namespace arc-runners \
  --create-namespace \
  oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set \
  --version 0.12.1 \
  --set githubConfigUrl="https://github.com/${TEST_REPO}" \
  --set githubConfigSecret="github-auth" \
  --set controllerServiceAccount.namespace="arc-systems" \
  --set controllerServiceAccount.name="arc-controller-gha-rs-controller" \
  --set minRunners=1 \
  --set maxRunners=10 \
  --set runnerGroup="default" \
  --set runnerScaleSetName="test-scale-set"

# Watch the runner scale set
kubectl -n arc-runners get autoscalingrunnersets -w
kubectl -n arc-runners get ephemeralrunnersets -w
kubectl -n arc-runners get ephemeralrunners -w
```

## Step 7: Verify Installation

```bash
# Check controller logs
kubectl -n arc-systems logs -l app.kubernetes.io/name=gha-runner-scale-set-controller -f

# Check listener logs
kubectl -n arc-systems logs -l app.kubernetes.io/name=arc-runner-set-listener -f

# Check runner pods
kubectl -n arc-runners get pods

# Get runner scale set status
kubectl -n arc-runners get autoscalingrunnersets -o wide
```

## Development Workflow

### Quick Iteration for Controller Changes

```bash
# 1. Make your code changes

# 2. Rebuild controller
VERSION=dev-$(date +%s) make docker-build
docker tag ${DOCKER_USER}/actions-runner-controller:${VERSION} \
           ${CONTROLLER_IMAGE}:${VERSION}

# 3. Load into Kind
kind load docker-image ${CONTROLLER_IMAGE}:${VERSION} --name ${CLUSTER_NAME}

# 4. Update the deployment
kubectl -n arc-systems set image deployment/arc-controller-gha-rs-controller \
  manager=${CONTROLLER_IMAGE}:${VERSION}

# 5. Watch logs
kubectl -n arc-systems logs -l app.kubernetes.io/name=gha-runner-scale-set-controller -f
```

### Testing Parallel Runner Creation

```bash
# Scale up to test parallel creation
kubectl -n arc-runners patch autoscalingrunnerset arc-runner-set-runner-set \
  --type merge \
  -p '{"spec":{"maxRunners":50}}'

# Trigger scale up by running workflows in your test repo
# Or manually patch the ephemeralrunnerset
kubectl -n arc-runners patch ephemeralrunnerset <name> \
  --type merge \
  -p '{"spec":{"replicas":50}}'

# Monitor creation time
time kubectl -n arc-runners wait --for=condition=Ready ephemeralrunners --all --timeout=600s

# Check metrics
kubectl -n arc-systems port-forward service/arc-controller-gha-rs-controller 8080:80
curl http://localhost:8080/metrics | grep ephemeral
```

## Debugging

### Enable Verbose Logging

```bash
# Update controller deployment with debug logging
kubectl -n arc-systems edit deployment arc-controller-gha-rs-controller

# Add to container args:
# - "--log-level=debug"
```

### Common Commands

```bash
# Get all resources
kubectl get all -n arc-systems
kubectl get all -n arc-runners

# Describe runner set
kubectl -n arc-runners describe autoscalingrunnerset

# Get events
kubectl -n arc-runners get events --sort-by='.lastTimestamp'

# Port forward for pprof debugging
kubectl -n arc-systems port-forward deployment/arc-controller-gha-rs-controller 6060:6060
go tool pprof http://localhost:6060/debug/pprof/profile
```

## Performance Testing Script

```bash
#!/bin/bash
# perf-test.sh

NAMESPACE="arc-runners"
REPLICAS="${1:-100}"

echo "Testing creation of ${REPLICAS} runners..."

# Record start time
START=$(date +%s)

# Scale up
kubectl -n ${NAMESPACE} patch ephemeralrunnerset $(kubectl -n ${NAMESPACE} get ers -o name | head -1) \
  --type merge \
  -p "{\"spec\":{\"replicas\":${REPLICAS}}}"

# Wait for all runners
kubectl -n ${NAMESPACE} wait --for=condition=Ready ephemeralrunners --all --timeout=600s

# Record end time
END=$(date +%s)
DURATION=$((END - START))

echo "Created ${REPLICAS} runners in ${DURATION} seconds"
echo "Average time per runner: $((DURATION / REPLICAS)) seconds"

# Get runner creation events
kubectl -n ${NAMESPACE} get events --field-selector reason=Created | grep EphemeralRunner
```

## Cleanup

```bash
# Delete runner scale set
helm uninstall arc-runner-set -n arc-runners

# Delete controller
helm uninstall arc-controller -n arc-systems

# Delete namespaces
kubectl delete namespace arc-systems arc-runners

# Delete Kind cluster
kind delete cluster --name ${CLUSTER_NAME}
```

## Troubleshooting

### Runner Scale Set Not Creating Runners

```bash
# Check if runner scale set is registered
kubectl -n arc-runners get autoscalingrunnerset -o yaml | grep runnerScaleSetId

# Check GitHub API connectivity
kubectl -n arc-systems exec -it deployment/arc-controller-gha-rs-controller -- \
  curl -H "Authorization: token ${GITHUB_TOKEN}" \
  https://api.github.com/repos/${TEST_REPO}/actions/runners/registration-token
```

### Runners Not Picking Up Jobs

```bash
# Ensure runner group matches your workflow
# In workflow file:
# runs-on: [self-hosted, linux, x64, default]  # default = runner group

# Check runner registration
kubectl -n arc-runners logs -l app.kubernetes.io/component=runner --tail=100
```

## Key Differences from Legacy Mode

1. **No Cert-Manager**: New mode doesn't use admission webhooks
2. **Different CRDs**: Uses `AutoscalingRunnerSet`, `EphemeralRunnerSet`, `EphemeralRunner`
3. **Separate Helm Charts**: `gha-runner-scale-set-controller` and `gha-runner-scale-set`
4. **Listener Pod**: Runs in controller namespace, handles GitHub webhooks
5. **No Runner Deployment**: Only uses ephemeral runners

## Resources

- [Runner Scale Set Documentation](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/deploying-runner-scale-sets-with-actions-runner-controller)
- [ARC Helm Charts](https://github.com/actions/actions-runner-controller/tree/master/charts)
- [Kind Documentation](https://kind.sigs.k8s.io/)