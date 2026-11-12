-- ADR 0014: the delivery-point flag of a package, stored on its journey. Like
-- the ETA window it is not a scan. The two columns are null together and the
-- reason is one of the two the producer publishes.
ALTER TABLE package_journeys
    ADD COLUMN point_flag_reason text NULL,
    ADD COLUMN point_flagged_at  TIMESTAMPTZ NULL,
    ADD CONSTRAINT package_journeys_point_flag_check CHECK (
        (point_flag_reason IS NULL AND point_flagged_at IS NULL)
        OR (point_flag_reason IS NOT NULL AND point_flag_reason IN ('NOT_HOME', 'ACCESS_PROBLEM')
            AND point_flagged_at IS NOT NULL));
