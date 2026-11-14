-- C6 pickup collection (ADR 0016): a pickup journey names its point, becomes
-- READY_FOR_COLLECTION when the wave deposits the package there, and COLLECTED
-- when the shopper collects it. The uncollected sweep reads hold_until.

ALTER TABLE package_journeys DROP CONSTRAINT package_journeys_state_check;
ALTER TABLE package_journeys ADD CONSTRAINT package_journeys_state_check CHECK (state IN ('LABELLED', 'HANDED_OVER', 'IN_TRANSIT', 'AT_STATION', 'WITH_CARRIER',
                                                                                          'OUT_FOR_DELIVERY', 'READY_FOR_COLLECTION', 'DELIVERED', 'COLLECTED',
                                                                                          'RETURNED', 'LOST', 'VOIDED'));

ALTER TABLE package_journeys
    ADD COLUMN destination_kind         TEXT NOT NULL DEFAULT 'SHIP_TO'
        CHECK (destination_kind IN ('SHIP_TO', 'PICKUP_POINT')),
    ADD COLUMN pickup_point_id          TEXT,
    ADD COLUMN ready_for_collection_at  TIMESTAMPTZ,
    ADD COLUMN hold_until               TIMESTAMPTZ,
    ADD COLUMN collected_at             TIMESTAMPTZ,
    ADD CONSTRAINT journey_pickup_all_or_none CHECK (
        (destination_kind = 'SHIP_TO' AND pickup_point_id IS NULL AND hold_until IS NULL AND ready_for_collection_at IS NULL AND collected_at IS NULL)
        OR (destination_kind = 'PICKUP_POINT' AND pickup_point_id IS NOT NULL AND hold_until IS NOT NULL));

-- The uncollected sweep scans the waiting pickup journeys by hold deadline.
CREATE INDEX idx_package_journeys_uncollected ON package_journeys (hold_until, tracking_number)
    WHERE state = 'READY_FOR_COLLECTION';

-- The PickupPointView read model (ADR 0016): node-topology's pickup points
-- folded locally; a RETIRED point is deleted from it.
CREATE TABLE pickup_points (
    id              TEXT PRIMARY KEY,
    station_node_id TEXT NOT NULL,
    postal_prefix   TEXT NOT NULL,
    state           TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
