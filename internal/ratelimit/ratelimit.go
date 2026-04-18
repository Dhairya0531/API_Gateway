// Package ratelimit implements rate limiting middleware for the API Gateway.
// You will build this on Days 13-14 of the project.
//
// Planned implementations:
//   - Fixed window counter (Day 13) — simple Redis INCR+EXPIRE
//   - Token bucket (Day 14)        — allows bursting, per-user+per-route
//   - Sliding window (Day 21)      — via Redis Lua script (bonus)
package ratelimit
