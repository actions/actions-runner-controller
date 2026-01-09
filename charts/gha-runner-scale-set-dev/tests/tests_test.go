package tests

import (
	"path/filepath"
	"strings"

	"github.com/gruntwork-io/terratest/modules/random"
)

var chartPath string

func init() {
	var err error
	chartPath, err = filepath.Abs("../../gha-runner-scale-set-dev")
	if err != nil {
		panic(err)
	}
}

// generateNamespace generates namespace with given prefix
// If prefix is not specified, a default prefix "test-" is used
func generateNamespace(prefix string) string {
	if prefix == "" {
		prefix = "test-"
	}
	return prefix + strings.ToLower(random.UniqueId())
}
