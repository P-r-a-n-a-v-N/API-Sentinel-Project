# API Sentinel

A production-grade, lightweight API Gateway with dynamic rate-limiting and statistical anomaly detection — built in Go, backed by Redis, with a React observability dashboard.

[![CI/CD](https://github.com/P-r-a-n-a-v-N/api-sentinel/actions/workflows/ci.yml/badge.svg)](https://github.com/P-r-a-n-a-v-N/api-sentinel/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/P-r-a-n-a-v-N/api-sentinel)](https://goreportcard.com/report/github.com/P-r-a-n-a-v-N/api-sentinel)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

---



# API Sentinel
Author: Pranav N

What is this?
API Sentinel is a lightweight, smart "traffic cop" for web applications.

When you build a website or an app, people (and bots) send requests to your server to load data. If too many requests come in at once, your server can crash. If bad actors try to scrape your data, they can slow everything down.

API Sentinel sits right in front of your main server and checks every single request before letting it through.

Core Features
The Router (Reverse Proxy): It takes incoming requests and safely passes them to the right place behind the scenes.

The Bouncer (Rate Limiting): It keeps track of who is asking for data. If a single user asks for too much too quickly, Sentinel tells them to slow down, protecting the server from being overwhelmed.

The Detective (Anomaly Detection): Instead of just counting requests, it looks for weird traffic patterns. If a bot is trying to sneakily download all your data, Sentinel spots the unusual behavior and blocks it.

Why I built this ?
Most beginner projects just read and write data to a database. I built API Sentinel to understand how the internet actually handles heavy traffic. This project tackles real-world problems like server security, managing multiple connections at once, and using fast data structures to make decisions in milliseconds.


---


## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │          API Sentinel Gateway           │
                    │                                         │
Client ────────────▶│  Recovery                               │
  HTTP/HTTPS        │    └─ AccessLogger (analytics store)    │
                    │         └─ AnomalyDetector (EMA/Z-score)│
                    │              └─ RateLimiter (Token Bucket│─── Redis
                    │                   └─ ReverseProxy       │
                    │                        └─────────────────│────▶ Upstream
                    │                                         │
                    │  GET /health            → 200 OK        │
                    │  GET /api/v1/stats/*    → Dashboard API  │
                    └─────────────────────────────────────────┘
                                     │
                                     │  REST polling (2s)
                                     ▼
                    ┌─────────────────────────────────────────┐
                    │        React Dashboard (Nginx)          │
                    │  • KPI cards  • Traffic timeline chart  │
                    │  • Status distribution  • Top paths     │
                    │  • Live request event log               │
                    └─────────────────────────────────────────┘
```

## Tech Stack

| Layer            | Technology                                      |
|------------------|-------------------------------------------------|
| Gateway          | Go 1.22 — `net/http/httputil.ReverseProxy`      |
| Rate Limiting    | Redis 7 — Token Bucket via atomic Lua script    |
| Anomaly Detection| In-memory EMA + Z-score spike detection         |
| Analytics        | In-memory ring buffer (10 000 events)           |
| Dashboard        | React 19 + Vite + TailwindCSS + Recharts        |
| Container        | Docker (distroless gateway, Nginx dashboard)    |
| CI/CD            | GitHub Actions → GHCR                          |

## Project Structure

```
api-sentinel/
├── cmd/gateway/main.go              # Entry point — wires all middleware
├── internal/
│   ├── config/                      # Env-var config loading + validation
│   ├── logger/                      # Structured JSON logger (NDJSON)
│   ├── middleware/                  # Health check, panic recovery
│   ├── proxy/                       # httputil.ReverseProxy wrapper
│   ├── ratelimit/                   # Token Bucket — Redis Lua, HTTP 429
│   ├── anomaly/                     # EMA + Z-score anomaly detector
│   ├── analytics/                   # Ring-buffer event store
│   └── api/                         # REST handlers for dashboard
├── scripts/dummy_backend.go         # Local dev upstream simulator
├── dashboard/                       # React + Vite dashboard
│   ├── src/
│   │   ├── hooks/useGatewayData.ts  # Polling hooks
│   │   ├── components/              # StatCard, TrafficChart, EventsTable …
│   │   └── utils/format.ts
│   └── nginx.conf                   # Production Nginx config
├── Dockerfile.gateway               # Multi-stage → distroless (~12MB)
├── Dockerfile.dashboard             # Multi-stage → Nginx Alpine (~25MB)
├── Dockerfile.dummy                 # Dev-only upstream
├── docker-compose.yml               # Full stack orchestration
├── .github/workflows/ci.yml         # CI/CD pipeline
└── .golangci.yml                    # Linter config
```

## Phases Delivered

| Phase | Feature | Status |
|-------|---------|--------|
| 1 | HTTP server + reverse proxy + structured logging | ✅ |
| 2 | Redis Token Bucket rate limiter — O(1), atomic Lua | ✅ |
| 3 | EMA + Z-score anomaly detection + analytics API | ✅ |
| 4 | React dashboard — KPIs, charts, live event log | ✅ |
| 5 | Docker, docker-compose, GitHub Actions CI/CD | ✅ |

**Test results: 37 tests, 0 failures, race detector clean.**

---

## Quick Start

### Option A — Docker Compose (recommended)

```bash
git clone https://github.com/P-r-a-n-a-v-N/api-sentinel.git
cd api-sentinel
cp .env.example .env

# Start full stack: Redis + dummy backend + gateway + dashboard
docker-compose up --build

# Gateway:   http://localhost:8080
# Dashboard: http://localhost:3000
```

### Option B — Local development

**Prerequisites:** Go 1.22+, Redis 7, Node 22+

```bash
# Terminal 1 — Redis
redis-server

# Terminal 2 — Dummy backend
go run ./scripts/dummy_backend.go
# Listening on :9000

# Terminal 3 — Gateway
cp .env.example .env
GATEWAY_PORT=8080 UPSTREAM_URL=http://localhost:9000 \
  go run ./cmd/gateway/main.go

# Terminal 4 — Dashboard (dev server with hot reload)
cd dashboard && npm install && npm run dev
# http://localhost:3000
```

---

## Testing

```bash
# All tests with race detector
go test -race ./...

# With coverage report
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Single package verbose
go test -v -race ./internal/ratelimit/...

# Against a real Redis (integration tests)
REDIS_ADDR=localhost:6379 go test -race ./internal/ratelimit/...
```

**37 tests across 6 packages — all PASS, no races detected.**

---

## Configuration

All config via environment variables — 12-factor compatible.

| Variable             | Default                  | Description                              |
|----------------------|--------------------------|------------------------------------------|
| `GATEWAY_PORT`       | `8080`                   | TCP port the gateway listens on          |
| `UPSTREAM_URL`       | `http://localhost:9000`  | Upstream service base URL                |
| `LOG_LEVEL`          | `info`                   | `debug` \| `info` \| `warn` \| `error`  |
| `READ_TIMEOUT_SEC`   | `10`                     | HTTP read timeout (seconds)              |
| `WRITE_TIMEOUT_SEC`  | `10`                     | HTTP write timeout (seconds)             |
| `IDLE_TIMEOUT_SEC`   | `60`                     | HTTP keep-alive idle timeout (seconds)   |
| `REDIS_ADDR`         | `localhost:6379`         | Redis address                            |
| `REDIS_PASSWORD`     | `""`                     | Redis AUTH password                      |
| `RATE_LIMIT_RPS`     | `100`                    | Allowed requests/second per client IP    |
| `RATE_LIMIT_BURST`   | `200`                    | Token bucket burst capacity              |

---

## API Reference

### Gateway Internal Endpoints

```
GET /health
→ 200 {"status":"ok","go_version":"go1.22","uptime":"5m3s","timestamp":"..."}

GET /api/v1/stats/summary
→ 200 {"total_requests":1000,"blocked_requests":12,"anomaly_count":3,...}

GET /api/v1/stats/timeseries?window=60
→ 200 [{"ts":"...","total":5,"blocked":0,"anomalous":0}, ...]

GET /api/v1/events?limit=100
→ 200 [{"timestamp":"...","method":"GET","path":"/users","status_code":200,...}]
```

### Rate Limit Response Headers

Every proxied response includes:

```
X-RateLimit-Limit:     200        # bucket capacity
X-RateLimit-Remaining: 198.00     # tokens remaining
X-RateLimit-Reset:     1720000060 # Unix timestamp of full reset
```

When rate-limited (HTTP 429):
```
Retry-After: 1
{"error":"rate_limit_exceeded","message":"...","retry_after":1}
```

---

## Log Format

Every log line is a single JSON object (NDJSON), ingestible by Datadog, Loki, CloudWatch Logs Insights.

```json
{"ts":"2024-01-15T10:23:45.123Z","level":"INFO","component":"proxy","msg":"proxied request","method":"GET","path":"/users","status":200,"latency_ms":4,"bytes_sent":312,"request_id":"aB3xKm9pQr2w","remote_addr":"127.0.0.1","upstream":"localhost:9000"}
```

Error entries additionally include `"caller":"file.go:42"` for post-mortem debugging.

---

## Design Decisions

**Why Go over Node.js/TypeScript?**
Go goroutines handle C10K+ concurrency at ~8KB stack per goroutine. `net/http/httputil.ReverseProxy` is production battle-tested (used in Kubernetes ingress). A compiled binary is ~12MB with zero runtime dependencies vs Node's 300MB+ footprint.

**Why stdlib only for the gateway core?**
Zero external dependencies = zero supply-chain risk, smaller attack surface, faster builds, and a 12MB distroless Docker image. Go 1.22's ServeMux supports the routing patterns needed for a gateway.

**Why Lua in Redis for rate limiting?**
The read-modify-write cycle of a Token Bucket is inherently a race condition under concurrent load. A Lua script executes atomically in Redis — no distributed locks, no WATCH/MULTI/EXEC round-trips, true O(1) complexity.

**Why EMA + Z-score for anomaly detection?**
Exponential Moving Average adapts to gradual traffic growth without false positives. Z-score normalization makes the threshold configurable in standard deviations rather than absolute request counts, making it scale-independent across upstream sizes.

---

## Deployment (AWS/GCP Free Tier)

### AWS — EC2 t3.micro + ElastiCache Redis
```bash
# On the instance
docker-compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### GCP — Cloud Run
```bash
# Build and push to Artifact Registry, then deploy
gcloud run deploy api-sentinel-gateway \
  --image ghcr.io/yourusername/api-sentinel/gateway:latest \
  --port 8080 \
  --set-env-vars REDIS_ADDR=...,UPSTREAM_URL=...
```

---

## License

MIT — see [LICENSE](LICENSE).
