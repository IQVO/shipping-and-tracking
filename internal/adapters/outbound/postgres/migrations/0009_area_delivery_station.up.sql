-- ADR 0019: the delivery station of an area. node-topology's ServiceAreaAssigned
-- carries deliveryStationNodeId; this context used to read and drop it and now
-- keeps it, to publish it as destinationStationNodeId on LabelIssued.
--
-- NULL = no station known: the row predates this migration, or the event named
-- none. The column is NOT backfilled here: the next ServiceAreaAssigned of the
-- prefix (a replay, or a re-emit at the same assignmentVersion) fills it - the
-- version guard lets an equal version write when the stored station IS NULL.
ALTER TABLE area_modes ADD COLUMN delivery_station_node_id TEXT NULL;
