-- Read-only extraction of current createDatabase/createSql DDL.
-- Java source is not run; whitespace normalized from Java textblocks only.
-- PRAGMA user_version is appended from Database.initDb/setUserVersion, not createSql.
-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/recipients/RecipientStore.java:68 (RecipientStore.createSql)
CREATE TABLE recipient (
  _id INTEGER PRIMARY KEY AUTOINCREMENT,
  storage_id BLOB UNIQUE,
  storage_record BLOB,
  number TEXT UNIQUE,
  username TEXT UNIQUE,
  aci TEXT UNIQUE,
  pni TEXT UNIQUE,
  unregistered_timestamp INTEGER,
  discoverable INTEGER,
  profile_key BLOB,
  profile_key_credential BLOB,
  needs_pni_signature INTEGER NOT NULL DEFAULT FALSE,
  pni_signature_verified INTEGER NOT NULL DEFAULT FALSE,

  given_name TEXT,
  family_name TEXT,
  nick_name TEXT,
  nick_name_given_name TEXT,
  nick_name_family_name TEXT,
  note TEXT,
  color TEXT,

  expiration_time INTEGER NOT NULL DEFAULT 0,
  expiration_time_version INTEGER DEFAULT 1 NOT NULL,
  mute_until INTEGER NOT NULL DEFAULT 0,
  blocked INTEGER NOT NULL DEFAULT FALSE,
  blocked_at INTEGER NOT NULL DEFAULT 0,
  archived INTEGER NOT NULL DEFAULT FALSE,
  profile_sharing INTEGER NOT NULL DEFAULT FALSE,
  hide_story INTEGER NOT NULL DEFAULT FALSE,
  hidden INTEGER NOT NULL DEFAULT FALSE,

  profile_last_update_timestamp INTEGER NOT NULL DEFAULT 0,
  profile_given_name TEXT,
  profile_family_name TEXT,
  profile_about TEXT,
  profile_about_emoji TEXT,
  profile_avatar_url_path TEXT,
  profile_mobile_coin_address BLOB,
  profile_unidentified_access_mode TEXT,
  profile_capabilities TEXT,
  profile_phone_number_sharing TEXT
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/sendLog/MessageSendLogStore.java:60 (MessageSendLogStore.createSql)
CREATE TABLE message_send_log (
  _id INTEGER PRIMARY KEY,
  content_id INTEGER NOT NULL REFERENCES message_send_log_content (_id) ON DELETE CASCADE,
  address TEXT NOT NULL,
  device_id INTEGER NOT NULL
) STRICT;
CREATE TABLE message_send_log_content (
  _id INTEGER PRIMARY KEY,
  group_id BLOB,
  timestamp INTEGER NOT NULL,
  content BLOB NOT NULL,
  content_hint INTEGER NOT NULL,
  urgent INTEGER NOT NULL
) STRICT;
CREATE INDEX mslc_timestamp_index ON message_send_log_content (timestamp);
CREATE INDEX msl_recipient_index ON message_send_log (address, device_id, content_id);
CREATE INDEX msl_content_index ON message_send_log (content_id);

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/stickers/StickerStore.java:27 (StickerStore.createSql)
CREATE TABLE sticker (
  _id INTEGER PRIMARY KEY,
  pack_id BLOB UNIQUE NOT NULL,
  pack_key BLOB NOT NULL,
  installed INTEGER NOT NULL DEFAULT FALSE,
  position INTEGER NOT NULL DEFAULT 0,
  deleted_timestamp INTEGER NOT NULL DEFAULT 0,
  storage_id BLOB UNIQUE,
  storage_record BLOB
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/prekeys/PreKeyStore.java:29 (PreKeyStore.createSql)
CREATE TABLE pre_key (
  _id INTEGER PRIMARY KEY,
  account_id_type INTEGER NOT NULL,
  key_id INTEGER NOT NULL,
  public_key BLOB NOT NULL,
  private_key BLOB NOT NULL,
  stale_timestamp INTEGER,
  UNIQUE(account_id_type, key_id)
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/prekeys/SignedPreKeyStore.java:32 (SignedPreKeyStore.createSql)
CREATE TABLE signed_pre_key (
  _id INTEGER PRIMARY KEY,
  account_id_type INTEGER NOT NULL,
  key_id INTEGER NOT NULL,
  public_key BLOB NOT NULL,
  private_key BLOB NOT NULL,
  signature BLOB NOT NULL,
  timestamp INTEGER DEFAULT 0,
  UNIQUE(account_id_type, key_id)
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/prekeys/KyberPreKeyStore.java:30 (KyberPreKeyStore.createSql)
CREATE TABLE kyber_pre_key (
  _id INTEGER PRIMARY KEY,
  account_id_type INTEGER NOT NULL,
  key_id INTEGER NOT NULL,
  serialized BLOB NOT NULL,
  is_last_resort INTEGER NOT NULL,
  stale_timestamp INTEGER,
  timestamp INTEGER DEFAULT 0,
  UNIQUE(account_id_type, key_id)
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/groups/GroupStore.java:52 (GroupStore.createSql)
CREATE TABLE group_v2 (
  _id INTEGER PRIMARY KEY,
  storage_id BLOB UNIQUE,
  storage_record BLOB,
  group_id BLOB UNIQUE NOT NULL,
  master_key BLOB NOT NULL,
  group_data BLOB,
  distribution_id BLOB UNIQUE NOT NULL,
  endorsement_expiration_time INTEGER NOT NULL DEFAULT 0,
  blocked INTEGER NOT NULL DEFAULT FALSE,
  blocked_at INTEGER NOT NULL DEFAULT 0,
  profile_sharing INTEGER NOT NULL DEFAULT FALSE,
  permission_denied INTEGER NOT NULL DEFAULT FALSE
) STRICT;
CREATE TABLE group_v2_member (
  _id INTEGER PRIMARY KEY,
  group_id INTEGER NOT NULL REFERENCES group_v2 (_id) ON DELETE CASCADE,
  recipient_id INTEGER NOT NULL REFERENCES recipient (_id) ON DELETE CASCADE,
  endorsement BLOB NOT NULL,
  UNIQUE(group_id, recipient_id)
) STRICT;
CREATE TABLE group_v1 (
  _id INTEGER PRIMARY KEY,
  storage_id BLOB UNIQUE,
  storage_record BLOB,
  group_id BLOB UNIQUE NOT NULL,
  group_id_v2 BLOB UNIQUE,
  name TEXT,
  color TEXT,
  expiration_time INTEGER NOT NULL DEFAULT 0,
  blocked INTEGER NOT NULL DEFAULT FALSE,
  blocked_at INTEGER NOT NULL DEFAULT 0,
  archived INTEGER NOT NULL DEFAULT FALSE
) STRICT;
CREATE TABLE group_v1_member (
  _id INTEGER PRIMARY KEY,
  group_id INTEGER NOT NULL REFERENCES group_v1 (_id) ON DELETE CASCADE,
  recipient_id INTEGER NOT NULL REFERENCES recipient (_id) ON DELETE CASCADE,
  UNIQUE(group_id, recipient_id)
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/sessions/SessionStore.java:44 (SessionStore.createSql)
CREATE TABLE session (
  _id INTEGER PRIMARY KEY,
  account_id_type INTEGER NOT NULL,
  address TEXT NOT NULL,
  device_id INTEGER NOT NULL,
  record BLOB NOT NULL,
  UNIQUE(account_id_type, address, device_id)
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/identities/IdentityKeyStore.java:37 (IdentityKeyStore.createSql)
CREATE TABLE identity (
  _id INTEGER PRIMARY KEY,
  address TEXT UNIQUE NOT NULL,
  identity_key BLOB NOT NULL,
  added_timestamp INTEGER NOT NULL,
  trust_level INTEGER NOT NULL
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/senderKeys/SenderKeyRecordStore.java:28 (SenderKeyRecordStore.createSql)
CREATE TABLE sender_key (
  _id INTEGER PRIMARY KEY,
  address TEXT NOT NULL,
  device_id INTEGER NOT NULL,
  distribution_id BLOB NOT NULL,
  record BLOB NOT NULL,
  created_timestamp INTEGER NOT NULL,
  UNIQUE(address, device_id, distribution_id)
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/senderKeys/SenderKeySharedStore.java:27 (SenderKeySharedStore.createSql)
CREATE TABLE sender_key_shared (
  _id INTEGER PRIMARY KEY,
  address TEXT NOT NULL,
  device_id INTEGER NOT NULL,
  distribution_id BLOB NOT NULL,
  timestamp INTEGER NOT NULL,
  UNIQUE(address, device_id, distribution_id)
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/keyValue/KeyValueStore.java:24 (KeyValueStore.createSql)
CREATE TABLE key_value (
  _id INTEGER PRIMARY KEY,
  key TEXT UNIQUE NOT NULL,
  value ANY
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/recipients/CdsiStore.java:18 (CdsiStore.createSql)
CREATE TABLE cdsi (
  _id INTEGER PRIMARY KEY,
  number TEXT NOT NULL UNIQUE,
  last_seen_at INTEGER NOT NULL
) STRICT;

-- reference/signal-cli/lib/src/main/java/org/asamk/signal/manager/storage/UnknownStorageIdStore.java:17 (UnknownStorageIdStore.createSql)
CREATE TABLE storage_id (
  _id INTEGER PRIMARY KEY,
  type INTEGER NOT NULL,
  storage_id BLOB UNIQUE NOT NULL
) STRICT;

-- Database.java:initDb/setUserVersion; AccountDatabase.java:DATABASE_VERSION
PRAGMA user_version = 31;
