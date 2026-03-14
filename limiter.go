// Package ratelimit implements a Token Bucket rate-limiting algorithm backed
// by Redis. All state mutations are performed atomically via a server-side Lua
// script, which guarantees correctness under concurrent request load without
// requiring distributed locks.
//
// # Algorithm: Token Bucket
//
// Each unique key (e.g. client IP) has a bucket with a fixed capacity. Tokens
// are added to the bucket at a constant refill rate. Each request consumes one
// token. If the bucket is empty the request is rejected with HTTP 429.
//
// Complexity: O(1) per request — the Lua script performs a fixed number of
// Redis operations regardless of request history volume.
//
// # Redis Data Model
//
//	Key:   "rl:{key}"  (e.g. "rl:192.168.1.1")
//	Type:  Hash
//	Fields:
//	  tokens     float64  – current token count (may be fractional during refill)
//	  last_refill int64   – Unix nanosecond timestamp of last refill calculation
//
// TTL is set to 2× the refill window so idle keys are evicted automatically.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Policy defines the rate-limiting parameters applied to a bucket.
type Policy struct {
	// Rate is the number of tokens added per second (requests per second allowed
	// in steady state).
	Rate float64

	// Burst is the maximum number of tokens the bucket can hold. This sets the
	// ceiling on burst traffic: a client can fire up to Burst requests instantly
	// before being throttled.
	Burst float64
}

// Result is returned by Limiter.Allow and carries the outcome and rate-limit
// metadata that should be surfaced in response headers.
type Result struct {
	// Allowed is true if the request consumed a token and may proceed.
	Allowed bool

	// Remaining is the number of tokens left in the bucket after this request.
	Remaining float64

	// RetryAfter is only meaningful when Allowed==false. It is the minimum
	// duration the client should wait before retrying.
	RetryAfter time.Duration

	// ResetAt is when the bucket will be full again.
	ResetAt time.Time
}

// Limiter is the rate-limiting engine. It is safe for concurrent use.
type Limiter struct {
	rdb    *redis.Client
	policy Policy
	// script is the pre-loaded Lua script SHA. Using EVALSHA avoids sending
	// the full script body on every request.
	script *redis.Script
}

// tokenBucketLua is the atomic Token Bucket implementation.
// It runs entirely inside Redis, eliminating race conditions between the
// read-modify-write cycle that would exist if this logic lived in Go.
//
// Arguments (ARGV):
//
//	[1] rate        – tokens added per second (float)
//	[2] burst       – bucket capacity (float)
//	[3] now_ns      – current time in nanoseconds (int)
//	[4] ttl_sec     – key TTL in seconds (int)
//
// Returns: {allowed (0|1), remaining_tokens (float), retry_after_ms (int)}
const tokenBucketLua = `
local key         = KEYS[1]
local rate        = tonumber(ARGV[1])
local burst       = tonumber(ARGV[2])
local now_ns      = tonumber(ARGV[3])
local ttl_sec     = tonumber(ARGV[4])

-- Read current bucket state
local data = redis.call("HMGET", key, "tokens", "last_refill")
local tokens      = tonumber(data[1])
local last_refill = tonumber(data[2])

-- First request: initialise the bucket full
if tokens == nil then
    tokens      = burst
    last_refill = now_ns
end

-- Calculate how many tokens have been added since the last request
local elapsed_sec = (now_ns - last_refill) / 1e9
local refill      = elapsed_sec * rate
tokens = math.min(burst, tokens + refill)

local allowed       = 0
local retry_after   = 0

if tokens >= 1.0 then
    tokens  = tokens - 1.0
    allowed = 1
else
    -- Time until one token is available
    local deficit   = 1.0 - tokens
    retry_after     = math.ceil((deficit / rate) * 1000)  -- milliseconds
end

-- Persist updated state and refresh TTL
redis.call("HMSET", key, "tokens", tokens, "last_refill", now_ns)
redis.call("EXPIRE", key, ttl_sec)

return {allowed, tokens, retry_after}
`

// New creates a Limiter using the provided Redis client and Policy.
// Returns an error if the policy values are invalid.
func New(rdb *redis.Client, policy Policy) (*Limiter, error) {
	if policy.Rate <= 0 {
		return nil, fmt.Errorf("ratelimit: Rate must be > 0, got %f", policy.Rate)
	}
	if policy.Burst <= 0 {
		return nil, fmt.Errorf("ratelimit: Burst must be > 0, got %f", policy.Burst)
	}

	return &Limiter{
		rdb:    rdb,
		policy: policy,
		script: redis.NewScript(tokenBucketLua),
	}, nil
}

// Allow checks whether a request associated with key is permitted under the
// configured policy. It atomically updates bucket state in Redis.
//
// key should uniquely identify the rate-limited entity — typically the client
// IP address, API key, or user ID.
func (l *Limiter) Allow(ctx context.Context, key string) (Result, error) {
	redisKey := "rl:" + key
	nowNs := time.Now().UnixNano()
	// TTL = 2× the time it takes to fully refill the bucket from empty.
	ttlSec := int64((l.policy.Burst/l.policy.Rate)*2) + 1

	vals, err := l.script.Run(ctx, l.rdb,
		[]string{redisKey},
		l.policy.Rate,
		l.policy.Burst,
		nowNs,
		ttlSec,
	).Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: lua script error: %w", err)
	}

	// Redis can return numeric Lua values as either int64 or float64 depending
	// on whether the value has a fractional part. Use a helper to handle both.
	allowed := toInt64(vals[0]) == 1
	remaining := toFloat64(vals[1])
	retryAfterMs := toInt64(vals[2])

	refillDuration := time.Duration(l.policy.Burst/l.policy.Rate*1000) * time.Millisecond
	resetAt := time.Now().Add(refillDuration)

	return Result{
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: time.Duration(retryAfterMs) * time.Millisecond,
		ResetAt:    resetAt,
	}, nil
}

// Policy returns the active rate limiting policy.
func (l *Limiter) Policy() Policy {
	return l.policy
}

// toInt64 converts a Redis Lua numeric return value to int64.
// Redis may return integers as int64 or float64 depending on context.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// toFloat64 converts a Redis Lua numeric return value to float64.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	return 0
}
