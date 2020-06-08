package fake

type FixedResponses struct {
	listRepositoryWorkflowRuns FixedResponse
}

type FixedResponse struct {
	Status int
	Body   string
}

func (r FixedResponse) handler() handler {
	return handler{
		Status: r.Status,
		Body:   r.Body,
	}
}

type Option func(responses *FixedResponses)

func WithListRepositoryWorkflowRunsResponse(status int, body string) Option {
	return func(r *FixedResponses) {
		r.listRepositoryWorkflowRuns = FixedResponse{
			Status: status,
			Body:   body,
		}
	}
}
