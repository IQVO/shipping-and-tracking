-- C4 carrier handoff (ADR 0015): the journey stores the deterministic carrier
-- choice computed at the station (all three null or all three set), and gains
-- the WITH_CARRIER state.

ALTER TABLE package_journeys DROP CONSTRAINT package_journeys_state_check;
ALTER TABLE package_journeys ADD CONSTRAINT package_journeys_state_check CHECK (state IN ('LABELLED', 'HANDED_OVER', 'IN_TRANSIT', 'AT_STATION', 'WITH_CARRIER',
                                                                                          'OUT_FOR_DELIVERY', 'DELIVERED', 'RETURNED', 'LOST', 'VOIDED'));

ALTER TABLE package_journeys
    ADD COLUMN chosen_carrier_id TEXT,
    ADD COLUMN handoff_node_id   TEXT,
    ADD COLUMN area_version      BIGINT,
    ADD CONSTRAINT journey_carrier_choice_all_or_none CHECK (
        (chosen_carrier_id IS NULL AND handoff_node_id IS NULL AND area_version IS NULL)
        OR (chosen_carrier_id IS NOT NULL AND handoff_node_id IS NOT NULL AND area_version IS NOT NULL));

-- WITH_CARRIER is silence-watched with the tier window, so the stale-sweep
-- partial index must cover it too.
DROP INDEX idx_package_journeys_stale;
CREATE INDEX idx_package_journeys_stale ON package_journeys (last_scan_at, tracking_number)
    WHERE state IN ('HANDED_OVER', 'IN_TRANSIT', 'AT_STATION', 'WITH_CARRIER', 'OUT_FOR_DELIVERY');

-- The local area read model (ADR 0015): node-topology's ServiceAreaAssigned
-- folded per postal prefix. carrier_options mirrors the declared list; an
-- empty list with delivery_mode NETWORK_ROUTE is the pre-C4 meaning.
CREATE TABLE area_modes (
    postal_prefix      TEXT        PRIMARY KEY,
    delivery_mode      TEXT        NOT NULL DEFAULT 'NETWORK_ROUTE' CHECK (delivery_mode IN ('NETWORK_ROUTE', 'CARRIER_HANDOFF')),
    carrier_options    JSONB       NOT NULL DEFAULT '[]',
    assignment_version BIGINT      NOT NULL CHECK (assignment_version >= 1),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
