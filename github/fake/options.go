package fake

type FixedResponses struct {
	ListRepositoryWorkflowRuns *Handler
}

type Option func(*ServerConfig)

func WithListRepositoryWorkflowRunsResponse(status int, body string) Option {
	return func(c *ServerConfig) {
		c.FixedResponses.ListRepositoryWorkflowRuns = &Handler{
			Status: status,
			Body:   body,
		}
	}
}

func WithFixedResponses(responses *FixedResponses) Option {
	return func(c *ServerConfig) {
		c.FixedResponses = responses
	}
}
