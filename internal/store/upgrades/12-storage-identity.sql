-- v12: Storage identity observations and local decision protection
CREATE TABLE gosignal_storage_identities (
    service_id TEXT PRIMARY KEY,
    remote_key BLOB,
    remote_trust TEXT NOT NULL DEFAULT '',
    local_key BLOB,
    local_trust TEXT NOT NULL DEFAULT '',
    dirty BOOLEAN NOT NULL DEFAULT false
);
-- Protect pre-existing queued decisions, including decisions made before this migration.
INSERT INTO gosignal_storage_identities (service_id, dirty)
SELECT service_id, true FROM gosignal_identity_sync;
