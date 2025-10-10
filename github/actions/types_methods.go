package actions

// GetJobName returns the job display name with fallback logic.
// It first checks JobDisplayName (preferred), then falls back to JobName if JobDisplayName is empty.
// This method handles GitHub API changes where the job name field may be sent as either
// "jobDisplayName" (old format) or "jobName" (new format).
func (j *JobMessageBase) GetJobName() string {
	if j.JobDisplayName != "" {
		return j.JobDisplayName
	}
	return j.JobName
}
