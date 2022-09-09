package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var configMapNames []string

	output, err := output()
	if err != nil {
		log.Printf("Command failed with output: %s", string(output))
		return err
	}

	s := bufio.NewScanner(bytes.NewBuffer(output))

	for s.Scan() {
		if t := s.Text(); strings.Contains(t, "test-info") || strings.Contains(t, "test-result-") {
			configMapNames = append(configMapNames, s.Text())
		}
	}

	for _, n := range configMapNames {
		println(n)

		if output, err := delete(n); err != nil {
			log.Printf("Command failed with output: %s", string(output))
			return err
		}
	}

	return nil
}

func output() ([]byte, error) {
	cmd := exec.Command("kubectl", "get", "cm", "-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	data, err := cmd.CombinedOutput()
	return data, err
}

func delete(cmName string) ([]byte, error) {
	cmd := exec.Command("kubectl", "delete", "cm", cmName)
	return cmd.CombinedOutput()
}
