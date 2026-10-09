-- v11: Durable outgoing ACI verification decisions
CREATE TABLE gosignal_identity_sync (
    service_id TEXT PRIMARY KEY,
    identity_key BLOB NOT NULL,
    trust TEXT NOT NULL,
    token TEXT NOT NULL
);
