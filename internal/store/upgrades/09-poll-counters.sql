-- v9: Account-local durable poll vote high-water counters
CREATE TABLE gosignal_poll_counters (
    chat TEXT NOT NULL,
    author TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    counter INTEGER NOT NULL CHECK (counter BETWEEN 1 AND 4294967295),
    PRIMARY KEY (chat, author, timestamp)
);
