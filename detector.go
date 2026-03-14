// Package anomaly implements a real-time statistical anomaly detection engine
// for HTTP request traffic. It uses two complementary algorithms:
//
// # Exponential Moving Average (EMA) with Z-score spike detection
//
// For each tracked key (e.g. client IP), the engine maintains:
//   - An EMA of the request rate (requests per second) with configurable α
//   - A rolling estimate of the standard deviation (via EMA of squared deviations)
//
// A request is flagged as anomalous when the current rate deviates from the EMA
// by more than `Threshold` standard deviations (i.e. Z-score > Threshold).
//
// This is equivalent to detecting points outside a dynamic Bollinger Band and
// is well-suited for bursty, non-stationary traffic patterns.
//
// # Sliding Window Counter
//
// Additionally, a fixed sliding window tracks the total request count over the
// last N seconds. This catches sustained high-volume attacks that might not
// trigger the EMA detector if the EMA itself drifts upward.
//
// # Complexity
//
// O(1) time and O(K) space where K is the number of distinct tracked keys.
// All state is in-memory with no Redis dependency (analytics are ephemeral).
package anomaly

import (
	"math"
	"sync"
	"time"

	"github.com/yourusername/api-sentinel/internal/logger"
)

// Config holds tuning parameters for the anomaly detector.
type Config struct {
	// Alpha is the EMA smoothing factor in (0, 1).
	// Higher α = faster adaptation, lower α = longer memory.
	// Recommended range: 0.1–0.3 for API traffic.
	Alpha float64

	// Threshold is the Z-score above which a data point is considered anomalous.
	// Common values: 2.0 (95% CI), 3.0 (99.7% CI).
	Threshold float64

	// WindowSize is the number of seconds in the sliding window counter.
	WindowSize time.Duration

	// MinSamples is the minimum number of observations required before anomaly
	// detection activates. Prevents false positives during cold-start.
	MinSamples int
}

// DefaultConfig returns a sensible production-ready configuration.
func DefaultConfig() Config {
	return Config{
		Alpha:      0.2,
		Threshold:  3.0,
		WindowSize: 60 * time.Second,
		MinSamples: 10,
	}
}

// AnomalyEvent is emitted when an anomaly is detected.
type AnomalyEvent struct {
	Key       string
	Timestamp time.Time
	ZScore    float64
	// CurrentRate is the observed requests-per-second at time of detection.
	CurrentRate float64
	// ExpectedRate is the EMA-predicted normal rate.
	ExpectedRate float64
	Reason       string
}

// keyState holds per-key statistical state.
type keyState struct {
	mu       sync.Mutex
	ema      float64 // exponential moving average of rate
	emavar   float64 // exponential moving average of variance
	samples  int     // total observations
	lastSeen time.Time

	// Sliding window: ring buffer of second-level buckets
	window     []int64
	windowHead int
	windowSum  int64
	windowTS   time.Time // start time of the current window head bucket
}

// Detector is the anomaly detection engine. It is safe for concurrent use.
type Detector struct {
	cfg    Config
	log    *logger.Logger
	mu     sync.RWMutex
	states map[string]*keyState

	// onAnomaly is called (in a goroutine) whenever an anomaly is detected.
	// It is set via WithAnomalyCallback.
	onAnomaly func(AnomalyEvent)
}

// New creates a Detector with the given configuration.
func New(cfg Config, log *logger.Logger) *Detector {
	return &Detector{
		cfg:    cfg,
		log:    log,
		states: make(map[string]*keyState),
		onAnomaly: func(e AnomalyEvent) {
			// Default: structured log entry only.
			log.Warn("anomaly detected", logger.F{
				"key":           e.Key,
				"z_score":       math.Round(e.ZScore*100) / 100,
				"current_rate":  math.Round(e.CurrentRate*100) / 100,
				"expected_rate": math.Round(e.ExpectedRate*100) / 100,
				"reason":        e.Reason,
			})
		},
	}
}

// WithAnomalyCallback replaces the default anomaly handler. The callback is
// invoked in a new goroutine so it must be safe for concurrent use.
func (d *Detector) WithAnomalyCallback(fn func(AnomalyEvent)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onAnomaly = fn
}

// Record observes one request for key at the given timestamp.
// It updates the EMA/variance estimates and returns true if the observation
// is statistically anomalous.
func (d *Detector) Record(key string, now time.Time) bool {
	d.mu.RLock()
	st, exists := d.states[key]
	d.mu.RUnlock()

	if !exists {
		d.mu.Lock()
		// Double-checked locking
		st, exists = d.states[key]
		if !exists {
			buckets := int(d.cfg.WindowSize.Seconds())
			if buckets < 1 {
				buckets = 1
			}
			st = &keyState{
				window:   make([]int64, buckets),
				windowTS: now,
			}
			d.states[key] = st
		}
		d.mu.Unlock()
	}

	return d.update(st, key, now)
}

// update performs the core statistical update and anomaly check.
func (d *Detector) update(st *keyState, key string, now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()

	// ── Sliding window update ─────────────────────────────────────────────────
	buckets := len(st.window)
	elapsed := now.Sub(st.windowTS)
	elapsedBuckets := int(elapsed.Seconds())

	if elapsedBuckets >= buckets {
		// Full window rotation — reset everything
		for i := range st.window {
			st.window[i] = 0
		}
		st.windowSum = 0
		st.windowHead = 0
		st.windowTS = now
	} else if elapsedBuckets > 0 {
		// Advance the ring buffer, zeroing expired buckets
		for i := 0; i < elapsedBuckets; i++ {
			next := (st.windowHead + i + 1) % buckets
			st.windowSum -= st.window[next]
			st.window[next] = 0
		}
		st.windowHead = (st.windowHead + elapsedBuckets) % buckets
		st.windowTS = st.windowTS.Add(time.Duration(elapsedBuckets) * time.Second)
	}

	currentBucket := (st.windowHead) % buckets
	st.window[currentBucket]++
	st.windowSum++

	// ── EMA / Z-score update ──────────────────────────────────────────────────
	st.samples++
	if !st.lastSeen.IsZero() {
		dt := now.Sub(st.lastSeen).Seconds()
		if dt < 0.001 {
			dt = 0.001 // clamp to avoid division near-zero
		}
		currentRate := 1.0 / dt // instantaneous rate: requests/second

		if st.samples <= d.cfg.MinSamples {
			// Warm-up phase: only update EMA, never flag anomaly
			if st.ema == 0 {
				st.ema = currentRate
				st.emavar = 0
			} else {
				st.ema = d.cfg.Alpha*currentRate + (1-d.cfg.Alpha)*st.ema
				diff := currentRate - st.ema
				st.emavar = d.cfg.Alpha*diff*diff + (1-d.cfg.Alpha)*st.emavar
			}
		} else {
			prevEMA := st.ema
			st.ema = d.cfg.Alpha*currentRate + (1-d.cfg.Alpha)*st.ema
			diff := currentRate - prevEMA
			st.emavar = d.cfg.Alpha*diff*diff + (1-d.cfg.Alpha)*st.emavar

			stddev := math.Sqrt(st.emavar)
			if stddev > 0 {
				zScore := math.Abs(currentRate-prevEMA) / stddev
				if zScore > d.cfg.Threshold {
					event := AnomalyEvent{
						Key:          key,
						Timestamp:    now,
						ZScore:       zScore,
						CurrentRate:  currentRate,
						ExpectedRate: prevEMA,
						Reason:       "ema_zscore_spike",
					}
					go d.onAnomaly(event)
					st.lastSeen = now
					return true
				}
			}
		}
	}

	st.lastSeen = now
	return false
}

// Stats returns a point-in-time snapshot of the statistics for a key.
// Returns zero values if the key has never been seen.
func (d *Detector) Stats(key string) Stats {
	d.mu.RLock()
	st, ok := d.states[key]
	d.mu.RUnlock()
	if !ok {
		return Stats{}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return Stats{
		EMA:         st.ema,
		StdDev:      math.Sqrt(st.emavar),
		Samples:     st.samples,
		WindowTotal: st.windowSum,
	}
}

// Stats is a point-in-time statistical snapshot for a key.
type Stats struct {
	EMA         float64 // exponential moving average of request rate (req/s)
	StdDev      float64 // estimated standard deviation
	Samples     int     // total observations
	WindowTotal int64   // requests in the current sliding window
}

// KeyCount returns the number of distinct keys currently tracked.
func (d *Detector) KeyCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.states)
}
