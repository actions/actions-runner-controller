package testing

const (
	ActionsCheckoutV2 = "actions/checkout@v2"
)

type Workflow struct {
	Name string         `json:"name"`
	On   On             `json:"on"`
	Jobs map[string]Job `json:"jobs"`
}

type On struct {
	Push             *Push             `json:"push,omitempty"`
	WorkflowDispatch *WorkflowDispatch `json:"workflow_dispatch,omitempty"`
}

type Push struct {
	Branches []string `json:"branches,omitempty"`
}

type WorkflowDispatch struct {
	Inputs map[string]InputSpec `json:"inputs,omitempty"`
}

type InputSpec struct {
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Default     string `json:"default,omitempty"`
}

type Job struct {
	RunsOn string `json:"runs-on"`
	Steps  []Step `json:"steps"`
}

type Step struct {
	Name string `json:"name,omitempty"`
	Uses string `json:"uses,omitempty"`
	With *With  `json:"with,omitempty"`
	Run  string `json:"run,omitempty"`
}

type With struct {
	Version string `json:"version,omitempty"`
}
