package org.gosignal.accountimport;

import java.util.*;
import org.signal.libsignal.protocol.*;
import org.signal.libsignal.protocol.ecc.ECPublicKey;
import org.signal.libsignal.protocol.groups.state.SenderKeyRecord;
import org.signal.libsignal.protocol.state.*;

/** Synthetic source-oriented store. It never opens a signal-cli account. */
public final class SourceProtocolStore implements SignalProtocolStore {
    public record Snapshot(byte[] identity, int registrationID,
        Map<Integer, byte[]> preKeys, Map<Integer, byte[]> signedPreKeys,
        Map<Integer, byte[]> kyberPreKeys, Set<Integer> lastResortIDs,
        Map<String, byte[]> sessions, Map<String, byte[]> identities, Map<String, byte[]> senderKeys) {}

    private final IdentityKeyPair identity;
    private final int registrationID;
    private final Map<Integer, byte[]> preKeys = new TreeMap<>();
    private final Map<Integer, byte[]> signedPreKeys = new TreeMap<>();
    private final Map<Integer, byte[]> kyberPreKeys = new TreeMap<>();
    private final Set<Integer> lastResortIDs = new TreeSet<>();
    private final Map<String, byte[]> sessions = new TreeMap<>();
    private final Map<String, byte[]> identities = new TreeMap<>();
    private final Map<String, byte[]> senderKeys = new TreeMap<>();

    public SourceProtocolStore(IdentityKeyPair identity, int registrationID) {
        if (registrationID <= 0) throw new IllegalArgumentException("invalid registration ID");
        this.identity = identity;
        this.registrationID = registrationID;
    }

    static String key(SignalProtocolAddress address) { return address.getName() + "/" + address.getDeviceId(); }
    public void setLastResort(int id, boolean value) {
        if (!kyberPreKeys.containsKey(id)) throw new IllegalArgumentException("missing Kyber key");
        if (value) lastResortIDs.add(id); else lastResortIDs.remove(id);
    }
    public Snapshot snapshot() {
        return new Snapshot(identity.serialize(), registrationID, copy(preKeys), copy(signedPreKeys),
            copy(kyberPreKeys), Set.copyOf(lastResortIDs), copy(sessions), copy(identities), copy(senderKeys));
    }
    public static SourceProtocolStore restore(Snapshot value) throws InvalidKeyException {
        var result = new SourceProtocolStore(new IdentityKeyPair(value.identity()), value.registrationID());
        result.preKeys.putAll(copy(value.preKeys()));
        result.signedPreKeys.putAll(copy(value.signedPreKeys()));
        result.kyberPreKeys.putAll(copy(value.kyberPreKeys()));
        result.lastResortIDs.addAll(value.lastResortIDs());
        result.sessions.putAll(copy(value.sessions()));
        result.identities.putAll(copy(value.identities()));
        result.senderKeys.putAll(copy(value.senderKeys()));
        if (!result.kyberPreKeys.keySet().containsAll(result.lastResortIDs)) throw new IllegalArgumentException("missing last-resort key");
        return result;
    }
    private static <K> Map<K, byte[]> copy(Map<K, byte[]> values) {
        var result = new LinkedHashMap<K, byte[]>();
        values.forEach((key, value) -> result.put(key, value.clone()));
        return result;
    }
    private static byte[] required(Map<Integer, byte[]> values, int id) throws InvalidKeyIdException {
        byte[] raw = values.get(id);
        if (raw == null) throw new InvalidKeyIdException("missing synthetic prekey");
        return raw;
    }
    @Override public IdentityKeyPair getIdentityKeyPair() { return identity; }
    @Override public int getLocalRegistrationId() { return registrationID; }
    @Override public IdentityChange saveIdentity(SignalProtocolAddress address, IdentityKey value) {
        byte[] old = identities.put(address.getName(), value.serialize());
        return old == null || Arrays.equals(old, value.serialize()) ? IdentityChange.NEW_OR_UNCHANGED : IdentityChange.REPLACED_EXISTING;
    }
    @Override public boolean isTrustedIdentity(SignalProtocolAddress address, IdentityKey value, Direction direction) {
        byte[] old = identities.get(address.getName());
        return old == null || Arrays.equals(old, value.serialize());
    }
    @Override public IdentityKey getIdentity(SignalProtocolAddress address) {
        byte[] raw = identities.get(address.getName());
        if (raw == null) return null;
        try { return new IdentityKey(raw); } catch (InvalidKeyException e) { throw new IllegalStateException("invalid fixture identity", e); }
    }
    @Override public PreKeyRecord loadPreKey(int id) throws InvalidKeyIdException {
        byte[] raw = required(preKeys, id);
        try { return new PreKeyRecord(raw); } catch (InvalidMessageException e) { throw new IllegalStateException("invalid fixture prekey", e); }
    }
    @Override public void storePreKey(int id, PreKeyRecord record) { preKeys.put(id, record.serialize()); }
    @Override public boolean containsPreKey(int id) { return preKeys.containsKey(id); }
    @Override public void removePreKey(int id) { preKeys.remove(id); }
    @Override public SignedPreKeyRecord loadSignedPreKey(int id) throws InvalidKeyIdException {
        byte[] raw = required(signedPreKeys, id);
        try { return new SignedPreKeyRecord(raw); } catch (InvalidMessageException e) { throw new IllegalStateException("invalid fixture signed prekey", e); }
    }
    @Override public List<SignedPreKeyRecord> loadSignedPreKeys() {
        try {
            var result = new ArrayList<SignedPreKeyRecord>();
            for (int id : signedPreKeys.keySet()) result.add(loadSignedPreKey(id));
            return result;
        } catch (InvalidKeyIdException e) { throw new IllegalStateException(e); }
    }
    @Override public void storeSignedPreKey(int id, SignedPreKeyRecord record) { signedPreKeys.put(id, record.serialize()); }
    @Override public boolean containsSignedPreKey(int id) { return signedPreKeys.containsKey(id); }
    @Override public void removeSignedPreKey(int id) { signedPreKeys.remove(id); }
    @Override public KyberPreKeyRecord loadKyberPreKey(int id) throws InvalidKeyIdException {
        byte[] raw = required(kyberPreKeys, id);
        try { return new KyberPreKeyRecord(raw); } catch (InvalidMessageException e) { throw new IllegalStateException("invalid fixture Kyber prekey", e); }
    }
    @Override public List<KyberPreKeyRecord> loadKyberPreKeys() {
        try {
            var result = new ArrayList<KyberPreKeyRecord>();
            for (int id : kyberPreKeys.keySet()) result.add(loadKyberPreKey(id));
            return result;
        } catch (InvalidKeyIdException e) { throw new IllegalStateException(e); }
    }
    @Override public void storeKyberPreKey(int id, KyberPreKeyRecord record) { kyberPreKeys.put(id, record.serialize()); }
    @Override public boolean containsKyberPreKey(int id) { return kyberPreKeys.containsKey(id); }
    @Override public void markKyberPreKeyUsed(int id, int signedID, ECPublicKey baseKey) {
        // signal-cli deletes ordinary keys and retains last-resort keys. Its
        // schema has no tuple replay history to restore.
        if (!lastResortIDs.contains(id)) kyberPreKeys.remove(id);
    }
    @Override public SessionRecord loadSession(SignalProtocolAddress address) {
        byte[] raw = sessions.get(key(address));
        if (raw == null) return new SessionRecord();
        try { return new SessionRecord(raw); } catch (InvalidMessageException e) { throw new IllegalStateException("invalid fixture session", e); }
    }
    @Override public List<SessionRecord> loadExistingSessions(List<SignalProtocolAddress> addresses) throws NoSessionException {
        var result = new ArrayList<SessionRecord>();
        for (var address : addresses) {
            if (!containsSession(address)) throw new NoSessionException("missing synthetic session");
            result.add(loadSession(address));
        }
        return result;
    }
    @Override public List<Integer> getSubDeviceSessions(String name) {
        return sessions.keySet().stream().filter(k -> k.startsWith(name + "/"))
            .map(k -> Integer.parseInt(k.substring(k.lastIndexOf('/') + 1))).filter(id -> id != 1).toList();
    }
    @Override public void storeSession(SignalProtocolAddress address, SessionRecord record) { sessions.put(key(address), record.serialize()); }
    @Override public boolean containsSession(SignalProtocolAddress address) { return sessions.containsKey(key(address)); }
    @Override public void deleteSession(SignalProtocolAddress address) { sessions.remove(key(address)); }
    @Override public void deleteAllSessions(String name) { sessions.keySet().removeIf(k -> k.startsWith(name + "/")); }
    @Override public void storeSenderKey(SignalProtocolAddress address, UUID distribution, SenderKeyRecord record) { senderKeys.put(key(address) + "/" + distribution, record.serialize()); }
    @Override public SenderKeyRecord loadSenderKey(SignalProtocolAddress address, UUID distribution) {
        byte[] raw = senderKeys.get(key(address) + "/" + distribution);
        if (raw == null) return null;
        try { return new SenderKeyRecord(raw); } catch (InvalidMessageException e) { throw new IllegalStateException("invalid fixture sender key", e); }
    }
}
