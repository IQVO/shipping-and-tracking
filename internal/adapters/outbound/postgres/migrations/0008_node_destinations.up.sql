-- The NodeDestinationView read model (ADR 0018): what a label needs about a
-- node of node-topology, whatever its type - the node id and the destination
-- prefix this context derives from the node's registered postal code (the
-- first five characters from A-Z 0-9; ADR 0002 A10). The postal code itself
-- is not stored. An empty prefix means the node is known and its code left
-- nothing usable. No state column and no delete: the node's lifecycle facts
-- never reach this table, so they can neither demote nor erase a row.
--
-- The table starts EMPTY: the NodeRegistered events consumed before this
-- migration fed only pickup_points. docs/docs/operations/runbook.md
-- ("Rebuilding the node destination view") is the rebuild by replay.
CREATE TABLE node_destinations (
    node_id       TEXT PRIMARY KEY CHECK (node_id <> ''),
    postal_prefix TEXT NOT NULL CHECK (postal_prefix ~ '^[A-Z0-9]{0,5}$'),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
