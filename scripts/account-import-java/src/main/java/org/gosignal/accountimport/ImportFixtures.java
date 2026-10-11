package org.gosignal.accountimport;

import com.fasterxml.jackson.databind.*;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.MessageDigest;
import java.util.*;
import org.signal.core.util.UuidUtil;
import org.signal.libsignal.protocol.*;
import org.signal.libsignal.protocol.ecc.ECKeyPair;
import org.signal.libsignal.protocol.groups.*;
import org.signal.libsignal.protocol.kem.*;
import org.signal.libsignal.protocol.message.*;
import org.signal.libsignal.protocol.state.*;

/** Offline synthetic fixture generation and retained-Java-peer continuation. */
public final class ImportFixtures {
    static final ObjectMapper JSON = new ObjectMapper().enable(SerializationFeature.INDENT_OUTPUT);
    static final long KEY_TIMESTAMP_MS = 1_700_000_000_000L;
    static final String ALICE_ACI = "11111111-1111-4111-8111-111111111111";
    static final String BOB_ACI = "22222222-2222-4222-8222-222222222222";
    static final String ALICE_PNI = "33333333-3333-4333-8333-333333333333";
    static final String BOB_PNI = "44444444-4444-4444-8444-444444444444";
    public record SID(String type, String uuid) {
        SignalProtocolAddress address() {
            if (!type.equals("ACI") && !type.equals("PNI")) throw new IllegalArgumentException("unsupported service ID kind");
            var id = UUID.fromString(uuid);
            return new SignalProtocolAddress(type.equals("ACI") ? new ServiceId.Aci(id) : new ServiceId.Pni(id), 2);
        }
        String text() { return address().getName(); }
    }
    public record ECRow(int id, byte[] publicKey, byte[] privateKey) {}
    public record SignedRow(int id, byte[] publicKey, byte[] privateKey, byte[] signature, long timestampMilliseconds) {}
    public record KyberRow(int id, byte[] serialized, boolean lastResort, long timestampMilliseconds) {}
    public record SourceRows(byte[] identityPublic, byte[] identityPrivate, List<ECRow> preKeys,
                             List<SignedRow> signedPreKeys, List<KyberRow> kyberPreKeys) {}
    public record Account(String id, String aci, String pni, int deviceID, String namespace,
                          Map<String, SourceProtocolStore.Snapshot> stores, Map<String, SourceRows> sourceRows) {}
    public record Step(String messageType, byte[] ciphertext, byte[] expectedPlaintext) {}
    public record Scenario(String id, String kind, String namespace, String sourceAccountID, String peerAccountID,
                           SID localServiceID, SID remoteServiceID, List<Step> queued,
                           Integer consumedECID, Integer usedKyberID, boolean retainKyber,
                           String distributionID, byte[] distributionMessage) {}
    public record Corpus(int version, List<Account> accounts, List<Scenario> scenarios, Map<String, Object> expectedMetadata) {}
    public record Request(int version, String scenarioID, int step, SID senderServiceID, SID receiverServiceID,
                          String messageType, byte[] ciphertext, byte[] expectedPlaintext) {}
    public record Response(int version, String scenarioID, int step, SID senderServiceID, SID receiverServiceID,
                           String messageType, byte[] ciphertext, byte[] expectedPlaintext, String peerStateSHA256) {}
    // The seed is an original Java corpus account, never a Go-created protocol store.
    public record Seed(Scenario scenario, Account peer) {}
    public record PeerState(String seedSHA256, int lastStep, SourceProtocolStore.Snapshot store) {}

    public static void main(String[] args) throws Exception {
        if (!ImportFixtures.class.desiredAssertionStatus()) throw new IllegalStateException("enable Java assertions");
        if (args.length == 2 && args[0].equals("generate") && Files.exists(Path.of(args[1])))
            throw new FileAlreadyExistsException(args[1]);
        if (args.length == 1 && args[0].equals("self-test")) {
            verifiedProvenance();
            Class.forName("org.gosignal.accountimport.ImportFixturesTest").getMethod("main", String[].class)
                .invoke(null, (Object)new String[0]);
        } else if (args.length == 2 && args[0].equals("generate")) generate(Path.of(args[1]));
        else if (args.length == 2 && args[0].equals("continue")) {
            verifiedProvenance();
            continuePeer(Path.of(args[1]));
        } else throw new IllegalArgumentException("expected self-test, generate <new-dir>, or continue <exchange-dir>");
    }

    static ObjectNode verifiedProvenance() throws Exception {
        String path = System.getProperty("gosignal.import.provenance");
        if (path == null) throw new IllegalStateException("missing verified artifact provenance");
        var node = (ObjectNode) JSON.readTree(Path.of(path).toFile());
        var m = node.path("supportedMarkers");
        if (m.path("registry").asInt() != 2 || m.path("accountJSON").asInt() != 11 ||
            m.path("sqlite").asInt() != 31 || !m.path("javaLibsignal").asText().equals("0.103.0"))
            throw new IllegalArgumentException("unsupported source format markers");
        // Force loading, then identify the actual bundled resource rather than guessing
        // production vs testing JNI. This acceptance harness currently targets Linux amd64.
        IdentityKeyPair.generate();
        String loaded = Files.readAllLines(Path.of("/proc/self/maps")).stream()
            .filter(s -> s.contains("libsignal_jni") && s.contains(".so"))
            .map(s -> s.substring(s.indexOf('/'))).distinct().findFirst()
            .orElseThrow(() -> new IllegalStateException("cannot identify loaded JNI"));
        String name = Path.of(loaded).getFileName().toString();
        String hash = sha(Files.readAllBytes(Path.of(loaded)));
        boolean matched = false;
        for (var entry : node.path("embeddedJNI")) {
            if (name.endsWith(entry.path("resource").asText()) && hash.equals(entry.path("sha256").asText())) matched = true;
        }
        if (!matched) throw new IllegalStateException("loaded JNI checksum mismatch");
        node.set("loadedJNI", JSON.valueToTree(Map.of("resource", name, "sha256", hash)));
        byte[] vector = UuidUtil.toByteArray(UUID.fromString("00112233-4455-6677-8899-aabbccddeeff"));
        if (!HexFormat.of().formatHex(vector).equals("00112233445566778899aabbccddeeff"))
            throw new IllegalStateException("source UUID codec mismatch");
        node.put("uuidCodecVector", HexFormat.of().formatHex(vector));
        return node;
    }

    static SourceProtocolStore fresh(int registration) throws Exception {
        var s = new SourceProtocolStore(IdentityKeyPair.generate(), registration);
        for (int id : new int[]{0, 1, 5}) s.storePreKey(id, new PreKeyRecord(id, ECKeyPair.generate()));
        var signed = ECKeyPair.generate();
        s.storeSignedPreKey(2, new SignedPreKeyRecord(2, KEY_TIMESTAMP_MS, signed,
            s.getIdentityKeyPair().getPrivateKey().calculateSignature(signed.getPublicKey().serialize())));
        for (int id : new int[]{3, 4}) {
            var k = KEMKeyPair.generate(KEMKeyType.KYBER_1024);
            s.storeKyberPreKey(id, new KyberPreKeyRecord(id, KEY_TIMESTAMP_MS, k,
                s.getIdentityKeyPair().getPrivateKey().calculateSignature(k.getPublicKey().serialize())));
        }
        s.setLastResort(4, true);
        return s;
    }
    static PreKeyBundle bundle(SourceProtocolStore s, int ecID, int kyberID) throws Exception {
        var ec = s.loadPreKey(ecID); var sig = s.loadSignedPreKey(2); var k = s.loadKyberPreKey(kyberID);
        return new PreKeyBundle(s.getLocalRegistrationId(), 2, ecID, ec.getKeyPair().getPublicKey(),
            2, sig.getKeyPair().getPublicKey(), sig.getSignature(), s.getIdentityKeyPair().getPublicKey(),
            kyberID, k.getKeyPair().getPublicKey(), k.getSignature());
    }
    static Step encrypt(SourceProtocolStore s, SID local, SID remote, String text) throws Exception {
        byte[] plain = text.getBytes(StandardCharsets.UTF_8);
        var msg = new SessionCipher(s, local.address(), remote.address()).encrypt(plain);
        return new Step(msg.getType() == CiphertextMessage.PREKEY_TYPE ? "prekey" : "signal", msg.serialize(), plain);
    }
    static byte[] decrypt(SourceProtocolStore s, SID local, SID remote, Step step) throws Exception {
        var c = new SessionCipher(s, local.address(), remote.address());
        return step.messageType().equals("prekey") ? c.decrypt(new PreKeySignalMessage(step.ciphertext())) :
            c.decrypt(new SignalMessage(step.ciphertext()));
    }
    static void deliver(SourceProtocolStore from, SourceProtocolStore to, SID a, SID b, String text) throws Exception {
        var step = encrypt(from, a, b, text);
        if (!Arrays.equals(decrypt(to, b, a, step), step.expectedPlaintext())) throw new AssertionError("Java plaintext mismatch");
    }
    static void establish(SourceProtocolStore a, SourceProtocolStore b, SID aid, SID bid, int ec, int kyber) throws Exception {
        new SessionBuilder(a, bid.address(), aid.address()).process(bundle(b, ec, kyber));
        // Both directions acknowledge pending state; enough genuine rounds reach
        // established PQ state without manufactured timestamps or wire records.
        for (int i = 0; i < 36; i++) {
            deliver(a, b, aid, bid, "warmup-a-" + i);
            deliver(b, a, bid, aid, "warmup-b-" + i);
        }
    }
    static void archive(SourceProtocolStore s, SID peer) {
        var record = s.loadSession(peer.address()); record.archiveCurrentState(); s.storeSession(peer.address(), record);
    }
    static Account account(String id, boolean alice, String namespace, SourceProtocolStore selected) throws Exception {
        var scopes = new TreeMap<String, SourceProtocolStore.Snapshot>();
        scopes.put(namespace, selected.snapshot());
        scopes.put(namespace.equals("ACI") ? "PNI" : "ACI", fresh(alice ? 123 : 456).snapshot());
        var rows = new TreeMap<String, SourceRows>();
        for (var entry : scopes.entrySet()) rows.put(entry.getKey(), sourceRows(SourceProtocolStore.restore(entry.getValue())));
        return new Account(id, alice ? ALICE_ACI : BOB_ACI, alice ? ALICE_PNI : BOB_PNI, 2, namespace, scopes, rows);
    }
    static SourceRows sourceRows(SourceProtocolStore s) throws Exception {
        var ec = new ArrayList<ECRow>(); var signed = new ArrayList<SignedRow>(); var kyber = new ArrayList<KyberRow>();
        for (int id : s.snapshot().preKeys().keySet()) {
            var k = s.loadPreKey(id).getKeyPair(); ec.add(new ECRow(id, k.getPublicKey().serialize(), k.getPrivateKey().serialize()));
        }
        for (var k : s.loadSignedPreKeys()) signed.add(new SignedRow(k.getId(), k.getKeyPair().getPublicKey().serialize(),
            k.getKeyPair().getPrivateKey().serialize(), k.getSignature(), k.getTimestamp()));
        for (var k : s.loadKyberPreKeys()) kyber.add(new KyberRow(k.getId(), k.serialize(),
            s.snapshot().lastResortIDs().contains(k.getId()), k.getTimestamp()));
        return new SourceRows(s.getIdentityKeyPair().getPublicKey().serialize(), s.getIdentityKeyPair().getPrivateKey().serialize(), ec, signed, kyber);
    }
    static Corpus corpus() throws Exception {
        var accounts = new ArrayList<Account>(); var scenarios = new ArrayList<Scenario>();
        for (String namespace : new String[]{"ACI", "PNI"}) for (String kind : new String[]{"ordinary", "last-resort", "current-skipped", "archived-only", "archived-current", "group-skipped"}) {
            String id = namespace.toLowerCase(Locale.ROOT) + "-" + kind;
            var aid = new SID(namespace, namespace.equals("ACI") ? ALICE_ACI : ALICE_PNI);
            var bid = new SID(namespace, namespace.equals("ACI") ? BOB_ACI : BOB_PNI);
            var a = fresh(123); var b = fresh(456);
            var queue = new ArrayList<Step>(); String distribution = null; byte[] skdm = null;
            boolean receivingBob = kind.equals("ordinary") || kind.equals("last-resort");
            if (receivingBob) {
                int kyber = kind.equals("ordinary") ? 3 : 4;
                new SessionBuilder(a, bid.address(), aid.address()).process(bundle(b, 0, kyber));
                queue.add(encrypt(a, aid, bid, id + "/queued-prekey"));
            } else if (kind.equals("group-skipped")) {
                UUID dist = UUID.fromString("55555555-5555-4555-8555-555555555555"); distribution = dist.toString();
                var created = new GroupSessionBuilder(b).create(bid.address(), dist); skdm = created.serialize();
                new GroupSessionBuilder(a).process(bid.address(), new SenderKeyDistributionMessage(skdm));
                byte[] first = (id + "/group-skipped").getBytes(StandardCharsets.UTF_8);
                var skipped = new GroupCipher(b, bid.address()).encrypt(dist, first);
                var later = new GroupCipher(b, bid.address()).encrypt(dist, "group-later".getBytes(StandardCharsets.UTF_8));
                new GroupCipher(a, bid.address()).decrypt(later.serialize());
                queue.add(new Step("sender-key", skipped.serialize(), first));
            } else {
                establish(a, b, aid, bid, 0, 3);
                queue.add(encrypt(b, bid, aid, id + "/skipped-direct"));
                deliver(b, a, bid, aid, id + "/later-direct");
                if (kind.equals("current-skipped")) {
                    // The representative source SQL also contains a genuine incoming
                    // sender-key row, exercising its plain UUID BLOB codec.
                    UUID group = UUID.fromString("66666666-6666-4666-8666-666666666666");
                    var message = new GroupSessionBuilder(b).create(bid.address(), group);
                    new GroupSessionBuilder(a).process(bid.address(), new SenderKeyDistributionMessage(message.serialize()));
                }
                if (kind.startsWith("archived")) {
                    archive(a, bid);
                    if (kind.equals("archived-current")) establish(a, b, aid, bid, 1, 4);
                }
            }
            String source = id + "-source", peer = id + "-peer";
            accounts.add(account(source, !receivingBob, namespace, receivingBob ? b : a));
            accounts.add(account(peer, receivingBob, namespace, receivingBob ? a : b));
            scenarios.add(new Scenario(id, kind, namespace, source, peer, receivingBob ? bid : aid,
                receivingBob ? aid : bid, queue, receivingBob ? 0 : null,
                receivingBob ? (kind.equals("ordinary") ? 3 : 4) : null, kind.equals("last-resort"), distribution, skdm));
        }
        var metadata = new TreeMap<String, Object>();
        for (var account : accounts) for (var scope : account.stores().entrySet())
            for (var session : scope.getValue().sessions().entrySet())
                metadata.put(account.id() + "/" + scope.getKey() + "/" + session.getKey(), inspectJavaRecord(session.getValue()));
        return new Corpus(1, accounts, scenarios, metadata);
    }

    static void generate(Path output) throws Exception {
        var provenance = verifiedProvenance(); var corpus = corpus();
        // All native operations and validation finish before creating the destination.
        Files.createDirectory(output);
        try {
            JSON.writeValue(output.resolve("corpus.json").toFile(), corpus);
            var representative = corpus.accounts().stream().filter(a -> a.id().equals("aci-current-skipped-source")).findFirst().orElseThrow();
            JSON.writeValue(output.resolve("accounts.json").toFile(), Map.of("version", 2, "accounts", List.of(Map.of(
                "path", "synthetic-linked", "environment", "LIVE", "number", "+12025550111", "uuid", representative.aci()))));
            JSON.writeValue(output.resolve("account.json").toFile(), accountJSON(representative));
            Files.copy(Objects.requireNonNull(ImportFixtures.class.getResourceAsStream("/source-schema-v31.sql")), output.resolve("schema.sql"));
            Files.writeString(output.resolve("rows.sql"), sqlRows(representative));
            provenance.set("sourceFormatFiles", JSON.readTree(Objects.requireNonNull(ImportFixtures.class.getResourceAsStream("/source-provenance.json"))));
            var hashes = new TreeMap<String, String>();
            for (String file : new String[]{"corpus.json", "accounts.json", "account.json", "schema.sql", "rows.sql"}) hashes.put(file, sha(Files.readAllBytes(output.resolve(file))));
            provenance.set("fixtureSHA256", JSON.valueToTree(hashes));
            JSON.writeValue(output.resolve("provenance.json").toFile(), provenance);
            Files.writeString(output.resolve("README.md"), "# Synthetic Java 0.103.0 protocol corpus\n\nGenerated offline with the exact Java artifact and bundled JNI recorded in provenance.json. Every identity, credential and message is invented. No Signal account or server was accessed. Binary JSON fields use padded base64. Source SQL is schema 31, account JSON is 11 and registry is 2. This is protocol-state evidence; storage-service records, derivation, allocator and general import are separate stages. Regeneration changes random keys; review invariants and provenance.\n");
        } catch (Exception failure) {
            try (var files = Files.list(output)) { for (var file : files.toList()) Files.delete(file); }
            Files.delete(output); throw failure;
        }
        System.out.println("Generated 12 synthetic Java scenarios; no account/server access");
    }
    static Map<String, Object> accountJSON(Account a) {
        var json = new LinkedHashMap<String, Object>();
        json.put("version", 11); json.put("timestamp", KEY_TIMESTAMP_MS); json.put("serviceEnvironment", "LIVE");
        json.put("registered", true); json.put("number", "+12025550111"); json.put("deviceId", 2); json.put("isMultiDevice", true);
        json.put("password", "synthetic-offline-password-never-authenticate");
        json.put("profileKey", Base64.getEncoder().encodeToString(new byte[32]));
        for (String name : new String[]{"username", "encryptedDeviceName", "registrationLockPin", "pinMasterKey", "storageKey", "accountEntropyPool", "authCredentialSalt", "mediaRootBackupKey", "usernameLinkEntropy", "usernameLinkServerId"}) json.put(name, null);
        for (String kind : new String[]{"ACI", "PNI"}) {
            var r = a.sourceRows().get(kind);
            json.put(kind.toLowerCase(Locale.ROOT) + "AccountData", Map.of("serviceId", kind.equals("ACI") ? a.aci() : "PNI:" + a.pni(),
                "registrationId", a.stores().get(kind).registrationID(), "identityPublicKey", Base64.getEncoder().encodeToString(r.identityPublic()),
                "identityPrivateKey", Base64.getEncoder().encodeToString(r.identityPrivate()), "nextPreKeyId", 100, "nextSignedPreKeyId", 100,
                "activeSignedPreKeyId", 2, "nextKyberPreKeyId", 100, "activeLastResortKyberPreKeyId", 4));
        }
        return json;
    }
    static String blob(byte[] raw) { return "X'" + HexFormat.of().formatHex(raw) + "'"; }
    static String text(String s) { return "'" + s.replace("'", "''") + "'"; }
    static String sqlRows(Account a) {
        var sql = new StringBuilder("-- Synthetic source rows, not a target database.\nBEGIN;\n");
        for (String kind : new String[]{"ACI", "PNI"}) {
            int type = kind.equals("ACI") ? 0 : 1; var r = a.sourceRows().get(kind); var s = a.stores().get(kind);
            for (var k : r.preKeys()) sql.append("INSERT INTO pre_key(account_id_type,key_id,public_key,private_key) VALUES(").append(type).append(',').append(k.id()).append(',').append(blob(k.publicKey())).append(',').append(blob(k.privateKey())).append(");\n");
            for (var k : r.signedPreKeys()) sql.append("INSERT INTO signed_pre_key(account_id_type,key_id,public_key,private_key,signature,timestamp) VALUES(").append(type).append(',').append(k.id()).append(',').append(blob(k.publicKey())).append(',').append(blob(k.privateKey())).append(',').append(blob(k.signature())).append(',').append(k.timestampMilliseconds()).append(");\n");
            for (var k : r.kyberPreKeys()) sql.append("INSERT INTO kyber_pre_key(account_id_type,key_id,serialized,is_last_resort,timestamp) VALUES(").append(type).append(',').append(k.id()).append(',').append(blob(k.serialized())).append(',').append(k.lastResort() ? 1 : 0).append(',').append(k.timestampMilliseconds()).append(");\n");
            s.sessions().forEach((address, raw) -> {
                int slash = address.lastIndexOf('/');
                sql.append("INSERT INTO session(account_id_type,address,device_id,record) VALUES(").append(type).append(',').append(text(address.substring(0, slash))).append(',').append(address.substring(slash + 1)).append(',').append(blob(raw)).append(");\n");
            });
            s.identities().forEach((address, raw) -> sql.append("INSERT INTO identity(address,identity_key,added_timestamp,trust_level) VALUES(").append(text(address)).append(',').append(blob(raw)).append(',').append(KEY_TIMESTAMP_MS).append(",1);\n"));
            s.senderKeys().forEach((address, raw) -> {
                String[] parts = address.split("/");
                sql.append("INSERT INTO sender_key(address,device_id,distribution_id,record,created_timestamp) VALUES(").append(text(parts[0])).append(',').append(parts[1]).append(',').append(blob(UuidUtil.toByteArray(UUID.fromString(parts[2])))).append(',').append(blob(raw)).append(',').append(KEY_TIMESTAMP_MS).append(");\n");
            });
        }
        return sql.append("COMMIT;\n").toString();
    }

    static void continuePeer(Path dir) throws Exception {
        byte[] seedBytes = Files.readAllBytes(dir.resolve("seed.json")); var seed = JSON.readValue(seedBytes, Seed.class);
        var request = JSON.readValue(dir.resolve("request.json").toFile(), Request.class); String seedHash = sha(seedBytes);
        var scenario = seed.scenario();
        if (request.version() != 1 || !request.scenarioID().equals(scenario.id()) || !request.senderServiceID().equals(scenario.localServiceID()) ||
            !request.receiverServiceID().equals(scenario.remoteServiceID()) || !seed.peer().id().equals(scenario.peerAccountID()))
            throw new IllegalArgumentException("exchange identity/scenario mismatch");
        Path statePath = dir.resolve("peer-state.json");
        var state = Files.exists(statePath) ? JSON.readValue(statePath.toFile(), PeerState.class) :
            new PeerState(seedHash, -1, seed.peer().stores().get(scenario.namespace()));
        if (!state.seedSHA256().equals(seedHash) || request.step() != state.lastStep() + 1) throw new IllegalArgumentException("exchange continuity mismatch");
        var store = SourceProtocolStore.restore(state.store()); SID local = request.receiverServiceID(), remote = request.senderServiceID();
        Step response;
        if (request.messageType().equals("sender-key-distribution")) {
            new GroupSessionBuilder(store).process(remote.address(), new SenderKeyDistributionMessage(request.ciphertext()));
            response = new Step("ack", new byte[0], new byte[0]);
        } else {
            byte[] actual = request.messageType().equals("sender-key") ? new GroupCipher(store, remote.address()).decrypt(request.ciphertext()) :
                decrypt(store, local, remote, new Step(request.messageType(), request.ciphertext(), request.expectedPlaintext()));
            if (!Arrays.equals(actual, request.expectedPlaintext())) throw new IllegalStateException("Java continuation plaintext mismatch");
            if (request.messageType().equals("prekey")) {
                // Fresh Go initiation uses the retained original Java EC5/LR4 bundle.
                if (store.containsPreKey(5) || !store.containsKyberPreKey(4) || !store.containsSignedPreKey(2))
                    throw new AssertionError("Java continuation key consumption differs");
            }
            String reply = scenario.id() + "/java/" + request.step();
            if (request.messageType().equals("sender-key")) {
                byte[] plain = reply.getBytes(StandardCharsets.UTF_8);
                response = new Step("sender-key", new GroupCipher(store, local.address()).encrypt(UUID.fromString(scenario.distributionID()), plain).serialize(), plain);
            } else response = encrypt(store, local, remote, reply);
        }
        byte[] stateBytes = JSON.writeValueAsBytes(new PeerState(seedHash, request.step(), store.snapshot()));
        atomic(statePath, stateBytes);
        atomic(dir.resolve("response.json"), JSON.writeValueAsBytes(new Response(1, scenario.id(), request.step(), local, remote,
            response.messageType(), response.ciphertext(), response.expectedPlaintext(), sha(stateBytes))));
    }
    static void atomic(Path path, byte[] bytes) throws Exception {
        Path tmp = Files.createTempFile(path.getParent(), ".java-peer-", ".tmp");
        try { Files.write(tmp, bytes); Files.move(tmp, path, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING); }
        finally { Files.deleteIfExists(tmp); }
    }
    static String sha(byte[] bytes) throws Exception { return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes)); }

    // A bounded independent wire reader extracts expected Java metadata. Current
    // getters are cross-checked against Java's native SessionRecord below.
    record Field(int number, Long scalar, byte[] bytes) {}
    static List<Field> wire(byte[] raw) {
        if (raw.length > 1 << 20) throw new IllegalArgumentException("fixture record too large");
        var out = new ArrayList<Field>(); int[] pos = {0};
        while (pos[0] < raw.length) {
            long tag = varint(raw, pos); int field = (int)(tag >>> 3), type = (int)(tag & 7);
            if (field == 0) throw new IllegalArgumentException("invalid fixture protobuf tag");
            if (type == 0) out.add(new Field(field, varint(raw, pos), null));
            else if (type == 2) {
                long size = varint(raw, pos);
                if (size < 0 || size > raw.length - pos[0]) throw new IllegalArgumentException("invalid fixture protobuf length");
                out.add(new Field(field, null, Arrays.copyOfRange(raw, pos[0], pos[0] + (int)size))); pos[0] += (int)size;
            } else throw new IllegalArgumentException("unsupported fixture protobuf wire type");
        }
        return out;
    }
    static long varint(byte[] raw, int[] pos) {
        long value = 0;
        for (int shift = 0; shift < 64; shift += 7) {
            if (pos[0] == raw.length) throw new IllegalArgumentException("truncated fixture protobuf");
            int next = raw[pos[0]++] & 255;
            if (shift == 63 && next > 1) throw new IllegalArgumentException("overflow fixture protobuf");
            value |= (long)(next & 127) << shift; if (next < 128) return value;
        }
        throw new IllegalArgumentException("overflow fixture protobuf");
    }
    static byte[] bytes(List<Field> fields, int number) { return fields.stream().filter(f -> f.number() == number).reduce((a,b) -> b).map(Field::bytes).orElse(new byte[0]); }
    static long scalar(List<Field> fields, int number) { return fields.stream().filter(f -> f.number() == number).reduce((a,b) -> b).map(Field::scalar).orElse(0L); }
    static Object inspectJavaRecord(byte[] raw) throws Exception {
        var fields = wire(raw); var archives = new ArrayList<Object>(); Object current = null;
        for (var field : fields) {
            if (field.number() == 1) current = stateMetadata(field.bytes());
            else if (field.number() == 2) archives.add(stateMetadata(field.bytes()));
        }
        if (current != null) {
            var record = new SessionRecord(raw); var state = wire(bytes(fields, 1));
            if (record.getSessionVersion() != scalar(state, 1) || record.getLocalRegistrationId() != scalar(state, 11) ||
                record.getRemoteRegistrationId() != scalar(state, 10) ||
                !Arrays.equals(record.getLocalIdentityKey().serialize(), bytes(state, 2)) ||
                !Arrays.equals(record.getRemoteIdentityKey().serialize(), bytes(state, 3))) throw new AssertionError("Java metadata reader mismatch");
        }
        var result = new LinkedHashMap<String, Object>(); result.put("Current", current); result.put("Archived", archives); return result;
    }
    static Object stateMetadata(byte[] raw) {
        var f = wire(raw); var m = new LinkedHashMap<String, Object>();
        m.put("Version", scalar(f, 1)); m.put("LocalRegistrationID", scalar(f, 11)); m.put("RemoteRegistrationID", scalar(f, 10));
        m.put("LocalIdentityPublic", bytes(f, 2)); m.put("RemoteIdentityPublic", bytes(f, 3));
        m.put("SenderChainPresent", f.stream().anyMatch(x -> x.number() == 6));
        int chains = 0, skipped = 0;
        for (var field : f) if (field.number() == 7) { chains++; skipped += (int)wire(field.bytes()).stream().filter(x -> x.number() == 4).count(); }
        m.put("ReceiverChainCount", chains); m.put("SkippedMessageKeyCount", skipped);
        Object pending = null, kyber = null;
        if (f.stream().anyMatch(x -> x.number() == 9)) {
            var p = wire(bytes(f, 9)); var pm = new LinkedHashMap<String, Object>();
            pm.put("PreKeyID", p.stream().anyMatch(x -> x.number() == 1) ? scalar(p, 1) : null);
            pm.put("SignedPreKeyID", (int)scalar(p, 3)); pm.put("TimestampSeconds", scalar(p, 4)); pending = pm;
        }
        if (f.stream().anyMatch(x -> x.number() == 14)) kyber = scalar(wire(bytes(f, 14)), 1);
        m.put("PendingPreKey", pending); m.put("PendingKyberID", kyber);
        var pq = wire(bytes(f, 15)); boolean negotiating = pq.stream().anyMatch(x -> x.number() == 1);
        m.put("PQRatchet", Map.of("Present", bytes(f, 15).length > 0, "Version", pq.stream().anyMatch(x -> x.number() == 3) ? 1 : 0,
            "Negotiating", negotiating, "MinVersion", negotiating ? scalar(wire(bytes(pq, 1)), 3) : 0));
        return m;
    }
}
