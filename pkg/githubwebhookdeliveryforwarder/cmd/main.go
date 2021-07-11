package main

import (
	"flag"

	"github.com/actions-runner-controller/actions-runner-controller/pkg/githubwebhookdeliveryforwarder"
)

func main() {
	config := &githubwebhookdeliveryforwarder.Config{}

	config.InitFlags((flag.CommandLine))

	flag.Parse()

	githubwebhookdeliveryforwarder.Run(config)
}
