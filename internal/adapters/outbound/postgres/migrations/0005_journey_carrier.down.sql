-- Down: drop the carrier choice and the WITH_CARRIER state.

DROP INDEX idx_package_journeys_stale;
CREATE INDEX idx_package_journeys_stale ON package_journeys (last_scan_at, tracking_number)
    WHERE state IN ('HANDED_OVER', 'IN_TRANSIT', 'AT_STATION', 'OUT_FOR_DELIVERY');

ALTER TABLE package_journeys DROP CONSTRAINT journey_carrier_choice_all_or_none;
ALTER TABLE package_journeys
    DROP COLUMN area_version,
    DROP COLUMN handoff_node_id,
    DROP COLUMN chosen_carrier_id;

DELETE FROM journey_scans WHERE scan_type = 'CARRIER_HANDOFF';
UPDATE package_journeys SET state = 'AT_STATION' WHERE state = 'WITH_CARRIER';
ALTER TABLE package_journeys DROP CONSTRAINT package_journeys_state_check;
ALTER TABLE package_journeys ADD CONSTRAINT package_journeys_state_check CHECK (state IN ('LABELLED', 'HANDED_OVER', 'IN_TRANSIT', 'AT_STATION',
                                                                                          'OUT_FOR_DELIVERY', 'DELIVERED', 'RETURNED', 'LOST', 'VOIDED'));

DROP TABLE area_modes;
