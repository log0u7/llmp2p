package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/log0u7/llmp2p/internal/pull"
)

// DefaultDaemonURL is the local daemon address the CLI delegates to.
const DefaultDaemonURL = "http://127.0.0.1:8347"

var daemonHTTP = &http.Client{Timeout: 10 * time.Second}

// daemonUp reports whether a daemon answers on url.
func daemonUp(url string) bool {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get(url + "/api/v1/status")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// delegatePull posts the pull to the daemon and polls the job until it
// finishes, printing a status line on stderr. It returns the final job.
func delegatePull(url, ref string, httpOnly bool) (jobResult, error) {
	body, _ := json.Marshal(struct {
		Ref      string `json:"ref"`
		HTTPOnly bool   `json:"httpOnly"`
	}{ref, httpOnly})
	res, err := daemonHTTP.Post(url+"/api/v1/pulls", "application/json", bytes.NewReader(body))
	if err != nil {
		return jobResult{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var job jobResult
	if err := json.NewDecoder(res.Body).Decode(&job); err != nil || res.StatusCode != http.StatusAccepted {
		if res.StatusCode != http.StatusAccepted {
			return jobResult{}, fmt.Errorf("daemon rejected pull: status %d", res.StatusCode)
		}
		return jobResult{}, err
	}

	start := time.Now()
	for {
		time.Sleep(time.Second)
		res, err := daemonHTTP.Get(url + "/api/v1/pulls/" + job.ID)
		if err != nil {
			return jobResult{}, err
		}
		err = json.NewDecoder(res.Body).Decode(&job)
		_ = res.Body.Close()
		if err != nil {
			return jobResult{}, err
		}
		switch job.Status {
		case "succeeded", "failed":
			fmt.Fprintln(os.Stderr)
			return job, nil
		default:
			fmt.Fprintf(os.Stderr, "\r\033[Kdaemon pull %s (%.0fs elapsed)", job.Status, time.Since(start).Seconds())
		}
	}
}

// jobResult mirrors the daemon pullJob JSON (subset the CLI needs).
type jobResult struct {
	ID     string      `json:"id"`
	Ref    string      `json:"ref"`
	Status string      `json:"status"`
	Error  string      `json:"error,omitempty"`
	Result pull.Result `json:"result,omitempty"`
}
