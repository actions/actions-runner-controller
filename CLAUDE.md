# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository Information

**THIS IS A FORK**: This repository is a fork of the upstream `actions/actions-runner-controller` repository.
- **Fork Owner**: `justanotherspy`
- **Upstream**: `actions/actions-runner-controller`
- **IMPORTANT**: Always push changes to the fork (`justanotherspy/actions-runner-controller`), NEVER to upstream
- **Default Branch**: Work on feature branches, not directly on master

## Project Focus

**IMPORTANT**: We work EXCLUSIVELY on the NEW Runner Scale Set Controller mode, NOT the legacy mode.

- **NEW Mode ONLY**: Autoscaling Runner Sets using `actions.github.com` API group
- **NO Legacy Development**: Do not work on `actions.summerwind.net` resources
- **NO Cert-Manager**: The new mode doesn't use webhooks or cert-manager
- **GitHub Username**: `justanotherspy` (for test repositories)
- **Docker Hub Account**: `danielschwartzlol`

## Development Configuration

- **Controller Image**: `danielschwartzlol/gha-runner-scale-set-controller`
- **Runner Image**: Use official `ghcr.io/actions/actions-runner`
- **Helm Charts** (Version 0.12.1):
  - Controller: `gha-runner-scale-set-controller`
  - Runner Set: `gha-runner-scale-set`
- **Helm Chart Version**: Always use `0.12.1` (latest as of this setup)
- **Local Development**: Use Kind cluster without cert-manager (see ENV_SETUP.md)
- **Test Repository**: `justanotherspy/test-runner-repo`

## Key Components (New Mode Only)

### Controllers to Focus On

**AutoscalingRunnerSetReconciler** (`controllers/actions.github.com/autoscalingrunnerset_controller.go`)
- Manages runner scale set lifecycle
- Creates EphemeralRunnerSets based on demand
- Handles runner group configuration

**EphemeralRunnerSetReconciler** (`controllers/actions.github.com/ephemeralrunnerset_controller.go`)
- **CRITICAL FOR OPTIMIZATION**: Contains sequential runner creation loop
- `createEphemeralRunners()` method at line 359-386 needs parallelization
- Manages replicas of EphemeralRunners

**EphemeralRunnerReconciler** (`controllers/actions.github.com/ephemeralrunner_controller.go`)
- Manages individual runner pods
- Handles runner registration with GitHub

**AutoscalingListenerReconciler** (`controllers/actions.github.com/autoscalinglistener_controller.go`)
- Manages the listener pod that receives GitHub webhooks
- Triggers scaling events

### Resource Hierarchy (New Mode)

```text
AutoscalingRunnerSet
  ├── AutoscalingListener (webhook receiver pod)
  └── EphemeralRunnerSet
      └── EphemeralRunner (Pod)
```

## Performance Optimization Focus

### Current Problem
- `EphemeralRunnerSetReconciler.createEphemeralRunners()` creates runners sequentially
- Time complexity: O(n) where n = number of runners
- Bottleneck location: `controllers/actions.github.com/ephemeralrunnerset_controller.go:362-383`

### Optimization Goal
- Implement parallel runner creation with worker pool pattern
- Target: 10x improvement (create 100 runners in < 30 seconds)
- Configurable concurrency (default: 10 parallel creations)

## Build Commands

```bash
# Build controller for runner scale set mode
make docker-build
docker tag danielschwartzlol/actions-runner-controller:dev \
           danielschwartzlol/gha-runner-scale-set-controller:dev

# Run controller locally in scale set mode
make run-scaleset

# Generate CRDs (only actions.github.com ones matter)
make manifests

# Run tests for new mode controllers
go test -v ./controllers/actions.github.com/...
```

## Testing Commands

```bash
# Unit tests for runner scale set controllers
go test -v ./controllers/actions.github.com/... -run TestEphemeralRunnerSet

# Integration tests for new mode
KUBEBUILDER_ASSETS="$(setup-envtest use 1.28 -p path)" \
  go test -v ./controllers/actions.github.com/...

# Benchmark runner creation
go test -bench=BenchmarkCreateEphemeralRunners ./controllers/actions.github.com/...
```

## Local Development Workflow

```bash
# 1. Create Kind cluster (no cert-manager needed)
kind create cluster --name arc-dev

# 2. Build and load controller
VERSION=dev make docker-build
docker tag danielschwartzlol/actions-runner-controller:dev \
           danielschwartzlol/gha-runner-scale-set-controller:dev
kind load docker-image danielschwartzlol/gha-runner-scale-set-controller:dev --name arc-dev

# 3. Install controller with Helm (v0.12.1)
helm install arc-controller \
  oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set-controller \
  --version 0.12.1 \
  --set image.repository=danielschwartzlol/gha-runner-scale-set-controller \
  --set image.tag=dev \
  --set imagePullPolicy=Never

# 4. Deploy runner scale set (v0.12.1)
helm install arc-runner-set \
  oci://ghcr.io/actions/actions-runner-controller-charts/gha-runner-scale-set \
  --version 0.12.1 \
  --set githubConfigUrl="https://github.com/justanotherspy/test-runner-repo" \
  --set githubConfigSecret="github-auth"
```

## Important Files for Optimization

### Primary Focus
- `controllers/actions.github.com/ephemeralrunnerset_controller.go` - Contains sequential creation logic
- `controllers/actions.github.com/ephemeralrunner_controller.go` - Individual runner management
- `controllers/actions.github.com/autoscalingrunnerset_controller.go` - Scale set orchestration

### Configuration
- `charts/gha-runner-scale-set-controller/` - Controller Helm chart
- `charts/gha-runner-scale-set/` - Runner set Helm chart
- `cmd/ghalistener/` - Listener pod that receives GitHub webhooks

### Tests
- `controllers/actions.github.com/ephemeralrunnerset_controller_test.go`
- `controllers/actions.github.com/ephemeralrunner_controller_test.go`

## Code Patterns for New Mode

### Creating Resources in Parallel
```go
// Example pattern for parallel creation
func (r *EphemeralRunnerSetReconciler) createEphemeralRunnersParallel(
    ctx context.Context,
    runnerSet *v1alpha1.EphemeralRunnerSet,
    count int,
    log logr.Logger,
) error {
    workers := 10 // Configurable
    jobs := make(chan int, count)
    results := make(chan error, count)
    
    // Start workers
    for w := 0; w < workers; w++ {
        go r.createRunnerWorker(ctx, runnerSet, jobs, results, log)
    }
    
    // Queue jobs
    for i := 0; i < count; i++ {
        jobs <- i
    }
    close(jobs)
    
    // Collect results
    var errs []error
    for i := 0; i < count; i++ {
        if err := <-results; err != nil {
            errs = append(errs, err)
        }
    }
    
    return multierr.Combine(errs...)
}
```

## GitHub API Integration

- Use `github.Client` interface for testability
- Implement exponential backoff for rate limiting
- Runner scale sets register with GitHub using JIT configuration
- Default runner group: "default"

## DO NOT Work On

- **Legacy Controllers**: Anything in `controllers/actions.summerwind.net/`
- **Cert-Manager**: Not used in new mode
- **Webhooks**: New mode uses listener pod instead
- **RunnerDeployment**: Legacy resource type
- **HorizontalRunnerAutoscaler**: Legacy autoscaling

## Testing Performance Improvements

```bash
# Create many runners to test parallel creation
kubectl -n arc-runners patch ephemeralrunnerset <name> \
  --type merge -p '{"spec":{"replicas":100}}'

# Monitor creation time
time kubectl -n arc-runners wait --for=condition=Ready \
  ephemeralrunners --all --timeout=600s

# Check controller metrics
kubectl port-forward -n arc-systems service/arc-controller 8080:80
curl http://localhost:8080/metrics | grep ephemeral_runner_creation_duration
```

## Key Metrics to Track

- `ephemeral_runner_creation_duration_seconds` - Time to create each runner
- `ephemeral_runner_set_replicas` - Current vs desired replicas
- `controller_runtime_reconcile_time_seconds` - Reconciliation performance

## Files Referenced

@ENV_SETUP.md - Complete setup guide for new mode
@tasks.md - Performance optimization task plan
@controllers/actions.github.com/ephemeralrunnerset_controller.go
@controllers/actions.github.com/ephemeralrunner_controller.go
@controllers/actions.github.com/autoscalingrunnerset_controller.go