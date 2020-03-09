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
[
  {"id": 1, "name": "test1", "os": "linux", "status": "online"},
  {"id": 2, "name": "test2", "os": "linux", "status": "offline"}
]
`
)

func NewServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/test/valid/actions/runners/registration-token", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
		expiresAt := time.Now().Add(time.Hour * 1)
		fmt.Fprintf(w, fmt.Sprintf("{\"token\": \"%s\", \"expires_at\": \"%s\"}", RegistrationToken, expiresAt.Format(time.RFC3339)))
	})
	mux.HandleFunc("/repos/test/invalid/actions/runners/registration-token", func(w http.ResponseWriter, req *http.Request) {
		expiresAt := time.Now().Add(time.Hour * 1)
		fmt.Fprintf(w, fmt.Sprintf("{\"token\": \"%s\", \"expires_at\": \"%s\"}", RegistrationToken, expiresAt.Format(time.RFC3339)))
	})
	mux.HandleFunc("/repos/test/error/actions/runners/registration-token", func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "", http.StatusBadRequest)
	})

	mux.HandleFunc("/repos/test/valid/actions/runners", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, RunnersListBody)
	})
	mux.HandleFunc("/repos/test/invalid/actions/runners", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/repos/test/error/actions/runners", func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "", http.StatusBadRequest)
	})

	mux.HandleFunc("/repos/test/valid/actions/runners/1", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/repos/test/invalid/actions/runners/1", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/repos/test/error/actions/runners/1", func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "", http.StatusBadRequest)
	})

	return httptest.NewServer(mux)
}
