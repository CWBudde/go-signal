-- v8: Complete account-local sticker packs (secret keys and verified images)
CREATE TABLE gosignal_sticker_packs (
    pack_id TEXT PRIMARY KEY NOT NULL,
    data BLOB NOT NULL
);
