-- shipping-and-tracking analytics read model (ADR 0012).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/st-projector and read (read-only) by cmd/st-reports.
-- Everything here is a projection of this context's own integration topic
-- (retailnetwork.shipping-and-tracking.events), not a source of truth, and can
-- be dropped and rebuilt from the topic.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect on a fact table. occurred_at is the
-- event's CloudEvents `time` (the basis of /reports/freshness).
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per tracking number, from label.LabelIssued: the package's tier.
CREATE TABLE package_labels (
    tracking_number TEXT        PRIMARY KEY CHECK (tracking_number <> ''),
    label_ref       TEXT        NOT NULL CHECK (label_ref <> ''),
    tier            TEXT        NOT NULL CHECK (tier <> ''),
    labelled_at     TIMESTAMPTZ NOT NULL,
    event_id        TEXT        NOT NULL
);

-- One row per journey.PackageScanned: an APPLIED scan of a journey.
--   scanned_at  the scan's own instant (payload `at`)
CREATE TABLE journey_scans (
    event_id        TEXT        PRIMARY KEY,
    tracking_number TEXT        NOT NULL CHECK (tracking_number <> ''),
    scan_id         TEXT        NOT NULL CHECK (scan_id <> ''),
    scan_type       TEXT        NOT NULL CHECK (scan_type <> ''),
    scanned_at      TIMESTAMPTZ NOT NULL
);

-- One row per tracking number: the journey's terminal outcome, from
-- journey.PackageDelivered (deliveredAt), PackageReturned (returnedAt) or
-- PackageLost (declaredAt). A journey has exactly one terminal state.
CREATE TABLE package_outcomes (
    tracking_number TEXT        PRIMARY KEY CHECK (tracking_number <> ''),
    outcome         TEXT        NOT NULL CHECK (outcome IN ('DELIVERED', 'RETURNED', 'LOST')),
    outcome_at      TIMESTAMPTZ NOT NULL,
    event_id        TEXT        NOT NULL
);

-- The reports join scans to outcomes per tracking number and filter outcomes
-- on their instant.
CREATE INDEX idx_journey_scans_tracking_type ON journey_scans (tracking_number, scan_type);
CREATE INDEX idx_package_outcomes_outcome_at ON package_outcomes (outcome_at);
CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);
