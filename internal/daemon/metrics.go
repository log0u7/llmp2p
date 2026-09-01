package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// writeMetrics renders a minimal Prometheus text exposition for the
// daemon's gauges and counters. Hand-rolled instead of client_golang: the
// metric set is small and static, and the package adds no runtime value
// beyond formatting.
func (s *Server) writeMetrics(w http.ResponseWriter, r *http.Request) {
	var uploaded, downloaded, peers, torrents int64
	for _, e := range s.engines {
		for _, st := range e.TorrentStatuses() {
			torrents++
			peers += int64(st.Peers)
			uploaded += st.Uploaded
			downloaded += st.Downloaded
		}
	}
	models := s.countModels()

	var b strings.Builder
	writeGauge := func(name, help string, v int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
	}
	writeCounter := func(name, help string, v int64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
	}
	writeGauge("llmp2pd_uptime_seconds", "Seconds since the daemon started.", int64(time.Since(s.startedAt).Seconds()))
	writeGauge("llmp2pd_models", "Models present in the store.", int64(models))
	writeGauge("llmp2pd_torrents", "Torrents tracked by the daemon's engines.", torrents)
	writeGauge("llmp2pd_peers", "Connected peers across all torrents.", peers)
	writeGauge("llmp2pd_seeding_engines", "Running seeder engines (one per owner directory).", int64(len(s.engines)))
	writeCounter("llmp2pd_uploaded_bytes_total", "Bytes uploaded to peers since daemon start.", uploaded)
	writeCounter("llmp2pd_downloaded_bytes_total", "Bytes downloaded from peers since daemon start.", downloaded)
	writeCounter("llmp2pd_pulls_total", "Pull jobs executed by the daemon.", int64(len(s.pullStats)))
	for result, n := range s.pullStats {
		fmt.Fprintf(&b, "llmp2pd_pulls_total{result=%q} %d\n", result, n)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = fmt.Fprint(w, b.String())
}

// recordPullResult counts pull jobs by outcome; guarded by s.mu, updated
// on job completion.
func (s *Server) recordPullResult(result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pullStats == nil {
		s.pullStats = map[string]int{}
	}
	s.pullStats[result]++
}
