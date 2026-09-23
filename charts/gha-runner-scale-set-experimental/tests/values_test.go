package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValuesExamplesAreTopLevel(t *testing.T) {
	valuesPath, err := filepath.Abs("../values.yaml")
	if err != nil {
		t.Fatal(err)
	}

	values, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		marker  string
		example string
	}{
		{
			name:    "proxy",
			marker:  "## Proxy can be used to define proxy settings",
			example: "# proxy:",
		},
		{
			name:    "github server TLS",
			marker:  "## A self-signed CA certificate for communication with the GitHub server",
			example: "# githubServerTLS:",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			markerIndex := strings.Index(string(values), test.marker)
			if markerIndex == -1 {
				t.Fatalf("could not find example marker %q", test.marker)
			}

			exampleIndex := markerIndex + strings.Index(string(values[markerIndex:]), test.example)
			if exampleIndex < markerIndex {
				t.Fatalf("could not find example %q after marker %q", test.example, test.marker)
			}

			lineStart := strings.LastIndex(string(values[:exampleIndex]), "\n") + 1
			lineEnd := exampleIndex + strings.Index(string(values[exampleIndex:]), "\n")
			if got := string(values[lineStart:lineEnd]); got != test.example {
				t.Errorf("example must be top-level: got %q, want %q", got, test.example)
			}
		})
	}
}
