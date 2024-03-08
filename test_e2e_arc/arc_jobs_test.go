package e2e_arc

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type podCountsByType struct {
	controllers int
	listeners   int
	runners     int
}

func getPodsByType(clientset *kubernetes.Clientset) podCountsByType {
	arc_namespace := "arc-system"
	availableArcPods, err := clientset.CoreV1().Pods(arc_namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	runners_namespace := "arc-runners"
	availableRunnerPods, err := clientset.CoreV1().Pods(runners_namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	podsByType := podCountsByType{}
	for _, pod := range availableArcPods.Items {
		if strings.Contains(pod.Name, "controller") {
			podsByType.controllers += 1
		}
		if strings.Contains(pod.Name, "listener") {
			podsByType.listeners += 1
		}
	}
	for _, pod := range availableRunnerPods.Items {
		if strings.Contains(pod.Name, "runner") {
			podsByType.runners += 1
		}
	}
	return podsByType
}

func pollForClusterState(clientset *kubernetes.Clientset, expectedPodsCount podCountsByType, maxTime int) bool {
	sleepTime := 5
	maxRetries := maxTime / sleepTime
	success := false
	for i := 0; i <= maxRetries; i++ {
		time.Sleep(time.Second * time.Duration(sleepTime))
		availablePodsCount := getPodsByType(clientset)
		if availablePodsCount == expectedPodsCount {
			success = true
			break
		} else {
			fmt.Printf("%v", availablePodsCount)
		}
	}
	return success
}

func TestARCJobs(t *testing.T) {
	configFile := filepath.Join(
		os.Getenv("HOME"), ".kube", "config",
	)

	config, err := clientcmd.BuildConfigFromFlags("", configFile)
	if err != nil {
		t.Fatal(err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Get available pods before job run", func(t *testing.T) {
		expectedPodsCount := podCountsByType{1, 1, 0}
		success := pollForClusterState(clientset, expectedPodsCount, 60)
		if !success {
			t.Fatal("Expected pods count did not match available pods count before job run.")
		}
	},
	)
	t.Run("Get available pods during job run", func(t *testing.T) {
		c := http.Client{}
		targetArcName := os.Getenv("ARC_NAME")
		require.NotEmpty(t, targetArcName, "ARC_NAME environment variable is required for this test to run. (e.g. arc-e2e-test)")

		targetWorkflow := os.Getenv("WORKFLOW_FILE")
		require.NotEmpty(t, targetWorkflow, "WORKFLOW_FILE environment variable is required for this test to run. (e.g. e2e_test.yml)")

		ght := os.Getenv("GITHUB_TOKEN")
		require.NotEmpty(t, ght, "GITHUB_TOKEN environment variable is required for this test to run.")

		// We are triggering manually a workflow that already exists in the repo.
		// This workflow is expected to spin up a number of runner pods matching the runners value set in podCountsByType.
		url := "https://api.github.com/repos/actions-runner-controller/arc_e2e_test_dummy/actions/workflows/" + targetWorkflow + "/dispatches"
		jsonStr := []byte(fmt.Sprintf(`{"ref":"main", "inputs":{"arc_name":"%s"}}`, targetArcName))

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Add("Accept", "application/vnd.github+json")
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", ght))
		req.Header.Add("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		expectedPodsCount := podCountsByType{1, 1, 3}
		success := pollForClusterState(clientset, expectedPodsCount, 120)
		if !success {
			t.Fatal("Expected pods count did not match available pods count during job run.")
		}

	},
	)
	t.Run("Get available pods after job run", func(t *testing.T) {
		expectedPodsCount := podCountsByType{1, 1, 0}
		success := pollForClusterState(clientset, expectedPodsCount, 120)
		if !success {
			t.Fatal("Expected pods count did not match available pods count after job run.")
		}
	},
	)
}
