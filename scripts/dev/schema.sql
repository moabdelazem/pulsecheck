-- Idempotent: safe to run on every start.
CREATE TABLE IF NOT EXISTS monitors (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    url        TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS check_results (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    monitor_id  BIGINT NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    up          BOOLEAN NOT NULL,
    status_code INT,
    latency_ms  INT NOT NULL,
    error       TEXT,
    checked_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS check_results_monitor_checked_at_idx
    ON check_results (monitor_id, checked_at DESC);
