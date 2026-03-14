// Package analytics provides an in-memory time-series store for gateway
// request events. It is consumed by the REST API endpoints that feed the
// React dashboard.
//
// Design: a fixed-size ring buffer of RequestEvent records, protected by a
// single RWMutex. Writes are O(1). Reads are O(N) where N ≤ MaxEvents.
// There is no persistence — events are lost on restart. For production use,
// replace or supplement with Redis Streams or ClickHouse.
package analytics

import (
	"sync"
	"time"
)

const MaxEvents = 10_000

// RequestEvent is a single recorded HTTP transaction.
type RequestEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	ClientIP   string    `json:"client_ip"`
	RequestID  string    `json:"request_id"`
	Anomalous  bool      `json:"anomalous"`
	Blocked    bool      `json:"blocked"` // true = rate-limited (429)
	BytesSent  int64     `json:"bytes_sent"`
}

// Store is a thread-safe ring buffer of RequestEvents.
type Store struct {
	mu     sync.RWMutex
	events [MaxEvents]RequestEvent
	head   int // next write position
	count  int // total events written (capped at MaxEvents)
}

// NewStore constructs an empty analytics store.
func NewStore() *Store {
	return &Store{}
}

// Record appends a new event to the ring buffer. O(1).
func (s *Store) Record(e RequestEvent) {
	s.mu.Lock()
	s.events[s.head] = e
	s.head = (s.head + 1) % MaxEvents
	if s.count < MaxEvents {
		s.count++
	}
	s.mu.Unlock()
}

// Recent returns up to n events in reverse-chronological order (newest first).
// If n <= 0 or n > MaxEvents, all stored events are returned.
func (s *Store) Recent(n int) []RequestEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := s.count
	if n <= 0 || n > total {
		n = total
	}
	out := make([]RequestEvent, 0, n)
	// Walk backward from head
	for i := 1; i <= n; i++ {
		idx := (s.head - i + MaxEvents) % MaxEvents
		out = append(out, s.events[idx])
	}
	return out
}

// Summary returns aggregate statistics over all stored events.
func (s *Store) Summary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total, blocked, anomalous int64
	var totalLatency int64
	statusCounts := make(map[int]int64)
	pathCounts := make(map[string]int64)

	for i := 0; i < s.count; i++ {
		idx := (s.head - 1 - i + MaxEvents) % MaxEvents
		e := s.events[idx]
		total++
		totalLatency += e.LatencyMs
		statusCounts[e.StatusCode]++
		pathCounts[e.Path]++
		if e.Blocked {
			blocked++
		}
		if e.Anomalous {
			anomalous++
		}
	}

	var avgLatency float64
	if total > 0 {
		avgLatency = float64(totalLatency) / float64(total)
	}

	return Summary{
		TotalRequests:   total,
		BlockedRequests: blocked,
		AnomalyCount:    anomalous,
		AvgLatencyMs:    avgLatency,
		StatusCounts:    statusCounts,
		TopPaths:        topN(pathCounts, 10),
	}
}

// TimeSeries returns request counts bucketed by second over the last windowSec
// seconds, suitable for a time-series chart.
func (s *Store) TimeSeries(windowSec int) []TimePoint {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if windowSec <= 0 {
		windowSec = 60
	}
	now := time.Now().UTC().Truncate(time.Second)
	buckets := make(map[int64]*TimePoint)

	for i := 0; i < s.count; i++ {
		idx := (s.head - 1 - i + MaxEvents) % MaxEvents
		e := s.events[idx]
		ts := e.Timestamp.UTC().Truncate(time.Second)
		if now.Sub(ts) > time.Duration(windowSec)*time.Second {
			continue
		}
		key := ts.Unix()
		if _, ok := buckets[key]; !ok {
			buckets[key] = &TimePoint{Timestamp: ts}
		}
		buckets[key].Total++
		if e.Blocked {
			buckets[key].Blocked++
		}
		if e.Anomalous {
			buckets[key].Anomalous++
		}
	}

	// Fill gaps with zeroes and sort
	points := make([]TimePoint, windowSec)
	for i := 0; i < windowSec; i++ {
		t := now.Add(-time.Duration(windowSec-1-i) * time.Second)
		key := t.Unix()
		if bp, ok := buckets[key]; ok {
			points[i] = *bp
		} else {
			points[i] = TimePoint{Timestamp: t}
		}
	}
	return points
}

// Summary contains aggregate statistics over the event store.
type Summary struct {
	TotalRequests   int64            `json:"total_requests"`
	BlockedRequests int64            `json:"blocked_requests"`
	AnomalyCount    int64            `json:"anomaly_count"`
	AvgLatencyMs    float64          `json:"avg_latency_ms"`
	StatusCounts    map[int]int64    `json:"status_counts"`
	TopPaths        []PathCount      `json:"top_paths"`
}

// TimePoint is one second-level bucket in a time series.
type TimePoint struct {
	Timestamp time.Time `json:"ts"`
	Total     int64     `json:"total"`
	Blocked   int64     `json:"blocked"`
	Anomalous int64     `json:"anomalous"`
}

// PathCount pairs a path with its request count.
type PathCount struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
}

// topN extracts the top-n entries from a map by value, sorted descending.
func topN(m map[string]int64, n int) []PathCount {
	type kv struct {
		k string
		v int64
	}
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	// Simple insertion sort — n is small (≤10)
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j].v > pairs[j-1].v; j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
	if n > len(pairs) {
		n = len(pairs)
	}
	result := make([]PathCount, n)
	for i := 0; i < n; i++ {
		result[i] = PathCount{Path: pairs[i].k, Count: pairs[i].v}
	}
	return result
}
