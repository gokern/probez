package probez

import (
	"encoding/json"
	"net/http"
	"time"
)

func (p *Probe) handleStartupz(w http.ResponseWriter, _ *http.Request) {
	if p.currentState() >= stateStarted {
		w.WriteHeader(http.StatusOK)

		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
}

func (p *Probe) handleLivez(w http.ResponseWriter, _ *http.Request) {
	if p.isLive() {
		w.WriteHeader(http.StatusOK)

		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
}

func (p *Probe) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if p.isReady(r.Context()) {
		w.WriteHeader(http.StatusOK)

		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
}

func (p *Probe) handleHealthz(w http.ResponseWriter, r *http.Request) {
	p.handleReadyz(w, r)
}

func (p *Probe) handleStatus(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()

	var heartbeatAgeMs int64
	if last := p.lastPing.Load(); last != 0 {
		heartbeatAgeMs = now.Sub(time.Unix(0, last)).Milliseconds()
	} else {
		heartbeatAgeMs = -1 // no ping yet
	}

	resp := struct {
		State          string `json:"state"`
		UptimeMs       int64  `json:"uptime_ms"`
		HeartbeatAgeMs int64  `json:"heartbeat_age_ms"`
		StaleAfterMs   int64  `json:"stale_after_ms"`
	}{
		State:          p.currentState().String(),
		UptimeMs:       now.Sub(p.createdAt).Milliseconds(),
		HeartbeatAgeMs: heartbeatAgeMs,
		StaleAfterMs:   p.staleAfter.Milliseconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err := json.NewEncoder(w).Encode(resp)
	if err != nil {
		p.logger.Error("probez: failed to encode status", "err", err)
	}
}

// newMux creates the HTTP mux with all probe endpoints.
func (p *Probe) newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /startupz", p.handleStartupz)
	mux.HandleFunc("GET /livez", p.handleLivez)
	mux.HandleFunc("GET /readyz", p.handleReadyz)
	mux.HandleFunc("GET /healthz", p.handleHealthz)
	mux.HandleFunc("GET /status", p.handleStatus)

	return mux
}
