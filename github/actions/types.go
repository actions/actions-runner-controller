package actions

import (
	"time"

	"github.com/google/uuid"
)

type AcquirableJobList struct {
	Count int             `json:"count"`
	Jobs  []AcquirableJob `json:"value"`
}

type AcquirableJob struct {
	AcquireJobUrl   string   `json:"acquireJobUrl"`
	MessageType     string   `json:"messageType"`
	RunnerRequestId int64    `json:"runnerRequestId"`
	RepositoryName  string   `json:"repositoryName"`
	OwnerName       string   `json:"ownerName"`
	JobWorkflowRef  string   `json:"jobWorkflowRef"`
	EventName       string   `json:"eventName"`
	RequestLabels   []string `json:"requestLabels"`
}

type Int64List struct {
	Count int     `json:"count"`
	Value []int64 `json:"value"`
}

type JobAvailable struct {
	AcquireJobUrl string `json:"acquireJobUrl"`
	JobMessageBase
}

type JobAssigned struct {
	JobMessageBase
}

type JobStarted struct {
	RunnerId   int    `json:"runnerId"`
	RunnerName string `json:"runnerName"`
	JobMessageBase
}

type JobCompleted struct {
	Result     string `json:"result"`
	RunnerId   int    `json:"runnerId"`
	RunnerName string `json:"runnerName"`
	JobMessageBase
}

type JobMessageType struct {
	MessageType string `json:"messageType"`
}

type JobMessageBase struct {
	JobMessageType
	RunnerRequestId int64    `json:"runnerRequestId"`
	RepositoryName  string   `json:"repositoryName"`
	OwnerName       string   `json:"ownerName"`
	JobWorkflowRef  string   `json:"jobWorkflowRef"`
	JobDisplayName  string   `json:"jobDisplayName"`
	WorkflowRunId   int64    `json:"workflowRunId"`
	EventName       string   `json:"eventName"`
	RequestLabels   []string `json:"requestLabels"`
	// QueueTime          *time.Time `json:"queueTime"`
	// ScaleSetAssignTime *time.Time `json:"scaleSetAssignTime"`
	// RunnerAssignTime   *time.Time `json:"runnerAssignTime"`
	// FinishTime         *time.Time `json:"finishTime"`
}

type Label struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type RunnerGroup struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	IsDefault bool   `json:"isDefaultGroup"`
}

type RunnerGroupList struct {
	Count        int           `json:"count"`
	RunnerGroups []RunnerGroup `json:"value"`
}

type RunnerScaleSet struct {
	Id                 int                      `json:"id,omitempty"`
	Name               string                   `json:"name,omitempty"`
	RunnerGroupId      int                      `json:"runnerGroupId,omitempty"`
	RunnerGroupName    string                   `json:"runnerGroupName,omitempty"`
	Labels             []Label                  `json:"labels,omitempty"`
	RunnerSetting      RunnerSetting            `json:"RunnerSetting,omitempty"`
	CreatedOn          time.Time                `json:"createdOn,omitempty"`
	RunnerJitConfigUrl string                   `json:"runnerJitConfigUrl,omitempty"`
	Statistics         *RunnerScaleSetStatistic `json:"statistics,omitempty"`
}

type RunnerScaleSetJitRunnerSetting struct {
	Name       string `json:"name"`
	WorkFolder string `json:"workFolder"`
}

type RunnerScaleSetMessage struct {
	MessageId   int64                    `json:"messageId"`
	MessageType string                   `json:"messageType"`
	Body        string                   `json:"body"`
	Statistics  *RunnerScaleSetStatistic `json:"statistics"`
}

type runnerScaleSetsResponse struct {
	Count           int              `json:"count"`
	RunnerScaleSets []RunnerScaleSet `json:"value"`
}

type RunnerScaleSetSession struct {
	SessionId               *uuid.UUID               `json:"sessionId,omitempty"`
	OwnerName               string                   `json:"ownerName,omitempty"`
	RunnerScaleSet          *RunnerScaleSet          `json:"runnerScaleSet,omitempty"`
	MessageQueueUrl         string                   `json:"messageQueueUrl,omitempty"`
	MessageQueueAccessToken string                   `json:"messageQueueAccessToken,omitempty"`
	Statistics              *RunnerScaleSetStatistic `json:"statistics,omitempty"`
}

type RunnerScaleSetStatistic struct {
	TotalAvailableJobs     int `json:"totalAvailableJobs"`
	TotalAcquiredJobs      int `json:"totalAcquiredJobs"`
	TotalAssignedJobs      int `json:"totalAssignedJobs"`
	TotalRunningJobs       int `json:"totalRunningJobs"`
	TotalRegisteredRunners int `json:"totalRegisteredRunners"`
	TotalBusyRunners       int `json:"totalBusyRunners"`
	TotalIdleRunners       int `json:"totalIdleRunners"`
}

type RunnerSetting struct {
	Ephemeral     bool `json:"ephemeral,omitempty"`
	IsElastic     bool `json:"isElastic,omitempty"`
	DisableUpdate bool `json:"disableUpdate,omitempty"`
}

type RunnerReferenceList struct {
	Count            int               `json:"count"`
	RunnerReferences []RunnerReference `json:"value"`
}

type RunnerReference struct {
	Id               int    `json:"id"`
	Name             string `json:"name"`
	RunnerScaleSetId int    `json:"runnerScaleSetId"`
}

type RunnerScaleSetJitRunnerConfig struct {
	Runner           *RunnerReference `json:"runner"`
	EncodedJITConfig string           `json:"encodedJITConfig"`
}
