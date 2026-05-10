-- PostgreSQL schema for the API Gateway audit log
-- Executed on first startup via docker-entrypoint-initdb.d

CREATE TABLE IF NOT EXISTS request_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id  TEXT NOT NULL,
    user_id     TEXT,
    path        TEXT NOT NULL,
    method      TEXT NOT NULL,
    status      INT,
    latency_ms  INT,
    upstream    TEXT,
    ip_address  TEXT,
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for time-range dashboard queries: "show last 1 hour of requests"
CREATE INDEX IF NOT EXISTS idx_request_logs_created_at
    ON request_logs (created_at DESC);

-- Index for per-user debugging: "show all failed requests for user X"
CREATE INDEX IF NOT EXISTS idx_request_logs_user_id_created_at
    ON request_logs (user_id, created_at DESC);

-- Index for status-based queries: "show all 5xx errors"
CREATE INDEX IF NOT EXISTS idx_request_logs_status
    ON request_logs (status, created_at DESC);

-- Index for path-based analysis: "show slowest /payments requests"
CREATE INDEX IF NOT EXISTS idx_request_logs_path
    ON request_logs (path, created_at DESC);
