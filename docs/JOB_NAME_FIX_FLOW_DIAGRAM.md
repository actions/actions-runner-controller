# Technical Flow Diagram - job_name Metrics Fix

## Before the Fix

```
GitHub Actions API
       |
       | Sends JSON with "jobDisplayName" field (old format)
       | OR "jobName" field (new format since ~June 9, 2025)
       v
[Listener] json.Unmarshal → JobMessageBase
       |
       | Only captures "jobDisplayName" field
       | (JobDisplayName string `json:"jobDisplayName"`)
       v
[Worker/Metrics] Direct access: jobBase.JobDisplayName
       |
       | If GitHub sends "jobName" instead → JobDisplayName is empty ""
       v
❌ Metrics: job_name="" (EMPTY)
```

## After the Fix

```
GitHub Actions API
       |
       | Sends JSON with either field:
       | - "jobDisplayName" (old format) OR
       | - "jobName" (new format)
       v
[Listener] json.Unmarshal → JobMessageBase
       |
       | Captures BOTH fields:
       | - JobDisplayName string `json:"jobDisplayName"`
       | - JobName string `json:"jobName"`
       v
[Worker/Metrics] Smart access: jobBase.GetJobName()
       |
       | GetJobName() implements fallback logic:
       | 1. Return JobDisplayName if not empty (preferred)
       | 2. Otherwise return JobName (fallback)
       v
✅ Metrics: job_name="actual-job-name" (POPULATED)
```

## GetJobName() Method Logic

```go
func (j *JobMessageBase) GetJobName() string {
    if j.JobDisplayName != "" {
        return j.JobDisplayName  // ← Preferred (backward compatible)
    }
    return j.JobName             // ← Fallback (forward compatible)
}
```

## Data Flow Examples

### Example 1: Old API Format (Pre-June 2025)
```json
{
  "jobDisplayName": "Build and Test",
  "jobName": ""
}
```
→ GetJobName() returns: **"Build and Test"** ✅

### Example 2: New API Format (Post-June 2025)
```json
{
  "jobDisplayName": "",
  "jobName": "Build and Test"
}
```
→ GetJobName() returns: **"Build and Test"** ✅

### Example 3: Both Fields Present
```json
{
  "jobDisplayName": "Build and Test (Display)",
  "jobName": "build-test"
}
```
→ GetJobName() returns: **"Build and Test (Display)"** (prefers JobDisplayName) ✅

## Impact on Metrics

### Before Fix
```prometheus
gha_job_startup_duration_seconds_sum{
  event_name="push",
  job_name="",                    # ← EMPTY
  job_result="success",
  job_workflow_ref="...",
  organization="myorg",
  repository="myrepo"
} 120
```

### After Fix
```prometheus
gha_job_startup_duration_seconds_sum{
  event_name="push",
  job_name="Build and Test",      # ← POPULATED
  job_result="success",
  job_workflow_ref="...",
  organization="myorg",
  repository="myrepo"
} 120
```

## Benefits Summary

| Aspect | Before | After |
|--------|--------|-------|
| Backward Compatibility | ✅ Works with old API | ✅ Works with old API |
| Forward Compatibility | ❌ Breaks with new API | ✅ Works with new API |
| Resilience | ❌ Single field dependency | ✅ Dual field fallback |
| Debugging | ❌ Limited visibility | ✅ Enhanced logging |
| Metrics Quality | ❌ Empty labels | ✅ Populated labels |
