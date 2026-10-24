# Java storage-key derivation vectors

Generated offline from signal-cli's key models (models-jvm 2.15.3_unofficial_154) with the artifacts recorded in provenance.json. All inputs are invented and deterministic, so regeneration must reproduce vectors.json byte for byte. Binary fields use padded base64. Cases follow SignalAccount.getOrCreateStorageKey: a stored derived storageKey is used as is, a pinMasterKey and an accountEntropyPool are derived.
