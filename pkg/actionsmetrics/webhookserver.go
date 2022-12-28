package actionsmetrics

/*
Copyright 2022 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	gogithub "github.com/google/go-github/v47/github"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/actions/actions-runner-controller/github"
)

type EventHook func(interface{})

// WebhookServer is a HTTP server that handles workflow_job events sent from GitHub Actions
type WebhookServer struct {
	Log logr.Logger

	// SecretKeyBytes is the byte representation of the Webhook secret token
	// the administrator is generated and specified in GitHub Web UI.
	SecretKeyBytes []byte

	// GitHub Client to discover runner groups assigned to a repository
	GitHubClient *github.Client

	// When HorizontalRunnerAutoscalerGitHubWebhook handles a request, each EventHook is sent the webhook event
	EventHooks []EventHook
}

func (autoscaler *WebhookServer) Reconcile(_ context.Context, request reconcile.Request) (reconcile.Result, error) {
	return ctrl.Result{}, nil
}

func (autoscaler *WebhookServer) Handle(w http.ResponseWriter, r *http.Request) {
	var (
		ok bool

		err error
	)

	defer func() {
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)

			if err != nil {
				msg := err.Error()
				if written, err := w.Write([]byte(msg)); err != nil {
					autoscaler.Log.V(1).Error(err, "failed writing http error response", "msg", msg, "written", written)
				}
			}
		}
	}()

	defer func() {
		if r.Body != nil {
			r.Body.Close()
		}
	}()

	// respond ok to GET / e.g. for health check
	if r.Method == http.MethodGet {
		ok = true
		fmt.Fprintln(w, "actions-metrics-server is running")
		return
	}

	var payload []byte

	if len(autoscaler.SecretKeyBytes) > 0 {
		payload, err = gogithub.ValidatePayload(r, autoscaler.SecretKeyBytes)
		if err != nil {
			autoscaler.Log.Error(err, "error validating request body")

			return
		}
	} else {
		payload, err = io.ReadAll(r.Body)
		if err != nil {
			autoscaler.Log.Error(err, "error reading request body")

			return
		}
	}

	webhookType := gogithub.WebHookType(r)
	event, err := gogithub.ParseWebHook(webhookType, payload)
	if err != nil {
		var s string
		if payload != nil {
			s = string(payload)
		}

		autoscaler.Log.Error(err, "could not parse webhook", "webhookType", webhookType, "payload", s)

		return
	}

	log := autoscaler.Log.WithValues(
		"event", webhookType,
		"hookID", r.Header.Get("X-GitHub-Hook-ID"),
		"delivery", r.Header.Get("X-GitHub-Delivery"),
	)

	switch event.(type) {
	case *gogithub.PingEvent:
		ok = true

		w.WriteHeader(http.StatusOK)

		msg := "pong"

		if written, err := w.Write([]byte(msg)); err != nil {
			log.Error(err, "failed writing http response", "msg", msg, "written", written)
		}

		log.Info("handled ping event")

		return
	}

	for _, eventHook := range autoscaler.EventHooks {
		eventHook(event)
	}

	ok = true

	w.WriteHeader(http.StatusOK)

	msg := "ok"

	log.Info(msg)

	if written, err := w.Write([]byte(msg)); err != nil {
		log.Error(err, "failed writing http response", "msg", msg, "written", written)
	}
}
