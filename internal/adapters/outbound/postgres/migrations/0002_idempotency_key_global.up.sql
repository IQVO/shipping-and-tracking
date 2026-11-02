-- REST guideline (ADR 0010): the Idempotency-Key is GLOBAL, so the same key on
-- another route is a reuse (409), not a new key. Rows live 24 h; where a key was
-- already used on two routes the oldest row is kept so the new primary key holds.
DELETE FROM idempotency_keys a
USING idempotency_keys b
WHERE a.key = b.key AND (a.created_at, a.route) > (b.created_at, b.route);

ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (key);
