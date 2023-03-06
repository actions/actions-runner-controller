package fake

import (
	"context"
	"time"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/google/uuid"
)

type Option func(*FakeClient)

func WithGetRunnerScaleSetResult(scaleSet *actions.RunnerScaleSet, err error) Option {
	return func(f *FakeClient) {
		f.getRunnerScaleSetResult.RunnerScaleSet = scaleSet
		f.getRunnerScaleSetResult.err = err
	}
}

func WithGetRunnerGroup(runnerGroup *actions.RunnerGroup, err error) Option {
	return func(f *FakeClient) {
		f.getRunnerGroupByNameResult.RunnerGroup = runnerGroup
		f.getRunnerGroupByNameResult.err = err
	}
}

func WithGetRunner(runner *actions.RunnerReference, err error) Option {
	return func(f *FakeClient) {
		f.getRunnerResult.RunnerReference = runner
		f.getRunnerResult.err = err
	}
}

func WithCreateRunnerScaleSet(scaleSet *actions.RunnerScaleSet, err error) Option {
	return func(f *FakeClient) {
		f.createRunnerScaleSetResult.RunnerScaleSet = scaleSet
		f.createRunnerScaleSetResult.err = err
	}
}

func WithUpdateRunnerScaleSet(scaleSet *actions.RunnerScaleSet, err error) Option {
	return func(f *FakeClient) {
		f.updateRunnerScaleSetResult.RunnerScaleSet = scaleSet
		f.updateRunnerScaleSetResult.err = err
	}
}

var defaultRunnerScaleSet = &actions.RunnerScaleSet{
	Id:                 1,
	Name:               "testset",
	RunnerGroupId:      1,
	RunnerGroupName:    "testgroup",
	Labels:             []actions.Label{{Type: "test", Name: "test"}},
	RunnerSetting:      actions.RunnerSetting{},
	CreatedOn:          time.Now(),
	RunnerJitConfigUrl: "test.test.test",
	Statistics:         nil,
}

var defaultUpdatedRunnerScaleSet = &actions.RunnerScaleSet{
	Id:                 1,
	Name:               "testset",
	RunnerGroupId:      2,
	RunnerGroupName:    "testgroup2",
	Labels:             []actions.Label{{Type: "test", Name: "test"}},
	RunnerSetting:      actions.RunnerSetting{},
	CreatedOn:          time.Now(),
	RunnerJitConfigUrl: "test.test.test",
	Statistics:         nil,
}

var defaultRunnerGroup = &actions.RunnerGroup{
	ID:        1,
	Name:      "testgroup",
	Size:      1,
	IsDefault: true,
}

var sessionID = uuid.New()

var defaultRunnerScaleSetSession = &actions.RunnerScaleSetSession{
	SessionId:               &sessionID,
	OwnerName:               "testowner",
	RunnerScaleSet:          defaultRunnerScaleSet,
	MessageQueueUrl:         "https://test.url/path",
	MessageQueueAccessToken: "faketoken",
	Statistics:              nil,
}

var defaultAcquirableJob = &actions.AcquirableJob{
	AcquireJobUrl:   "https://test.url",
	MessageType:     "",
	RunnerRequestId: 1,
	RepositoryName:  "testrepo",
	OwnerName:       "testowner",
	JobWorkflowRef:  "workflowref",
	EventName:       "testevent",
	RequestLabels:   []string{"test"},
}

var defaultAcquirableJobList = &actions.AcquirableJobList{
	Count: 1,
	Jobs:  []actions.AcquirableJob{*defaultAcquirableJob},
}

var defaultRunnerReference = &actions.RunnerReference{
	Id:               1,
	Name:             "testrunner",
	RunnerScaleSetId: 1,
}

var defaultRunnerScaleSetMessage = &actions.RunnerScaleSetMessage{
	MessageId:   1,
	MessageType: "test",
	Body:        "{}",
	Statistics:  nil,
}

var defaultRunnerScaleSetJitRunnerConfig = &actions.RunnerScaleSetJitRunnerConfig{
	Runner:           defaultRunnerReference,
	EncodedJITConfig: "test",
}

// FakeClient implements actions service
type FakeClient struct {
	getRunnerScaleSetResult struct {
		*actions.RunnerScaleSet
		err error
	}
	getRunnerScaleSetByIdResult struct {
		*actions.RunnerScaleSet
		err error
	}
	getRunnerGroupByNameResult struct {
		*actions.RunnerGroup
		err error
	}

	createRunnerScaleSetResult struct {
		*actions.RunnerScaleSet
		err error
	}
	updateRunnerScaleSetResult struct {
		*actions.RunnerScaleSet
		err error
	}
	deleteRunnerScaleSetResult struct {
		err error
	}
	createMessageSessionResult struct {
		*actions.RunnerScaleSetSession
		err error
	}
	deleteMessageSessionResult struct {
		err error
	}
	refreshMessageSessionResult struct {
		*actions.RunnerScaleSetSession
		err error
	}
	acquireJobsResult struct {
		ids []int64
		err error
	}
	getAcquirableJobsResult struct {
		*actions.AcquirableJobList
		err error
	}
	getMessageResult struct {
		*actions.RunnerScaleSetMessage
		err error
	}
	deleteMessageResult struct {
		err error
	}
	generateJitRunnerConfigResult struct {
		*actions.RunnerScaleSetJitRunnerConfig
		err error
	}
	getRunnerResult struct {
		*actions.RunnerReference
		err error
	}
	getRunnerByNameResult struct {
		*actions.RunnerReference
		err error
	}
	removeRunnerResult struct {
		err error
	}
}

func NewFakeClient(options ...Option) actions.ActionsService {
	f := &FakeClient{}
	f.applyDefaults()
	for _, opt := range options {
		opt(f)
	}
	return f
}

func (f *FakeClient) applyDefaults() {
	f.getRunnerScaleSetResult.RunnerScaleSet = defaultRunnerScaleSet
	f.getRunnerScaleSetByIdResult.RunnerScaleSet = defaultRunnerScaleSet
	f.getRunnerGroupByNameResult.RunnerGroup = defaultRunnerGroup
	f.createRunnerScaleSetResult.RunnerScaleSet = defaultRunnerScaleSet
	f.updateRunnerScaleSetResult.RunnerScaleSet = defaultUpdatedRunnerScaleSet
	f.createMessageSessionResult.RunnerScaleSetSession = defaultRunnerScaleSetSession
	f.refreshMessageSessionResult.RunnerScaleSetSession = defaultRunnerScaleSetSession
	f.acquireJobsResult.ids = []int64{1}
	f.getAcquirableJobsResult.AcquirableJobList = defaultAcquirableJobList
	f.getMessageResult.RunnerScaleSetMessage = defaultRunnerScaleSetMessage
	f.generateJitRunnerConfigResult.RunnerScaleSetJitRunnerConfig = defaultRunnerScaleSetJitRunnerConfig
	f.getRunnerResult.RunnerReference = defaultRunnerReference
	f.getRunnerByNameResult.RunnerReference = defaultRunnerReference
}

func (f *FakeClient) GetRunnerScaleSet(ctx context.Context, runnerScaleSetName string) (*actions.RunnerScaleSet, error) {
	return f.getRunnerScaleSetResult.RunnerScaleSet, f.getRunnerScaleSetResult.err
}

func (f *FakeClient) GetRunnerScaleSetById(ctx context.Context, runnerScaleSetId int) (*actions.RunnerScaleSet, error) {
	return f.getRunnerScaleSetByIdResult.RunnerScaleSet, f.getRunnerScaleSetResult.err
}

func (f *FakeClient) GetRunnerGroupByName(ctx context.Context, runnerGroup string) (*actions.RunnerGroup, error) {
	return f.getRunnerGroupByNameResult.RunnerGroup, f.getRunnerGroupByNameResult.err
}

func (f *FakeClient) CreateRunnerScaleSet(ctx context.Context, runnerScaleSet *actions.RunnerScaleSet) (*actions.RunnerScaleSet, error) {
	return f.createRunnerScaleSetResult.RunnerScaleSet, f.createRunnerScaleSetResult.err
}

func (f *FakeClient) UpdateRunnerScaleSet(ctx context.Context, runnerScaleSetId int, runnerScaleSet *actions.RunnerScaleSet) (*actions.RunnerScaleSet, error) {
	return f.updateRunnerScaleSetResult.RunnerScaleSet, f.updateRunnerScaleSetResult.err
}

func (f *FakeClient) DeleteRunnerScaleSet(ctx context.Context, runnerScaleSetId int) error {
	return f.deleteRunnerScaleSetResult.err
}

func (f *FakeClient) CreateMessageSession(ctx context.Context, runnerScaleSetId int, owner string) (*actions.RunnerScaleSetSession, error) {
	return f.createMessageSessionResult.RunnerScaleSetSession, f.createMessageSessionResult.err
}

func (f *FakeClient) DeleteMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) error {
	return f.deleteMessageSessionResult.err
}

func (f *FakeClient) RefreshMessageSession(ctx context.Context, runnerScaleSetId int, sessionId *uuid.UUID) (*actions.RunnerScaleSetSession, error) {
	return f.refreshMessageSessionResult.RunnerScaleSetSession, f.refreshMessageSessionResult.err
}

func (f *FakeClient) AcquireJobs(ctx context.Context, runnerScaleSetId int, messageQueueAccessToken string, requestIds []int64) ([]int64, error) {
	return f.acquireJobsResult.ids, f.acquireJobsResult.err
}

func (f *FakeClient) GetAcquirableJobs(ctx context.Context, runnerScaleSetId int) (*actions.AcquirableJobList, error) {
	return f.getAcquirableJobsResult.AcquirableJobList, f.getAcquirableJobsResult.err
}

func (f *FakeClient) GetMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, lastMessageId int64) (*actions.RunnerScaleSetMessage, error) {
	return f.getMessageResult.RunnerScaleSetMessage, f.getMessageResult.err
}

func (f *FakeClient) DeleteMessage(ctx context.Context, messageQueueUrl, messageQueueAccessToken string, messageId int64) error {
	return f.deleteMessageResult.err
}

func (f *FakeClient) GenerateJitRunnerConfig(ctx context.Context, jitRunnerSetting *actions.RunnerScaleSetJitRunnerSetting, scaleSetId int) (*actions.RunnerScaleSetJitRunnerConfig, error) {
	return f.generateJitRunnerConfigResult.RunnerScaleSetJitRunnerConfig, f.generateJitRunnerConfigResult.err
}

func (f *FakeClient) GetRunner(ctx context.Context, runnerId int64) (*actions.RunnerReference, error) {
	return f.getRunnerResult.RunnerReference, f.getRunnerResult.err
}

func (f *FakeClient) GetRunnerByName(ctx context.Context, runnerName string) (*actions.RunnerReference, error) {
	return f.getRunnerByNameResult.RunnerReference, f.getRunnerByNameResult.err
}

func (f *FakeClient) RemoveRunner(ctx context.Context, runnerId int64) error {
	return f.removeRunnerResult.err
}
