-- v3: Pending block and unblock overrides
CREATE TABLE gosignal_block_overrides (
    aci     TEXT PRIMARY KEY,
    blocked BOOLEAN NOT NULL,
    set_at  BIGINT NOT NULL
);
