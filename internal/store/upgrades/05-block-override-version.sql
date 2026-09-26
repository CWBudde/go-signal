-- v5: Storage service version of block overrides
-- storage_version is the storage service's manifest version when the block or unblock was made;
-- once a later version is seen, the phone has written the storage service since and wins.
-- 0 = unknown (made before v5).
ALTER TABLE gosignal_block_overrides ADD COLUMN storage_version BIGINT NOT NULL DEFAULT 0;
