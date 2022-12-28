package fake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"

	"github.com/google/go-github/v47/github"
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

	router.Handle("/repos/{owner}/{repo}/actions/runners", r.HandleList())
	router.Handle("/repos/{owner}/{repo}/actions/runners/{id}", r.handleRemove())
	router.Handle("/orgs/{org}/actions/runners", r.HandleList())
	router.Handle("/orgs/{org}/actions/runners/{id}", r.handleRemove())

	return httptest.NewServer(router)
}

func (r *RunnersList) HandleList() http.HandlerFunc {
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

func (r *RunnersList) Sync(runners []v1alpha1.Runner) {
	r.runners = nil

	for i, want := range runners {
		r.Add(&github.Runner{
			ID:     github.Int64(int64(i)),
			Name:   github.String(want.Name),
			OS:     github.String("linux"),
			Status: github.String("online"),
			Busy:   github.Bool(false),
		})
	}
}

func (r *RunnersList) AddOffline(runners []v1alpha1.Runner) {
	for i, want := range runners {
		r.Add(&github.Runner{
			ID:     github.Int64(int64(1000 + i)),
			Name:   github.String(want.Name),
			OS:     github.String("linux"),
			Status: github.String("offline"),
			Busy:   github.Bool(false),
		})
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
