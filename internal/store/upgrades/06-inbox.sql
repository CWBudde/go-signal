-- v6: Inbox of `mcp serve`
-- The events the MCP server received, until MCP clients read them. id is the clients' cursor;
-- AUTOINCREMENT keeps it from being reused after pruning. chat is signal.Chat.Key(); sender and
-- timestamp identify messages for read marks (empty and 0 for other events). event is the
-- event as JSON (see signal.marshalEvent). Times are ms since the epoch.
CREATE TABLE gosignal_inbox (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    received_at BIGINT NOT NULL,
    time        BIGINT NOT NULL,
    chat        TEXT NOT NULL,
    sender      TEXT NOT NULL,
    timestamp   BIGINT NOT NULL,
    unread      BOOLEAN NOT NULL,
    event       TEXT NOT NULL
);
CREATE INDEX gosignal_inbox_chat ON gosignal_inbox (chat, id);
CREATE INDEX gosignal_inbox_message ON gosignal_inbox (sender, timestamp);
CREATE INDEX gosignal_inbox_received ON gosignal_inbox (received_at);
