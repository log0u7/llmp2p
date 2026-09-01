package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/log0u7/llmp2p/internal/pull"
	"github.com/log0u7/llmp2p/internal/ref"
)

// newJobID returns a random 16-byte hex id.
func newJobID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("daemon: entropy unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// pullJob tracks one delegated pull.
type pullJob struct {
	ID         string      `json:"id"`
	Ref        string      `json:"ref"`
	Status     string      `json:"status"` // queued, running, succeeded, failed
	Error      string      `json:"error,omitempty"`
	Result     pull.Result `json:"result,omitempty"`
	QueuedAt   time.Time   `json:"queuedAt"`
	StartedAt  time.Time   `json:"startedAt,omitempty"`
	FinishedAt time.Time   `json:"finishedAt,omitempty"`
}

// pullQueue executes delegated pulls sequentially: each job runs pull.Run
// inside the daemon, which already owns the store lock (PullTemplate sets
// NoLock). The order is FIFO; at most one job runs at a time.
type pullQueue struct {
	mu       sync.Mutex
	jobs     map[string]*pullJob
	order    []string
	sem      chan struct{}
	srv      *Server
	template pull.Options
	log      *slog.Logger
}

func newPullQueue(srv *Server, template pull.Options, log *slog.Logger) *pullQueue {
	return &pullQueue{
		jobs:     map[string]*pullJob{},
		sem:      make(chan struct{}, 1),
		srv:      srv,
		template: template,
		log:      log,
	}
}

// enqueue registers a pull job and starts it in the background.
func (q *pullQueue) enqueue(ctx context.Context, r *ref.Ref, httpOnly bool) (*pullJob, error) {
	job := &pullJob{
		ID:       newJobID(),
		Ref:      r.String(),
		Status:   "queued",
		QueuedAt: time.Now().UTC(),
	}
	q.mu.Lock()
	q.jobs[job.ID] = job
	q.order = append(q.order, job.ID)
	q.mu.Unlock()

	go q.run(ctx, job, r, httpOnly)
	return job, nil
}

func (q *pullQueue) run(ctx context.Context, job *pullJob, r *ref.Ref, httpOnly bool) {
	q.sem <- struct{}{}
	defer func() { <-q.sem }()

	q.mu.Lock()
	job.Status = "running"
	job.StartedAt = time.Now().UTC()
	template := q.template
	q.mu.Unlock()

	// The HTTP request ends with the 202 response; the job must keep
	// running regardless of the client disconnecting.
	opts := template
	opts.HTTPOnly = httpOnly
	opts.NoLock = true
	res, err := pull.Run(context.WithoutCancel(ctx), r, opts)

	q.mu.Lock()
	defer q.mu.Unlock()
	job.FinishedAt = time.Now().UTC()
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
	} else {
		job.Status = "succeeded"
		job.Result = res
	}
	q.srv.recordPullResult(job.Status)
	if q.log != nil {
		logf(q.log, "pull job finished", "id", job.ID, "ref", job.Ref, "status", job.Status)
	}
}

// snapshot returns a copy of the job state.
func (q *pullQueue) snapshot(id string) (pullJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[id]
	if !ok {
		return pullJob{}, false
	}
	return *job, true
}

func (q *pullQueue) list() []pullJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]pullJob, 0, len(q.order))
	for _, id := range q.order {
		out = append(out, *q.jobs[id])
	}
	return out
}

func (s *Server) handlePullCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ref      string `json:"ref"`
		HTTPOnly bool   `json:"httpOnly"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid body")
		return
	}
	refObj, err := ref.Parse(body.Ref)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if refObj.Path != "" {
		httpError(w, http.StatusBadRequest, "delegated pulls operate on whole models")
		return
	}
	job, err := s.pulls.enqueue(r.Context(), refObj, body.HTTPOnly)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusAccepted, job)
}

func (s *Server) handlePullGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := s.pulls.snapshot(id)
	if !ok {
		httpError(w, http.StatusNotFound, "unknown pull job")
		return
	}
	writeJSON(w, job)
}

func (s *Server) handlePullList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.pulls.list())
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, struct {
		Error string `json:"error"`
	}{msg})
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
