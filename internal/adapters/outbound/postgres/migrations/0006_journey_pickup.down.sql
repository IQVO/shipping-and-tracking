DROP TABLE IF EXISTS pickup_points;
-- 0006 down: pickup collection (ADR 0016).
DROP INDEX idx_package_journeys_uncollected;
ALTER TABLE package_journeys
    DROP CONSTRAINT journey_pickup_all_or_none,
    DROP COLUMN destination_kind,
    DROP COLUMN pickup_point_id,
    DROP COLUMN ready_for_collection_at,
    DROP COLUMN hold_until,
    DROP COLUMN collected_at;
ALTER TABLE package_journeys DROP CONSTRAINT package_journeys_state_check;
ALTER TABLE package_journeys ADD CONSTRAINT package_journeys_state_check CHECK (state IN ('LABELLED', 'HANDED_OVER', 'IN_TRANSIT', 'AT_STATION', 'WITH_CARRIER',
                                                                                          'OUT_FOR_DELIVERY', 'DELIVERED', 'RETURNED', 'LOST', 'VOIDED'));
