package metrics

import (
	"path"
	"strings"
)

// WorkflowRefInfo contains parsed information from a job_workflow_ref
type WorkflowRefInfo struct {
	// Name is the workflow file name without extension
	Name string
	// Target is the target ref with type prefix retained for clarity
	// Examples:
	//   - heads/main (branch)
	//   - heads/feature/new-feature (branch)
	//   - tags/v1.2.3 (tag)
	//   - pull/123 (pull request)
	Target string
}

// ParseWorkflowRef parses a job_workflow_ref string to extract workflow name and target
// Format: {owner}/{repo}/.github/workflows/{workflow_file}@{ref}
// Example: mygithuborg/myrepo/.github/workflows/blank.yml@refs/heads/main
//
// The target field preserves type prefixes to differentiate between:
//   - Branch references: "heads/{branch}" (from refs/heads/{branch})
//   - Tag references: "tags/{tag}" (from refs/tags/{tag})
//   - Pull requests: "pull/{number}" (from refs/pull/{number}/merge)
func ParseWorkflowRef(workflowRef string) WorkflowRefInfo {
	info := WorkflowRefInfo{}

	if workflowRef == "" {
		return info
	}

	// Split by @ to separate path and ref
	parts := strings.Split(workflowRef, "@")
	if len(parts) != 2 {
		return info
	}

	workflowPath := parts[0]
	ref := parts[1]

	// Extract workflow name from path
	// The path format is: {owner}/{repo}/.github/workflows/{workflow_file}
	workflowFile := path.Base(workflowPath)
	// Remove .yml or .yaml extension
	info.Name = strings.TrimSuffix(strings.TrimSuffix(workflowFile, ".yml"), ".yaml")

	// Extract target from ref based on type
	// Branch refs: refs/heads/{branch}
	// Tag refs: refs/tags/{tag}
	// PR refs: refs/pull/{number}/merge
	const (
		branchPrefix = "refs/heads/"
		tagPrefix    = "refs/tags/"
		prPrefix     = "refs/pull/"
	)

	switch {
	case strings.HasPrefix(ref, branchPrefix):
		// Keep "heads/" prefix to indicate branch
		info.Target = "heads/" + strings.TrimPrefix(ref, branchPrefix)
	case strings.HasPrefix(ref, tagPrefix):
		// Keep "tags/" prefix to indicate tag
		info.Target = "tags/" + strings.TrimPrefix(ref, tagPrefix)
	case strings.HasPrefix(ref, prPrefix):
		// Extract PR number from refs/pull/{number}/merge
		// Keep "pull/" prefix to indicate pull request
		prPart := strings.TrimPrefix(ref, prPrefix)
		if idx := strings.Index(prPart, "/"); idx > 0 {
			info.Target = "pull/" + prPart[:idx]
		}
	}

	return info
}
