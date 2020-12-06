package fake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"

	"github.com/google/go-github/v33/github"
	"github.com/gorilla/mux"
)

type RunnersList struct {
	runners []*github.Runner
}

func NewRunnersList() *RunnersList {
	return &RunnersList{
		runners: make([]*github.Runner, 0),
	}
}

func (r *RunnersList) Add(runner *github.Runner) {
	if !exists(r.runners, runner) {
		r.runners = append(r.runners, runner)
	}
}

func (r *RunnersList) GetServer() *httptest.Server {
	router := mux.NewRouter()

	router.Handle("/repos/{owner}/{repo}/actions/runners", r.handleList())
	router.Handle("/repos/{owner}/{repo}/actions/runners/{id}", r.handleRemove())
	router.Handle("/orgs/{org}/actions/runners", r.handleList())
	router.Handle("/orgs/{org}/actions/runners/{id}", r.handleRemove())

	return httptest.NewServer(router)
}

func (r *RunnersList) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, res *http.Request) {
		j, err := json.Marshal(github.Runners{
			TotalCount: len(r.runners),
			Runners:    r.runners,
		})
		if err != nil {
			panic(err)
		}

		w.WriteHeader(http.StatusOK)
		w.Write(j)
	}
}

func (r *RunnersList) handleRemove() http.HandlerFunc {
	return func(w http.ResponseWriter, res *http.Request) {
		vars := mux.Vars(res)
		for i, runner := range r.runners {
			if runner.ID != nil && vars["id"] == strconv.FormatInt(*runner.ID, 10) {
				r.runners = append(r.runners[:i], r.runners[i+1:]...)
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

func exists(runners []*github.Runner, runner *github.Runner) bool {
	for _, r := range runners {
		if *r.Name == *runner.Name {
			return true
		}
	}
	return false
}
