# Fork Setup Guide

This guide helps you configure your fork of the Actions Runner Controller repository to work with your own Docker Hub account and GitHub repositories, ensuring CI workflows work properly in your fork.

## Repository Overview

- **Fork**: `justanotherspy/actions-runner-controller`
- **Upstream**: `actions/actions-runner-controller`
- **Docker Hub**: `danielschwartzlol`
- **Test Repository**: `justanotherspy/test-runner-repo`

## Prerequisites

### 1. GitHub App Setup (Required for E2E Tests)

Create a GitHub App for testing:

1. Go to GitHub Settings → Developer settings → GitHub Apps
2. Click "New GitHub App"
3. Fill in details:
   - **App name**: `arc-e2e-tests-justanotherspy`
   - **Homepage URL**: `https://github.com/justanotherspy/actions-runner-controller`
   - **Webhook URL**: Leave blank or use placeholder
   - **Repository permissions**:
     - Actions: Read & Write
     - Contents: Read & Write
     - Pull requests: Read & Write
     - Metadata: Read
   - **Organization permissions**:
     - Self-hosted runners: Write
4. Generate and download the private key
5. Note the App ID and Installation ID

### 2. Docker Hub Setup

1. Ensure you have a Docker Hub account: `danielschwartzlol`
2. Create a Docker Hub access token:
   - Go to Docker Hub → Account Settings → Security
   - Generate new access token with Read/Write permissions

### 3. Test Repository Setup

Create a test repository for E2E tests:

1. Create repository: `justanotherspy/test-runner-repo`
2. Make it public (required for GitHub Actions to work)
3. Add a simple workflow file for testing:

```yaml
# .github/workflows/arc-test-workflow.yaml
name: ARC Test Workflow
on:
  workflow_dispatch:
jobs:
  test:
    runs-on: [self-hosted, linux, x64, default]
    steps:
      - name: Hello World
        run: echo "Hello from ARC runner!"
```

## Repository Secrets Configuration

Add these secrets to your fork (`justanotherspy/actions-runner-controller`):

### Required Secrets

1. **DOCKERHUB_USERNAME**: `danielschwartzlol`
2. **DOCKERHUB_TOKEN**: Your Docker Hub access token
3. **E2E_TESTS_ACCESS_APP_ID**: Your GitHub App ID
4. **E2E_TESTS_ACCESS_PK**: Your GitHub App private key (full PEM content)

### Optional Secrets (for other workflows)

1. **ACTIONS_ACCESS_APP_ID**: GitHub App ID for releases (if needed)
2. **ACTIONS_ACCESS_PK**: GitHub App private key for releases (if needed)

## Workflow Files to Modify

The following workflows need updates to work with your fork:

### 1. Update E2E Test Configuration

Edit `.github/workflows/gha-e2e-tests.yaml`:

```yaml
env:
  TARGET_ORG: justanotherspy  # Changed from actions-runner-controller
  TARGET_REPO: test-runner-repo  # Changed from arc_e2e_test_dummy
  IMAGE_NAME: "danielschwartzlol/arc-test-image"  # Changed from arc-test-image
  IMAGE_VERSION: "0.12.1"
```

### 2. Update Canary Build Configuration  

Edit `.github/workflows/global-publish-canary.yaml`:

```yaml
env:
  PUSH_TO_REGISTRIES: false  # Set to true when ready for real pushes

jobs:
  legacy-canary-build:
    env:
      DOCKERHUB_USERNAME: ${{ secrets.DOCKERHUB_USERNAME }}
      TARGET_ORG: justanotherspy  # Changed from actions-runner-controller
      TARGET_REPO: actions-runner-controller  # Your fork name

  canary-build:
    steps:
      - name: Build and Push
        with:
          tags: |
            ghcr.io/${{ steps.resolve_parameters.outputs.repository_owner }}/gha-runner-scale-set-controller:canary
            ghcr.io/${{ steps.resolve_parameters.outputs.repository_owner }}/gha-runner-scale-set-controller:canary-${{ steps.resolve_parameters.outputs.short_sha }}
            danielschwartzlol/gha-runner-scale-set-controller:canary  # Add Docker Hub push
            danielschwartzlol/gha-runner-scale-set-controller:canary-${{ steps.resolve_parameters.outputs.short_sha }}
```

### 3. Disable Upstream-Specific Workflows

Disable or modify these workflows that are specific to the upstream repo:

```bash
# Disable workflows that interact with upstream release repo
mv .github/workflows/arc-publish.yaml .github/workflows/arc-publish.yaml.disabled
mv .github/workflows/arc-release-runners.yaml .github/workflows/arc-release-runners.yaml.disabled
mv .github/workflows/arc-update-runners-scheduled.yaml .github/workflows/arc-update-runners-scheduled.yaml.disabled
```

### 4. Update Docker Build Workflows

For any remaining docker build workflows, ensure they use your Docker Hub account:

```yaml
env:
  DOCKER_REGISTRY: docker.io
  DOCKER_REPOSITORY: danielschwartzlol
```

## GitHub Actions Setup Script

Create this setup script and run it once:

```bash
#!/bin/bash
# setup-fork.sh

set -e

echo "Setting up fork for justanotherspy/actions-runner-controller"

# Update E2E test configuration
sed -i 's/TARGET_ORG: actions-runner-controller/TARGET_ORG: justanotherspy/' .github/workflows/gha-e2e-tests.yaml
sed -i 's/TARGET_REPO: arc_e2e_test_dummy/TARGET_REPO: test-runner-repo/' .github/workflows/gha-e2e-tests.yaml
sed -i 's/IMAGE_NAME: "arc-test-image"/IMAGE_NAME: "danielschwartzlol\/arc-test-image"/' .github/workflows/gha-e2e-tests.yaml

# Disable upstream-specific workflows
for workflow in arc-publish arc-release-runners arc-update-runners-scheduled; do
  if [ -f ".github/workflows/${workflow}.yaml" ]; then
    mv ".github/workflows/${workflow}.yaml" ".github/workflows/${workflow}.yaml.disabled"
    echo "Disabled ${workflow}.yaml"
  fi
done

# Update canary build to use your Docker Hub
sed -i 's/TARGET_ORG: actions-runner-controller/TARGET_ORG: justanotherspy/' .github/workflows/global-publish-canary.yaml

echo "Fork setup complete!"
echo ""
echo "Next steps:"
echo "1. Add required secrets to GitHub repository settings"
echo "2. Create test repository: justanotherspy/test-runner-repo" 
echo "3. Test the E2E workflow"
```

## Testing Your Setup

### 1. Test Basic CI

Push changes and verify the basic workflows pass:

```bash
git add .
git commit -m "Configure fork for justanotherspy"
git push origin feature/setup-runner-scale-set-development
```

Check that these workflows pass:
- Go (format, lint, generate, test)
- Chart validation

### 2. Test E2E Workflows

Run E2E tests manually:

1. Go to Actions tab in your fork
2. Select "E2E Tests" workflow  
3. Click "Run workflow"
4. Monitor the results

### 3. Test Docker Builds

Test canary image building:

1. Enable canary builds by setting `PUSH_TO_REGISTRIES: true`
2. Push to master branch
3. Verify images are pushed to your Docker Hub account

## Environment Variables for Local Development

Update your local `.bashrc` or `.zshrc`:

```bash
# Fork-specific configuration
export GITHUB_USERNAME="justanotherspy"
export DOCKER_USER="danielschwartzlol" 
export TEST_REPO="${GITHUB_USERNAME}/test-runner-repo"

# GitHub App Configuration (for local E2E testing)
export E2E_APP_ID="your-app-id"
export E2E_INSTALLATION_ID="your-installation-id"
export E2E_PRIVATE_KEY_FILE="/path/to/your-private-key.pem"

# Docker Hub
export DOCKERHUB_USERNAME="danielschwartzlol"
export DOCKERHUB_TOKEN="your-docker-hub-token"
```

## Makefile Updates

Update the Makefile to use your Docker Hub account by default:

```makefile
# Add to Makefile
ifdef DOCKER_USER
	DOCKER_IMAGE_NAME ?= ${DOCKER_USER}/gha-runner-scale-set-controller
else
	DOCKER_IMAGE_NAME ?= danielschwartzlol/gha-runner-scale-set-controller
endif
```

## Keeping Fork Up to Date

Periodically sync with upstream:

```bash
# Add upstream remote (if not already added)
git remote add upstream https://github.com/actions/actions-runner-controller.git

# Sync with upstream
git fetch upstream
git checkout master
git merge upstream/master

# Push updates to your fork
git push origin master

# Reapply fork-specific changes if needed
# Re-run setup-fork.sh if necessary
```

## Troubleshooting

### Common Issues

1. **E2E Tests Fail with "Repository not found"**
   - Ensure `justanotherspy/test-runner-repo` exists and is public
   - Verify GitHub App has access to the repository

2. **Docker Push Fails**
   - Check Docker Hub token permissions
   - Verify `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` secrets

3. **GitHub App Authentication Fails**
   - Ensure private key is correctly formatted (include headers/footers)
   - Check App ID and Installation ID are correct
   - Verify App permissions include required scopes

4. **Workflows Don't Trigger**
   - Check that workflow files are in `.github/workflows/` (not disabled)
   - Verify branch protection rules aren't blocking workflows
   - Check if workflows are limited to specific paths

### Debug Commands

```bash
# Test GitHub App authentication locally
curl -H "Authorization: token $(gh auth token)" \
  https://api.github.com/repos/justanotherspy/test-runner-repo

# Test Docker Hub authentication
docker login -u danielschwartzlol -p $DOCKERHUB_TOKEN

# Check workflow permissions
gh api repos/justanotherspy/actions-runner-controller/actions/workflows
```

## Security Considerations

1. **Never commit secrets** to the repository
2. **Use GitHub App authentication** instead of PAT tokens where possible
3. **Limit Docker Hub token permissions** to only what's needed
4. **Regularly rotate secrets** and tokens
5. **Use environment-specific secrets** (don't reuse production secrets for testing)

## Next Steps

After completing this setup:

1. Follow the [ENV_SETUP.md](./ENV_SETUP.md) guide for local development
2. Implement the performance optimizations described in [tasks.md](./tasks.md)
3. Test your changes with the E2E workflows
4. Create PRs within your fork for code review

Your fork is now ready for independent development and CI/CD workflows!