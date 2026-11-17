-- Label purpose and the return-authorization link (ADR 0017).
--
-- Every existing row is an outbound label: purpose defaults to OUTBOUND and
-- return_authorization_id stays NULL. A RETURN label carries no purchase order
-- and no assignment (both empty), so the old UNIQUE (po_number, package_no)
-- constraint would collide two return labels of different authorizations; it
-- becomes two partial unique indexes - the outbound rule unchanged, the return
-- rule one label per (return_authorization_id, package_no).

ALTER TABLE shipping_labels
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'OUTBOUND'
        CONSTRAINT shipping_labels_purpose_check CHECK (purpose IN ('OUTBOUND', 'RETURN')),
    ADD COLUMN return_authorization_id TEXT NULL;

ALTER TABLE shipping_labels
    ADD CONSTRAINT shipping_labels_purpose_key
        CHECK ((purpose = 'RETURN') = (return_authorization_id IS NOT NULL));

ALTER TABLE shipping_labels DROP CONSTRAINT shipping_labels_package_key;

CREATE UNIQUE INDEX shipping_labels_package_key
    ON shipping_labels (po_number, package_no) WHERE purpose = 'OUTBOUND';

CREATE UNIQUE INDEX shipping_labels_return_package_key
    ON shipping_labels (return_authorization_id, package_no) WHERE purpose = 'RETURN';
