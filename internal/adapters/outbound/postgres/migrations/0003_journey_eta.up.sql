-- ADR 0013: the day-of ETA window of a package, stored on its journey. It is not
-- a scan. The three columns are null together; a window has a positive width.
ALTER TABLE package_journeys
    ADD COLUMN eta_start       TIMESTAMPTZ NULL,
    ADD COLUMN eta_end         TIMESTAMPTZ NULL,
    ADD COLUMN eta_assigned_at TIMESTAMPTZ NULL,
    ADD CONSTRAINT package_journeys_eta_check CHECK (
        (eta_start IS NULL AND eta_end IS NULL AND eta_assigned_at IS NULL)
        OR (eta_start IS NOT NULL AND eta_end IS NOT NULL AND eta_assigned_at IS NOT NULL AND eta_start < eta_end));
