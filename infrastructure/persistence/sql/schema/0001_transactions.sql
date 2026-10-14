-- Transaction records (spec §10.2, §10.5). One row per
-- transaction.Transaction; the idempotency uniqueness of (scope, key)
-- is enforced by the partial unique index, keyless rows excluded.
CREATE TABLE chame_transactions (
	id                          TEXT PRIMARY KEY,
	operation_id                TEXT NOT NULL,
	network_family              TEXT NOT NULL,
	network_name                TEXT NOT NULL,
	network_chain_id            TEXT NOT NULL,
	status                      TEXT NOT NULL,
	idempotency_application     TEXT NOT NULL DEFAULT '',
	idempotency_operation_type  TEXT NOT NULL DEFAULT '',
	idempotency_key             TEXT NOT NULL DEFAULT '',
	request_hash                TEXT NOT NULL DEFAULT '',
	sender_family               TEXT NOT NULL DEFAULT '',
	sender_value                TEXT NOT NULL DEFAULT '',
	recipient_family            TEXT NOT NULL DEFAULT '',
	recipient_value             TEXT NOT NULL DEFAULT '',
	amount_raw                  TEXT NOT NULL DEFAULT '',
	amount_decimals             INT NOT NULL DEFAULT 0,
	amount_asset                TEXT NOT NULL DEFAULT '',
	chain_hash                  TEXT NOT NULL DEFAULT '',
	chain_data                  JSONB,
	created_at                  TIMESTAMPTZ NOT NULL,
	updated_at                  TIMESTAMPTZ NOT NULL,
	lease_holder                TEXT NOT NULL DEFAULT '',
	lease_expires_at            TIMESTAMPTZ
);

CREATE UNIQUE INDEX chame_transactions_idempotency_key
	ON chame_transactions (idempotency_application, idempotency_operation_type, idempotency_key)
	WHERE idempotency_key <> '';

-- Recovery scan (spec §10.4): filter by stuck status and staleness,
-- oldest first.
CREATE INDEX chame_transactions_stale
	ON chame_transactions (status, updated_at);
