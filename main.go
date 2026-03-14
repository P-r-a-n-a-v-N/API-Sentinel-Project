// Command gateway is the API Sentinel API Gateway entry point.
//
// Full middleware chain (outermost to innermost):
//   Recovery -> AccessLogger -> AnomalyDetector -> RateLimiter -> ProxyHandler
//
// Internal routes (not proxied):
//   GET /health                  -> liveness probe
//   GET /api/v1/stats/summary    -> aggregate dashboard stats
//   GET /api/v1/stats/timeseries -> per-second time series
//   GET /api/v1/events           -> recent raw events
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/P-r-a-n-a-v-N/API-Sentinel-Project/internal/analytics"
	apihandler "github.com/yourusername/API-Sentinel-Project/internal/api"
	"github.com/P-r-a-n-a-v-N/API-Sentinel-Project/internal/anomaly"
	"github.com/P-r-a-n-a-v-N/API-Sentinel-Project/internal/config"
	"github.com/P-r-a-n-a-v-N/API-Sentinel-Project/internal/logger"
	"github.com/P-r-a-n-a-v-N/API-Sentinel-Project/internal/middleware"
	"github.com/P-r-a-n-a-v-N/API-Sentinel-Project/internal/proxy"
	"github.com/P-r-a-n-a-v-N/API-Sentinel-Project/internal/ratelimit"
)

func main() {
	log := logger.New("gateway")

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("failed to load configuration", err, nil)
	}
	log.Info("configuration loaded", logger.F{
		"port": cfg.GatewayPort, "upstream": cfg.UpstreamURL,
	})

	// Redis client
	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: getEnv("REDIS_PASSWORD", ""),
		DB:       0,
	})
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pingCancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Error("redis unavailable — rate limiting disabled, failing open", err, logger.F{"redis_addr": redisAddr})
	} else {
		log.Info("redis connected", logger.F{"redis_addr": redisAddr})
	}

	// Analytics store
	store := analytics.NewStore()

	// Rate limiter
	policy := ratelimit.Policy{
		Rate:  floatEnv("RATE_LIMIT_RPS", 100),
		Burst: floatEnv("RATE_LIMIT_BURST", 200),
	}
	limiter, err := ratelimit.New(rdb, policy)
	if err != nil {
		log.Fatal("failed to create rate limiter", err, nil)
	}
	log.Info("rate limiter configured", logger.F{"rate_rps": policy.Rate, "burst": policy.Burst})

	// Anomaly detector
	detector := anomaly.New(anomaly.DefaultConfig(), logger.New("anomaly"))
	detector.WithAnomalyCallback(func(e anomaly.AnomalyEvent) {
		log.Warn("traffic anomaly", logger.F{
			"key": e.Key, "z_score": e.ZScore,
			"current_rate": e.CurrentRate, "expected_rate": e.ExpectedRate,
		})
	})

	// Reverse proxy
	proxyHandler, err := proxy.New(cfg.UpstreamURL, logger.New("proxy"))
	if err != nil {
		log.Fatal("failed to create proxy handler", err, logger.F{"upstream": cfg.UpstreamURL})
	}

	// Router
	mux := http.NewServeMux()
	mux.Handle("/health", middleware.Health())

	apiH := apihandler.New(store, logger.New("api"))
	apiH.RegisterRoutes(mux)

	mux.Handle("/", buildChain(proxyHandler, store, limiter, detector, log))
	root := middleware.Recovery(mux)

	// Server
	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      root,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("gateway listening", logger.F{"addr": srv.Addr, "upstream": cfg.UpstreamURL})
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Info("shutdown signal received", logger.F{"signal": sig.String()})
	case err := <-serverErr:
		log.Error("server error", err, nil)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log.Info("shutting down gracefully", logger.F{"timeout": "30s"})
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", err, nil)
		os.Exit(1)
	}
	rdb.Close()
	log.Info("gateway stopped cleanly", nil)
}

// buildChain assembles the middleware stack. Outermost to innermost:
//   accessLogger -> anomalyDetector -> rateLimiter -> proxy
func buildChain(
	p http.Handler,
	store *analytics.Store,
	limiter *ratelimit.Limiter,
	detector *anomaly.Detector,
	log *logger.Logger,
) http.Handler {
	h := p
	h = ratelimit.Middleware(limiter, logger.New("ratelimit"), h)
	h = anomaly.Middleware(detector, logger.New("anomaly"), h)
	h = accessLogMiddleware(store, log, h)
	return h
}

func accessLogMiddleware(store *analytics.Store, log *logger.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &recorderWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rw, r)
		latency := time.Since(start)
		anomalous := rw.Header().Get("X-Anomaly") == "true"
		blocked := rw.statusCode == http.StatusTooManyRequests
		store.Record(analytics.RequestEvent{
			Timestamp: start.UTC(), Method: r.Method, Path: r.URL.Path,
			StatusCode: rw.statusCode, LatencyMs: latency.Milliseconds(),
			ClientIP: extractIP(r), RequestID: rw.Header().Get("X-Request-ID"),
			Anomalous: anomalous, Blocked: blocked, BytesSent: rw.bytesWritten,
		})
		_ = log
	})
}

type recorderWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (r *recorderWriter) WriteHeader(code int) { r.statusCode = code; r.ResponseWriter.WriteHeader(code) }
func (r *recorderWriter) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(n)
	return n, err
}

func extractIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	if i := strings.LastIndex(r.RemoteAddr, ":"); i != -1 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func floatEnv(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var f float64
	if _, err := fmt.Sscanf(v, "%f", &f); err != nil || f <= 0 {
		return fallback
	}
	return f
}

func init() {
	// Support a -health-check flag so the Docker HEALTHCHECK can call
	// the binary itself instead of needing curl in the image.
	// When -health-check is passed, we do a quick HTTP GET to /health
	// on the configured port and exit 0/1 accordingly.
	for _, arg := range os.Args[1:] {
		if arg == "-health-check" || arg == "--health-check" {
			port := getEnv("GATEWAY_PORT", "8080")
			resp, err := http.Get("http://localhost:" + port + "/health")
			if err != nil || resp.StatusCode != http.StatusOK {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}
