-- Events and cursors (spec §11): one record per chain event identity
-- (§11.3) and one resumable cursor per (network, source) pair (§11.5).

CREATE TABLE IF NOT EXISTS chame_events (
    id                TEXT PRIMARY KEY,
    network_family    TEXT NOT NULL,
    network_name      TEXT NOT NULL,
    network_chain_id  TEXT NOT NULL,
    source_type       TEXT NOT NULL,
    transaction_id    TEXT NOT NULL,
    block_hash        TEXT NOT NULL DEFAULT '',
    block_height      BIGINT NOT NULL,
    event_index       BIGINT NOT NULL DEFAULT 0,
    locator           TEXT NOT NULL DEFAULT '',
    event_type        TEXT NOT NULL,
    data              JSONB,
    decode_version    INT NOT NULL DEFAULT 0,
    observed_at       TIMESTAMPTZ NOT NULL,
    observation       TEXT NOT NULL,
    finality          TEXT NOT NULL,
    processing        TEXT NOT NULL,
    attempts          INT NOT NULL DEFAULT 0,
    next_attempt_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL
);

-- Delivery scans walk the queue in chain order, scoped per source.
CREATE INDEX IF NOT EXISTS chame_events_pending
    ON chame_events (network_family, network_name, network_chain_id, source_type, block_height, event_index)
    WHERE processing = 'queued';

-- Reorg invalidation sweeps the queued suffix of one source.
CREATE INDEX IF NOT EXISTS chame_events_reorg
    ON chame_events (network_family, network_name, network_chain_id, source_type, block_height)
    WHERE processing = 'queued';

CREATE TABLE IF NOT EXISTS chame_event_cursors (
    network_family    TEXT NOT NULL,
    network_name      TEXT NOT NULL,
    network_chain_id  TEXT NOT NULL,
    source_type       TEXT NOT NULL,
    block_height      BIGINT NOT NULL,
    block_hash        TEXT NOT NULL,
    rollback_count    INT NOT NULL DEFAULT 0,
    last_rollback     TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (network_family, network_name, network_chain_id, source_type)
);
