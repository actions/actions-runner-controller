# Runner Scale Set Controller Performance Optimization

## Problem Analysis

Based on analysis of the codebase, the runner scale set controller currently spawns runners **sequentially** in the `EphemeralRunnerSetReconciler.createEphemeralRunners()` method at `/controllers/actions.github.com/ephemeralrunnerset_controller.go:359-386`.

### Current Sequential Implementation Issues:
1. **Linear time complexity O(n)**: Creating n runners takes n sequential API calls
2. **Blocking loop**: Each runner creation blocks until the API call completes
3. **Poor scalability**: Large scale-ups (e.g., 100+ runners) take minutes
4. **Resource underutilization**: Controller pod doesn't leverage available CPU/memory for parallel operations

### Key Bottlenecks Identified:
- **EphemeralRunnerSet Controller** (`ephemeralrunnerset_controller.go:362-383`): Sequential for-loop creating runners one by one
- **API Call Latency**: Each `r.Create(ctx, ephemeralRunner)` call blocks for network roundtrip
- **No batching**: Individual API calls instead of batch operations
- **No concurrency**: Single-threaded execution path

## Proposed Task List for Performance Improvement

### Phase 1: Research & Design (Week 1)
- [ ] **Task 1.1**: Benchmark current performance
  - Measure time to create 10, 50, 100, 500 runners
  - Profile CPU/memory usage during scale-up
  - Document baseline metrics for comparison

- [ ] **Task 1.2**: Research Kubernetes client-go patterns for concurrent resource creation
  - Study controller-runtime workqueue patterns
  - Investigate rate limiting considerations
  - Review best practices for bulk operations

- [ ] **Task 1.3**: Design concurrent runner creation architecture
  - Define optimal concurrency level (suggest: configurable, default 10)
  - Design error handling and retry strategy
  - Plan backward compatibility approach

### Phase 2: Implementation (Week 2-3)

- [ ] **Task 2.1**: Refactor `createEphemeralRunners` for parallel execution
  ```go
  // Suggested approach:
  // - Use worker pool pattern with configurable concurrency
  // - Implement error aggregation
  // - Add progress tracking
  ```

- [ ] **Task 2.2**: Implement configurable concurrency controls
  - Add `--runner-creation-concurrency` flag (default: 10)
  - Add `--runner-creation-timeout` flag (default: 30s)
  - Environment variable overrides for containerized deployments

- [ ] **Task 2.3**: Add comprehensive error handling
  - Implement exponential backoff for failed creations
  - Partial success handling (some runners created, some failed)
  - Detailed error reporting and metrics

- [ ] **Task 2.4**: Implement progress tracking and observability
  - Add prometheus metrics for creation time per runner
  - Log progress at intervals (e.g., "Created 50/100 runners")
  - Add events to AutoscalingRunnerSet for visibility

### Phase 3: Testing (Week 3-4)

- [ ] **Task 3.1**: Unit tests for concurrent creation
  - Test with mock client
  - Verify error handling
  - Test concurrency limits
  - Test partial failures

- [ ] **Task 3.2**: Integration tests
  - Test with real Kubernetes API
  - Verify resource creation order
  - Test rollback on failure
  - Test with various concurrency levels

- [ ] **Task 3.3**: Load testing
  - Test creating 100+ runners simultaneously
  - Monitor API server impact
  - Measure improvement vs baseline
  - Test with rate limiting

- [ ] **Task 3.4**: Chaos testing
  - Test with network failures
  - Test with API server throttling
  - Test with partial quota exhaustion
  - Test controller restart during creation

### Phase 4: Optimization & Tuning (Week 4-5)

- [ ] **Task 4.1**: Implement adaptive concurrency
  - Start with low concurrency, increase based on success rate
  - Back off on errors or throttling
  - Self-tuning based on cluster capacity

- [ ] **Task 4.2**: Add bulk creation API support (if available)
  - Research if Actions API supports bulk runner registration
  - Implement batch registration if supported
  - Fall back to parallel individual creation

- [ ] **Task 4.3**: Optimize resource creation
  - Pre-compute runner configurations
  - Cache common data (secrets, configs)
  - Minimize API calls per runner

### Phase 5: Documentation & Rollout (Week 5-6)

- [ ] **Task 5.1**: Document configuration options
  - Update CLAUDE.md with new flags
  - Add tuning guide for different cluster sizes
  - Document performance improvements

- [ ] **Task 5.2**: Create migration guide
  - Document any breaking changes
  - Provide upgrade path
  - Include rollback procedures

- [ ] **Task 5.3**: Performance report
  - Before/after benchmarks
  - Scalability analysis
  - Recommendations for different use cases

## Implementation Details

### Suggested Code Structure

```go
// ephemeralrunnerset_controller.go

type runnerCreationJob struct {
    runner *v1alpha1.EphemeralRunner
    index  int
    err    error
}

func (r *EphemeralRunnerSetReconciler) createEphemeralRunnersParallel(
    ctx context.Context, 
    runnerSet *v1alpha1.EphemeralRunnerSet, 
    count int, 
    log logr.Logger,
) error {
    concurrency := r.getConfiguredConcurrency() // Default: 10
    
    jobs := make(chan runnerCreationJob, count)
    results := make(chan runnerCreationJob, count)
    
    // Start workers
    var wg sync.WaitGroup
    for i := 0; i < concurrency; i++ {
        wg.Add(1)
        go r.runnerCreationWorker(ctx, runnerSet, jobs, results, &wg, log)
    }
    
    // Queue jobs
    for i := 0; i < count; i++ {
        jobs <- runnerCreationJob{
            runner: r.newEphemeralRunner(runnerSet),
            index:  i,
        }
    }
    close(jobs)
    
    // Wait for completion
    go func() {
        wg.Wait()
        close(results)
    }()
    
    // Collect results and handle errors
    var errs []error
    created := 0
    for result := range results {
        if result.err != nil {
            errs = append(errs, result.err)
        } else {
            created++
            if created%10 == 0 || created == count {
                log.Info("Runner creation progress", "created", created, "total", count)
            }
        }
    }
    
    return multierr.Combine(errs...)
}
```

## Success Metrics

1. **Performance**: 
   - Target: Create 100 runners in < 30 seconds (vs current ~5 minutes)
   - Reduce time complexity from O(n) to O(n/c) where c = concurrency

2. **Reliability**:
   - Handle partial failures gracefully
   - No runner leaks on error
   - Proper cleanup on controller restart

3. **Observability**:
   - Clear progress tracking
   - Detailed metrics and logs
   - Actionable error messages

4. **Compatibility**:
   - Backward compatible by default
   - Configurable for different environments
   - No breaking changes to CRDs

## Risk Mitigation

1. **API Server Overload**: Implement rate limiting and backoff
2. **Resource Exhaustion**: Add memory/CPU limits and monitoring
3. **Partial Failures**: Implement proper rollback and cleanup
4. **Race Conditions**: Use proper locking and atomic operations

## Testing Requirements

- Unit test coverage > 80%
- Integration tests for all scenarios
- Performance regression tests
- Documentation for all new features
- Backward compatibility tests

## Rollout Plan

1. **Alpha**: Deploy to dev environment with conservative defaults
2. **Beta**: Test with select users, gather feedback
3. **GA**: Full rollout with documentation and migration guide

## Dependencies

- No changes to CRDs required
- Compatible with existing Actions Runner Controller versions
- Requires Go 1.21+ for errors.Join support (already in use)

## Timeline Estimate

- Total Duration: 5-6 weeks
- Developer Resources: 1-2 engineers
- Review & Testing: Additional 1 week

## Notes for Implementation

1. Consider using `golang.org/x/sync/errgroup` for cleaner error handling
2. Leverage existing `multierr` package for error aggregation
3. Use context cancellation for proper cleanup
4. Consider implementing circuit breaker pattern for API failures
5. Add feature flag to enable/disable parallel creation