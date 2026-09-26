-- v2: Group title cache and groups we left
CREATE TABLE gosignal_groups (
    group_id   TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    revision   INTEGER NOT NULL,
    left_at    INTEGER,
    updated_at INTEGER NOT NULL
);
