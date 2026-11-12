ALTER TABLE package_journeys
    DROP CONSTRAINT package_journeys_eta_check,
    DROP COLUMN eta_assigned_at,
    DROP COLUMN eta_end,
    DROP COLUMN eta_start;
