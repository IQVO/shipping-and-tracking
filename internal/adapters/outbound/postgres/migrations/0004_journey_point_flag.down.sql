-- ADR 0014 down: drop the delivery-point flag columns (the flag is lost; the
-- journey and its scans stay).
ALTER TABLE package_journeys
    DROP CONSTRAINT IF EXISTS package_journeys_point_flag_check,
    DROP COLUMN IF EXISTS point_flag_reason,
    DROP COLUMN IF EXISTS point_flagged_at;
