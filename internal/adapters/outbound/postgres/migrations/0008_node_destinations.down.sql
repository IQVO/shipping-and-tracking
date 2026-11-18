-- 0008 down: the NodeDestinationView read model (ADR 0018). The rows are a
-- projection of node-topology's NodeRegistered; nothing else is lost.
DROP TABLE IF EXISTS node_destinations;
