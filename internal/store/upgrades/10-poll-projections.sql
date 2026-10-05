-- v10: Durable poll evidence and materialized account-local projections
CREATE TABLE gosignal_poll_evidence (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat TEXT NOT NULL,
    author TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    hash TEXT NOT NULL,
    event TEXT NOT NULL,
    UNIQUE (chat, author, timestamp, hash)
);
CREATE INDEX gosignal_poll_evidence_lookup ON gosignal_poll_evidence (chat, author, timestamp, id);
CREATE TABLE gosignal_poll_projections (
    chat TEXT NOT NULL,
    author TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    state TEXT NOT NULL,
    PRIMARY KEY (chat, author, timestamp)
);
