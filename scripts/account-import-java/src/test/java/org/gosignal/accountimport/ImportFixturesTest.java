package org.gosignal.accountimport;

import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.UUID;
import org.signal.libsignal.protocol.*;
import org.signal.libsignal.protocol.ecc.ECKeyPair;
import org.signal.libsignal.protocol.kem.*;
import org.signal.libsignal.protocol.message.*;
import org.signal.libsignal.protocol.state.*;

/** Executable offline assertions; run with -ea against the exact fixture artifact. */
public final class ImportFixturesTest {
    public static void main(String[] args) throws Exception {
        if (!ImportFixturesTest.class.desiredAssertionStatus()) throw new IllegalStateException("enable assertions");
        consumption(false);
        consumption(true);
        typedSessionPersistence();
        generatorRefusesExistingDirectory();
        sourceSQLSenderKey();
        System.out.println("Java source-store assertions: PASS");
    }

    static void sourceSQLSenderKey() throws Exception {
        var corpus = ImportFixtures.corpus();
        var source = corpus.accounts().stream().filter(a -> a.id().equals("aci-current-skipped-source")).findFirst().orElseThrow();
        assert !source.stores().get("ACI").senderKeys().isEmpty() : "representative SQL lacks incoming sender key";
        assert ImportFixtures.sqlRows(source).contains("X'66666666666646668666666666666666'") : "UUID BLOB codec not exercised";
    }

    static void generatorRefusesExistingDirectory() throws Exception {
        var existing = java.nio.file.Files.createTempDirectory("import-fixture-existing");
        try {
            ImportFixtures.main(new String[]{"generate", existing.toString()});
            throw new AssertionError("existing output directory accepted");
        } catch (java.nio.file.FileAlreadyExistsException expected) {
            try (var files = java.nio.file.Files.list(existing)) { assert files.findAny().isEmpty(); }
        } finally {
            java.nio.file.Files.delete(existing);
        }
    }

    private static void consumption(boolean lastResort) throws Exception {
        var alice = new SourceProtocolStore(IdentityKeyPair.generate(), 123);
        var bob = new SourceProtocolStore(IdentityKeyPair.generate(), 456);
        var a = new SignalProtocolAddress(new ServiceId.Aci(UUID.fromString("11111111-1111-4111-8111-111111111111")), 2);
        var b = new SignalProtocolAddress(new ServiceId.Aci(UUID.fromString("22222222-2222-4222-8222-222222222222")), 2);
        var ec = ECKeyPair.generate();
        var signed = ECKeyPair.generate();
        var kyber = KEMKeyPair.generate(KEMKeyType.KYBER_1024);
        var signedSig = bob.getIdentityKeyPair().getPrivateKey().calculateSignature(signed.getPublicKey().serialize());
        var kyberSig = bob.getIdentityKeyPair().getPrivateKey().calculateSignature(kyber.getPublicKey().serialize());
        bob.storePreKey(0, new PreKeyRecord(0, ec));
        bob.storeSignedPreKey(2, new SignedPreKeyRecord(2, 1_700_000_000_000L, signed, signedSig));
        bob.storeKyberPreKey(3, new KyberPreKeyRecord(3, 1_700_000_000_000L, kyber, kyberSig));
        bob.setLastResort(3, lastResort);
        new SessionBuilder(alice, b, a).process(new PreKeyBundle(456, 2, 0, ec.getPublicKey(), 2, signed.getPublicKey(), signedSig, bob.getIdentityKeyPair().getPublicKey(), 3, kyber.getPublicKey(), kyberSig));
        var receiver = new SessionCipher(bob, b, a);
        var message = new SessionCipher(alice, a, b).encrypt("fixture plaintext".getBytes(StandardCharsets.UTF_8));
        byte[] bad = message.serialize().clone();
        bad[bad.length - 1] ^= 1;
        try {
            receiver.decrypt(new PreKeySignalMessage(bad));
            throw new AssertionError("tampered message accepted");
        } catch (InvalidMessageException expected) {
            assert bob.containsPreKey(0) && bob.containsKyberPreKey(3);
        }
        assert Arrays.equals(receiver.decrypt(new PreKeySignalMessage(message.serialize())), "fixture plaintext".getBytes(StandardCharsets.UTF_8));
        assert !bob.containsPreKey(0);
        assert bob.containsKyberPreKey(3) == lastResort;
        assert bob.containsSignedPreKey(2);
        var reopened = SourceProtocolStore.restore(bob.snapshot());
        assert reopened.containsKyberPreKey(3) == lastResort;
        assert Arrays.equals(reopened.loadSession(a).serialize(), bob.loadSession(a).serialize());
    }

    private static void typedSessionPersistence() throws Exception {
        var store = new SourceProtocolStore(IdentityKeyPair.generate(), 123);
        var id = UUID.fromString("33333333-3333-4333-8333-333333333333");
        var aci = new SignalProtocolAddress(new ServiceId.Aci(id), 2);
        var pni = new SignalProtocolAddress(new ServiceId.Pni(id), 2);
        store.storeSession(aci, new SessionRecord());
        assert store.containsSession(aci);
        assert !store.containsSession(pni);
        var reopened = SourceProtocolStore.restore(store.snapshot());
        assert reopened.containsSession(aci) && !reopened.containsSession(pni);
    }
}
