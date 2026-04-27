package capacity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// queuedThresholdMinutes=0: include jobs queued for any duration
	// maxAgeDays=3: look at the last 3 days of data
	// orgs=["pytorch"]: scope to the pytorch GitHub org
	// repo="": all repos in the org
	defaultHUDAPIURL = "https://hud.pytorch.org/api/clickhouse/queued_jobs_aggregate" +
		"?parameters=%7B%22queuedThresholdMinutes%22%3A0%2C%22maxAgeDays%22%3A3%2C%22orgs%22%3A%5B%22pytorch%22%5D%2C%22repo%22%3A%22%22%7D"
	// hudResponseMaxBytes caps the JSON payload we will read from the
	// HUD API. A misbehaving or compromised endpoint must not be able
	// to OOM the listener by streaming an unbounded response.
	hudResponseMaxBytes = 10 * 1024 * 1024 // 10 MiB
)

// QueuedJobsForRunner represents a single row from the HUD API response.
type QueuedJobsForRunner struct {
	RunnerLabel         string  `json:"runner_label"`
	Org                 string  `json:"org"`
	Repo                string  `json:"repo"`
	NumQueuedJobs       int     `json:"num_queued_jobs"`
	MinQueueTimeMinutes float64 `json:"min_queue_time_minutes"`
	MaxQueueTimeMinutes float64 `json:"max_queue_time_minutes"`
}

// HUDClient is an HTTP client for the PyTorch HUD API that returns
// aggregate queued job counts per runner label.
type HUDClient struct {
	url    string
	token  string
	client *http.Client
}

// NewHUDClient creates a new HUD API client with the given auth token.
func NewHUDClient(url, token string) *HUDClient {
	return &HUDClient{
		url:    url,
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetQueuedJobsForLabels queries the HUD API and returns the total
// number of queued jobs matching any of the provided runner labels.
// On any error the caller receives (0, err) and decides the fallback.
func (c *HUDClient) GetQueuedJobsForLabels(ctx context.Context, labels []string) (int, error) {
	if len(labels) == 0 {
		return 0, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return 0, fmt.Errorf("building HUD request: %w", err)
	}
	req.Header.Set("x-hud-internal-bot", c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("HUD API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HUD API returned status %d", resp.StatusCode)
	}

	var rows []QueuedJobsForRunner
	body := io.LimitReader(resp.Body, hudResponseMaxBytes)
	if err := json.NewDecoder(body).Decode(&rows); err != nil {
		return 0, fmt.Errorf("decoding HUD response: %w", err)
	}

	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}

	total := 0
	for _, row := range rows {
		if _, ok := labelSet[row.RunnerLabel]; ok {
			total += row.NumQueuedJobs
		}
	}
	return total, nil
}
