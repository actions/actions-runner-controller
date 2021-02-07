package fake

import "net/http"

type FixedResponses struct {
	ListRepositoryWorkflowRuns *Handler
	ListWorkflowJobs           *MapHandler
	ListRunners                http.Handler
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

func WithListWorkflowJobsResponse(status int, bodies map[int]string) Option {
	return func(c *ServerConfig) {
		c.FixedResponses.ListWorkflowJobs = &MapHandler{
			Status: status,
			Bodies: bodies,
		}
	}
}

func WithListRunnersResponse(status int, body string) Option {
	return func(c *ServerConfig) {
		c.FixedResponses.ListRunners = &ListRunnersHandler{
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
