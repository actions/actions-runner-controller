module github.com/actions-runner-controller/actions-runner-controller

go 1.15

require (
	github.com/bradleyfalzon/ghinstallation v1.1.1
	github.com/bradleyfalzon/ghinstallation/v2 v2.0.3
	github.com/davecgh/go-spew v1.1.1
	github.com/go-logr/logr v0.4.0
	github.com/google/go-cmp v0.5.6
	github.com/google/go-github/v37 v37.0.0
	github.com/gorilla/mux v1.8.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/prometheus/client_golang v1.11.0
	github.com/teambition/rrule-go v1.6.2
	go.uber.org/zap v1.19.0
	golang.org/x/oauth2 v0.0.0-20210819190943-2bc19b11175f
	gomodules.xyz/jsonpatch/v2 v2.2.0
	k8s.io/api v0.21.1
	k8s.io/apimachinery v0.21.1
	k8s.io/client-go v0.21.1
	sigs.k8s.io/controller-runtime v0.9.0
	sigs.k8s.io/yaml v1.2.0
)

replace github.com/google/go-github/v37 => github.com/mumoshu/go-github/v37 v37.0.100
