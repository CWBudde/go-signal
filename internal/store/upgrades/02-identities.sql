-- v2: Trust state of identity keys
-- One row per user whose identity key go-signal has seen, keyed like signalmeow_identity_keys
-- (their_service_id). previous_key is the last trusted key before a change; pending_event marks
-- a change that hasn't been reported as an event yet. Times are ms since the epoch, 0 = unknown.
CREATE TABLE gosignal_identities (
    service_id    TEXT PRIMARY KEY,
    identity_key  BLOB NOT NULL,
    trust         TEXT NOT NULL,
    first_seen    BIGINT NOT NULL DEFAULT 0,
    changed_at    BIGINT NOT NULL DEFAULT 0,
    previous_key  BLOB,
    pending_event BOOLEAN NOT NULL DEFAULT false
);
