// Package api provides the REST API handlers for the API Sentinel dashboard.
// All endpoints return JSON and are mounted at /api/v1/... in main.go.
//
// Endpoints:
//
//	GET /api/v1/stats/summary    — aggregate statistics
//	GET /api/v1/stats/timeseries — second-level time-series for charting
//	GET /api/v1/events           — recent raw request events
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/yourusername/api-sentinel/internal/analytics"
	"github.com/yourusername/api-sentinel/internal/logger"
)

// Handler holds dependencies for the API handlers.
type Handler struct {
	store *analytics.Store
	log   *logger.Logger
}

// New creates an API Handler backed by the provided analytics store.
func New(store *analytics.Store, log *logger.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// RegisterRoutes mounts all dashboard API routes onto mux.
// All routes are prefixed with /api/v1.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/stats/summary", h.withCORS(h.handleSummary))
	mux.HandleFunc("/api/v1/stats/timeseries", h.withCORS(h.handleTimeSeries))
	mux.HandleFunc("/api/v1/events", h.withCORS(h.handleEvents))
}

// handleSummary returns aggregate statistics over all stored events.
//
//	GET /api/v1/stats/summary
//	→ 200 {"total_requests":1000,"blocked_requests":12, ...}
func (h *Handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	summary := h.store.Summary()
	writeJSON(w, http.StatusOK, summary)
}

// handleTimeSeries returns per-second request counts for the last N seconds.
//
//	GET /api/v1/stats/timeseries?window=60
//	→ 200 [{"ts":"...","total":5,"blocked":0,"anomalous":0}, ...]
func (h *Handler) handleTimeSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	window := 60
	if s := r.URL.Query().Get("window"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || n > 3600 {
			writeError(w, http.StatusBadRequest, "invalid_window",
				"window must be an integer between 1 and 3600")
			return
		}
		window = n
	}
	series := h.store.TimeSeries(window)
	writeJSON(w, http.StatusOK, series)
}

// handleEvents returns the most recent raw request events.
//
//	GET /api/v1/events?limit=100
//	→ 200 [{"timestamp":"...","method":"GET","path":"/users", ...}, ...]
func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || n > 1000 {
			writeError(w, http.StatusBadRequest, "invalid_limit",
				"limit must be an integer between 1 and 1000")
			return
		}
		limit = n
	}
	events := h.store.Recent(limit)
	writeJSON(w, http.StatusOK, events)
}

// withCORS wraps a handler to add CORS headers permitting the React dev
// server (localhost:3000 / :5173) to call these endpoints during development.
// In production, restrict origins to the dashboard's actual domain.
func (h *Handler) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	if message == "" {
		message = code
	}
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
