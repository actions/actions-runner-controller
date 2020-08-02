package fake

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"
)

const (
	RegistrationToken = "fake-registration-token"

	RunnersListBody = `
{
  "total_count": 2,
  "runners": [
    {"id": 1, "name": "test1", "os": "linux", "status": "online"},
    {"id": 2, "name": "test2", "os": "linux", "status": "offline"}
  ]
}
`
)

type Handler struct {
	Status int
	Body   string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(h.Status)
	fmt.Fprintf(w, h.Body)
}

type ServerConfig struct {
	*FixedResponses
}

// NewServer creates a fake server for running unit tests
func NewServer(opts ...Option) *httptest.Server {
	config := ServerConfig{
		FixedResponses: &FixedResponses{},
	}

	for _, o := range opts {
		o(&config)
	}

	routes := map[string]*Handler{
		// For CreateRegistrationToken
		"/repos/test/valid/actions/runners/registration-token": &Handler{
			Status: http.StatusCreated,
			Body:   fmt.Sprintf("{\"token\": \"%s\", \"expires_at\": \"%s\"}", RegistrationToken, time.Now().Add(time.Hour*1).Format(time.RFC3339)),
		},
		"/repos/test/invalid/actions/runners/registration-token": &Handler{
			Status: http.StatusOK,
			Body:   fmt.Sprintf("{\"token\": \"%s\", \"expires_at\": \"%s\"}", RegistrationToken, time.Now().Add(time.Hour*1).Format(time.RFC3339)),
		},
		"/repos/test/error/actions/runners/registration-token": &Handler{
			Status: http.StatusBadRequest,
			Body:   "",
		},
		"/orgs/test/actions/runners/registration-token": &Handler{
			Status: http.StatusCreated,
			Body:   fmt.Sprintf("{\"token\": \"%s\", \"expires_at\": \"%s\"}", RegistrationToken, time.Now().Add(time.Hour*1).Format(time.RFC3339)),
		},
		"/orgs/invalid/actions/runners/registration-token": &Handler{
			Status: http.StatusOK,
			Body:   fmt.Sprintf("{\"token\": \"%s\", \"expires_at\": \"%s\"}", RegistrationToken, time.Now().Add(time.Hour*1).Format(time.RFC3339)),
		},
		"/orgs/error/actions/runners/registration-token": &Handler{
			Status: http.StatusBadRequest,
			Body:   "",
		},

		// For ListRunners
		"/repos/test/valid/actions/runners": &Handler{
			Status: http.StatusOK,
			Body:   RunnersListBody,
		},
		"/repos/test/invalid/actions/runners": &Handler{
			Status: http.StatusNoContent,
			Body:   "",
		},
		"/repos/test/error/actions/runners": &Handler{
			Status: http.StatusBadRequest,
			Body:   "",
		},
		"/orgs/test/actions/runners": &Handler{
			Status: http.StatusOK,
			Body:   RunnersListBody,
		},
		"/orgs/invalid/actions/runners": &Handler{
			Status: http.StatusNoContent,
			Body:   "",
		},
		"/orgs/error/actions/runners": &Handler{
			Status: http.StatusBadRequest,
			Body:   "",
		},

		// For RemoveRunner
		"/repos/test/valid/actions/runners/1": &Handler{
			Status: http.StatusNoContent,
			Body:   "",
		},
		"/repos/test/invalid/actions/runners/1": &Handler{
			Status: http.StatusOK,
			Body:   "",
		},
		"/repos/test/error/actions/runners/1": &Handler{
			Status: http.StatusBadRequest,
			Body:   "",
		},
		"/orgs/test/actions/runners/1": &Handler{
			Status: http.StatusNoContent,
			Body:   "",
		},
		"/orgs/invalid/actions/runners/1": &Handler{
			Status: http.StatusOK,
			Body:   "",
		},
		"/orgs/error/actions/runners/1": &Handler{
			Status: http.StatusBadRequest,
			Body:   "",
		},

		// For auto-scaling based on the number of queued(pending) workflow runs
		"/repos/test/valid/actions/runs": config.FixedResponses.ListRepositoryWorkflowRuns,
	}

	mux := http.NewServeMux()
	for path, handler := range routes {
		mux.Handle(path, handler)
	}

	return httptest.NewServer(mux)
}
