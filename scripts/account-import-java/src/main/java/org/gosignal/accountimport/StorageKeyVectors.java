package org.gosignal.accountimport;

import java.nio.file.*;
import java.security.MessageDigest;
import java.util.*;
import org.signal.core.models.AccountEntropyPool;
import org.signal.core.models.MasterKey;
import org.signal.core.models.storageservice.StorageKey;

/**
 * Storage-key derivation vectors from signal-cli's own key models (models-jvm, signal-service
 * 2.15.3_unofficial_154). The three cases mirror SignalAccount.getOrCreateStorageKey's sources:
 * a stored derived storageKey (used as is), a pinMasterKey and an accountEntropyPool.
 */
final class StorageKeyVectors {
    // Invented inputs: 64 characters of the pool alphabet, and fixed byte ramps.
    static final String ENTROPY_POOL = "gosignalstoragekeyvectors0123456789abcdefghijklmnopqrstuvwxyz012";
    static final long MANIFEST_VERSION = 7;

    public record Case(String name, String accountEntropyPool, byte[] masterKey, byte[] storageKey,
                       long manifestVersion, byte[] manifestKey, byte[] itemID, byte[] itemKey) {}
    public record Vectors(int version, List<Case> cases) {}

    private StorageKeyVectors() {}

    static byte[] ramp(int start) {
        var b = new byte[32];
        for (int i = 0; i < b.length; i++) b[i] = (byte) (start + i);
        return b;
    }

    static Case derived(String name, String pool, MasterKey master, StorageKey storage) {
        var itemID = Arrays.copyOf(ramp(0x40), 16);
        return new Case(name, pool, master == null ? null : master.serialize(), storage.serialize(),
            MANIFEST_VERSION, storage.deriveManifestKey(MANIFEST_VERSION).serialize(), itemID,
            storage.deriveItemKey(itemID).serialize());
    }

    static Vectors vectors() {
        if (ENTROPY_POOL.length() != AccountEntropyPool.LENGTH) throw new IllegalStateException("invalid pool length");
        var pool = new AccountEntropyPool(ENTROPY_POOL);
        var poolMaster = pool.deriveMasterKey();
        var pinMaster = new MasterKey(ramp(0x00));
        var stored = new StorageKey(ramp(0xa0));
        return new Vectors(1, List.of(
            derived("accountEntropyPool", ENTROPY_POOL, poolMaster, poolMaster.deriveStorageServiceKey()),
            derived("pinMasterKey", null, pinMaster, pinMaster.deriveStorageServiceKey()),
            derived("derivedStorageKey", null, null, stored)));
    }

    static void generate(Path output) throws Exception {
        var provenance = ImportFixtures.verifiedProvenance();
        Files.createDirectory(output);
        var file = output.resolve("vectors.json");
        ImportFixtures.JSON.writeValue(file.toFile(), vectors());
        var digest = MessageDigest.getInstance("SHA-256").digest(Files.readAllBytes(file));
        provenance.set("fixtureSHA256", ImportFixtures.JSON.valueToTree(Map.of("vectors.json", HexFormat.of().formatHex(digest))));
        ImportFixtures.JSON.writeValue(output.resolve("provenance.json").toFile(), provenance);
        Files.writeString(output.resolve("README.md"), "# Java storage-key derivation vectors\n\nGenerated offline from signal-cli's key models (models-jvm 2.15.3_unofficial_154) with the artifacts recorded in provenance.json. All inputs are invented and deterministic, so regeneration must reproduce vectors.json byte for byte. Binary fields use padded base64. Cases follow SignalAccount.getOrCreateStorageKey: a stored derived storageKey is used as is, a pinMasterKey and an accountEntropyPool are derived.\n");
    }
}
