package org.gosignal.accountimport;

import java.nio.file.*;
import java.security.MessageDigest;
import java.util.*;
import okio.ByteString;
import org.whispersystems.signalservice.api.storage.SignalAccountRecord;
import org.whispersystems.signalservice.internal.storage.protos.AccountRecord;

/**
 * AccountRecord overlay vectors: signal-cli's StorageSyncModels.localToRemoteRecord (v0.14.9,
 * StorageSyncModels.java:60-94) applied to an invented cached self storage_record. signal-cli's
 * account loader is not run, so the builder calls are restated here; the record decoding,
 * construction and encoding are signal-service-java's own Wire AccountRecord (2.15.3_unofficial_154).
 * Configuration values are null when the ConfigurationStore has no row.
 */
final class AccountOverlayVectors {
    public record Config(Boolean readReceipts, Boolean typingIndicators, Boolean unidentifiedDeliveryIndicators,
                         Boolean linkPreviews, Boolean phoneNumberUnlisted, String phoneNumberSharingMode) {}
    public record Case(String name, Config config, byte[] selfProfileKey, byte[] accountRecord) {}
    public record Vectors(int version, byte[] cachedRecord, List<Case> cases) {}

    private AccountOverlayVectors() {}

    static byte[] cachedRecord() {
        return new AccountRecord.Builder()
            .profileKey(ByteString.of(StorageKeyVectors.ramp(0x10)))
            .givenName("Invented")
            .readReceipts(true)
            .typingIndicators(false)
            .sealedSenderIndicators(false)
            .linkPreviews(false)
            .unlistedPhoneNumber(true)
            .phoneNumberSharingMode(AccountRecord.PhoneNumberSharingMode.EVERYBODY)
            .username("invented.01")
            .build()
            .encode();
    }

    // StorageSyncModels.localToRemote: CONTACTS has no remote value of its own.
    static AccountRecord.PhoneNumberSharingMode localToRemote(String mode) {
        if (mode == null) return AccountRecord.PhoneNumberSharingMode.UNKNOWN;
        return switch (mode) {
            case "EVERYBODY" -> AccountRecord.PhoneNumberSharingMode.EVERYBODY;
            case "CONTACTS", "NOBODY" -> AccountRecord.PhoneNumberSharingMode.NOBODY;
            default -> throw new IllegalArgumentException("unknown sharing mode " + mode);
        };
    }

    static byte[] overlay(byte[] cached, Config config, byte[] selfProfileKey) {
        var builder = SignalAccountRecord.Companion.newBuilder(cached);
        if (selfProfileKey != null) builder.profileKey(ByteString.of(selfProfileKey));
        // The self recipient has no profile and no username in these vectors.
        builder.typingIndicators(Optional.ofNullable(config.typingIndicators()).orElse(true))
            .readReceipts(Optional.ofNullable(config.readReceipts()).orElse(true))
            .sealedSenderIndicators(Optional.ofNullable(config.unidentifiedDeliveryIndicators()).orElse(true))
            .linkPreviews(Optional.ofNullable(config.linkPreviews()).orElse(true))
            .unlistedPhoneNumber(Optional.ofNullable(config.phoneNumberUnlisted()).orElse(false))
            .phoneNumberSharingMode(localToRemote(config.phoneNumberSharingMode()))
            .username("");
        return builder.build().encode();
    }

    static Vectors vectors() {
        var cached = cachedRecord();
        var self = StorageKeyVectors.ramp(0x20);
        var cases = new ArrayList<Case>();
        for (var c : List.of(
            Map.entry("allAbsent", new Config(null, null, null, null, null, null)),
            Map.entry("allFalse", new Config(false, false, false, false, false, "NOBODY")),
            Map.entry("allTrue", new Config(true, true, true, true, true, "EVERYBODY")),
            Map.entry("contactsSharing", new Config(true, true, true, true, false, "CONTACTS")),
            Map.entry("readReceiptsOnly", new Config(false, null, null, null, null, null)))) {
            cases.add(new Case(c.getKey(), c.getValue(), self, overlay(cached, c.getValue(), self)));
        }
        cases.add(new Case("noSelfProfileKey", new Config(null, null, null, null, null, null), null,
            overlay(cached, new Config(null, null, null, null, null, null), null)));
        return new Vectors(1, cached, cases);
    }

    static void generate(Path output) throws Exception {
        var provenance = ImportFixtures.verifiedProvenance();
        Files.createDirectory(output);
        var file = output.resolve("vectors.json");
        ImportFixtures.JSON.writeValue(file.toFile(), vectors());
        var digest = MessageDigest.getInstance("SHA-256").digest(Files.readAllBytes(file));
        provenance.set("fixtureSHA256", ImportFixtures.JSON.valueToTree(Map.of("vectors.json", HexFormat.of().formatHex(digest))));
        provenance.put("overlay", "reimplementation of StorageSyncModels.localToRemoteRecord builder calls (signal-cli v0.14.9) on signal-service-java's Wire AccountRecord");
        ImportFixtures.JSON.writeValue(output.resolve("provenance.json").toFile(), provenance);
        Files.writeString(output.resolve("README.md"), "# Java AccountRecord overlay vectors\n\nGenerated offline with the artifacts recorded in provenance.json. Each case applies the builder calls of signal-cli v0.14.9's StorageSyncModels.localToRemoteRecord to the invented cached self storage_record (`cachedRecord`): the self profile key, then the ConfigurationStore values with their defaults (true for receipts, typing, sealed-sender indicators and link previews, false for an unlisted number, UNKNOWN for phone-number sharing; CONTACTS and NOBODY both become NOBODY). signal-cli's account loader is not run, so those calls are restated, but decoding, building and encoding use signal-service-java's own Wire AccountRecord. A null configuration value means the source has no SQL row. Binary fields use padded base64; regeneration must reproduce vectors.json byte for byte.\n");
    }
}
