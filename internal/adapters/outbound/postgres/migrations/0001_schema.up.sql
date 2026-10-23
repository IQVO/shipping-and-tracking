-- shipping-and-tracking schema (ADR 0002, ADR 0003).
--
-- shipping_labels      ShippingLabel aggregate, one row per package. The ship-to
--                      address is NOT here: only the opaque stop_key and the
--                      postal prefix derived from it.
-- ship_to_snapshots    the AES-256-GCM sealed ship-to, one per label, deleted 30
--                      days after the journey closed (ADR 0002 A9). Never read
--                      back by a response.
-- package_journeys     PackageJourney aggregate, optimistic `version`.
-- journey_scans        the Scan entities of a journey, append only (`seq` is the
--                      recorded order).
-- catalog_dimensions   CatalogDimensionsView read model (offer-availability).
-- outbox_events        transactional outbox, one already-encoded Kafka message
--                      per row, inserted in the SAME transaction as the aggregate.
-- processed_events     inbox of the Kafka consumers, claimed in the effect's
--                      transaction.
-- idempotency_keys     the Idempotency-Key middleware, (route, key), 24 h TTL.

CREATE TABLE shipping_labels (
    label_ref                 TEXT        PRIMARY KEY,
    tracking_number           TEXT        NOT NULL,
    po_number                 TEXT        NOT NULL,
    package_no                INTEGER     NOT NULL CHECK (package_no >= 1),
    assignment_id             TEXT        NOT NULL,
    order_id                  TEXT        NOT NULL,
    tier                      TEXT        NOT NULL CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD')),
    carrier                   TEXT        NOT NULL,
    stop_key                  TEXT        NOT NULL,
    destination_postal_prefix TEXT        NOT NULL,
    status                    TEXT        NOT NULL CHECK (status IN ('ISSUED', 'VOIDED')),
    issued_at                 TIMESTAMPTZ NOT NULL,
    void_reason               TEXT,
    voided_at                 TIMESTAMPTZ,
    version                   BIGINT      NOT NULL CHECK (version >= 1),
    CONSTRAINT shipping_labels_package_key UNIQUE (po_number, package_no),
    CONSTRAINT shipping_labels_tracking_key UNIQUE (tracking_number)
);

CREATE TABLE ship_to_snapshots (
    label_ref  TEXT        PRIMARY KEY REFERENCES shipping_labels (label_ref),
    key_id     TEXT        NOT NULL,
    nonce      BYTEA       NOT NULL,
    ciphertext BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE package_journeys (
    tracking_number  TEXT        PRIMARY KEY,
    label_ref        TEXT        NOT NULL UNIQUE REFERENCES shipping_labels (label_ref),
    po_number        TEXT        NOT NULL,
    assignment_id    TEXT        NOT NULL,
    order_id         TEXT        NOT NULL,
    tier             TEXT        NOT NULL CHECK (tier IN ('SAME_DAY', 'NEXT_DAY', 'TWO_DAY', 'STANDARD')),
    state            TEXT        NOT NULL CHECK (state IN ('LABELLED', 'HANDED_OVER', 'IN_TRANSIT', 'AT_STATION',
                                                           'OUT_FOR_DELIVERY', 'DELIVERED', 'RETURNED', 'LOST', 'VOIDED')),
    started_at       TIMESTAMPTZ NOT NULL,
    last_scan_at     TIMESTAMPTZ,
    last_node_id     TEXT,
    handed_over_at   TIMESTAMPTZ,
    delivered_at     TIMESTAMPTZ,
    closed_at        TIMESTAMPTZ,
    last_exception_at TIMESTAMPTZ,
    version          BIGINT      NOT NULL CHECK (version >= 1)
);

-- The silence sweep only ever scans open journeys past LABELLED, by last scan.
CREATE INDEX idx_package_journeys_stale ON package_journeys (last_scan_at, tracking_number)
    WHERE state IN ('HANDED_OVER', 'IN_TRANSIT', 'AT_STATION', 'OUT_FOR_DELIVERY');

-- The ship-to purge joins closed journeys by closing instant.
CREATE INDEX idx_package_journeys_closed ON package_journeys (closed_at) WHERE closed_at IS NOT NULL;

CREATE TABLE journey_scans (
    tracking_number TEXT        NOT NULL REFERENCES package_journeys (tracking_number) ON DELETE CASCADE,
    seq             INTEGER     NOT NULL CHECK (seq >= 1),
    scan_id         TEXT        NOT NULL,
    node_id         TEXT        NOT NULL,
    scan_type       TEXT        NOT NULL,
    at              TIMESTAMPTZ NOT NULL,
    applied         BOOLEAN     NOT NULL,
    PRIMARY KEY (tracking_number, seq),
    CONSTRAINT journey_scans_scan_id_key UNIQUE (tracking_number, scan_id)
);

CREATE TABLE catalog_dimensions (
    catalog_item_id TEXT        PRIMARY KEY,
    title           TEXT        NOT NULL,
    length_mm       BIGINT      NOT NULL,
    width_mm        BIGINT      NOT NULL,
    height_mm       BIGINT      NOT NULL,
    weight_g        BIGINT      NOT NULL,
    hazmat          BOOLEAN     NOT NULL,
    fragile         BOOLEAN     NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

--   event_id    the CloudEvents `id`, minted once at encode time and persisted:
--               a relay retry republishes the same id.
--   event_type  the FULL CloudEvents `type`.
--   subject     the CloudEvents `subject`; key is the Kafka message key.
--   dataschema  the CloudEvents `dataschema` URN.
--   value       the structured-mode CloudEvents JSON bytes.
--   headers     [{"key":..,"value":..}] Kafka headers (content-type).
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

-- The relay only ever scans the unpublished tail; keep that scan tiny.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

CREATE TABLE processed_events (
    consumer     TEXT        NOT NULL,
    event_id     TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);

-- The Idempotency-Key middleware inserts a bare row inside its own transaction
-- before calling the handler, then updates it with the outcome and commits, all
-- in ONE transaction shared with the use case's writes, so a COMMITTED row always
-- has a status_code. request_hash detects a key reused with another body.
CREATE TABLE idempotency_keys (
    route            TEXT        NOT NULL,
    key              TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    status_code      INTEGER,
    response_body    BYTEA,
    response_headers JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ,
    PRIMARY KEY (route, key)
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
