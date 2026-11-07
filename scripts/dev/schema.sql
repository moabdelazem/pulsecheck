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

-- Per-monitor settings. ADD COLUMN IF NOT EXISTS upgrades existing databases in place.
ALTER TABLE monitors
    ADD COLUMN IF NOT EXISTS name             TEXT    NOT NULL DEFAULT ''
        CHECK (char_length(name) <= 100),
    ADD COLUMN IF NOT EXISTS method           TEXT    NOT NULL DEFAULT 'GET'
        CHECK (method IN ('GET', 'HEAD')),
    ADD COLUMN IF NOT EXISTS interval_seconds INT     NOT NULL DEFAULT 30
        CHECK (interval_seconds BETWEEN 10 AND 86400),
    ADD COLUMN IF NOT EXISTS timeout_seconds  INT     NOT NULL DEFAULT 5
        CHECK (timeout_seconds BETWEEN 1 AND 30),
    -- NULL = any 2xx or 3xx counts as up
    ADD COLUMN IF NOT EXISTS expected_status  INT
        CHECK (expected_status BETWEEN 100 AND 599),
    ADD COLUMN IF NOT EXISTS paused           BOOLEAN NOT NULL DEFAULT false;

-- Lets the checker find and delete old results without scanning the whole table.
CREATE INDEX IF NOT EXISTS check_results_checked_at_idx ON check_results (checked_at);
