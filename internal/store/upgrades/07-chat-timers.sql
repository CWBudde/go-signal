-- v7: Account-local direct-chat disappearing timers
CREATE TABLE gosignal_chat_timers (
    aci     TEXT PRIMARY KEY NOT NULL,
    seconds INTEGER NOT NULL CHECK (seconds BETWEEN 0 AND 4294967295),
    version INTEGER NOT NULL CHECK (version BETWEEN 0 AND 4294967295)
);
