-- Reverse of 0007: drops the purpose columns and restores the original
-- UNIQUE (po_number, package_no) constraint. Return labels (empty po_number)
-- must be gone before this runs, or the constraint will reject them.

DROP INDEX IF EXISTS shipping_labels_return_package_key;
DROP INDEX IF EXISTS shipping_labels_package_key;

ALTER TABLE shipping_labels
    ADD CONSTRAINT shipping_labels_package_key UNIQUE (po_number, package_no);

ALTER TABLE shipping_labels
    DROP CONSTRAINT IF EXISTS shipping_labels_purpose_key,
    DROP CONSTRAINT IF EXISTS shipping_labels_purpose_check;

ALTER TABLE shipping_labels
    DROP COLUMN IF EXISTS return_authorization_id,
    DROP COLUMN IF EXISTS purpose;
