package main

import (
	"flag"

	"github.com/actions-runner-controller/actions-runner-controller/pkg/githubwebhookdeliveryforwarder"
)

func main() {
	config := &githubwebhookdeliveryforwarder.Config{
		// TODO: Set to something that is backed by a CRD so that
		// restarting the forwarder doesn't result in missing deliveries.
		LogPositionProvider: nil,
	}

	config.InitFlags((flag.CommandLine))

	flag.Parse()

	githubwebhookdeliveryforwarder.Run(config)
}
