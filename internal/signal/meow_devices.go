//go:build cgo || purego

package signal

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/web"
	"google.golang.org/protobuf/proto"
)

// createdAtInfo is the HPKE info string the server seals a device's creation time with.
const createdAtInfo = "deviceCreatedAt"

var (
	errServerStatus = errors.New("unexpected server response")
	errCreatedAt    = errors.New("invalid createdAt plaintext")
	errDeviceName   = errors.New("device name doesn't match its synthetic IV")
)

// deviceList is the server's response to GET /v1/devices/.
type deviceList struct {
	Devices []deviceInfo `json:"devices"`
}

type deviceInfo struct {
	ID int `json:"id"`
	// Name is the base64 of an encrypted DeviceName protobuf (plain text on old devices).
	Name string `json:"name"`
	// LastSeen is in ms since epoch, truncated to the day by the server.
	LastSeen       int64 `json:"lastSeen"`
	RegistrationID int32 `json:"registrationId"`
	// CreatedAtCiphertext is the creation time in ms, HPKE-sealed to our ACI identity key.
	CreatedAtCiphertext []byte `json:"createdAtCiphertext"`
}

func (c *meowClient) Devices(ctx context.Context) ([]Device, error) {
	ctx = c.zlog.WithContext(ctx)

	acc, err := c.selectAccount()
	if err != nil {
		return nil, err
	}

	if acc.Unlinked() {
		return nil, UnlinkedError(acc)
	}

	device, err := c.device(ctx)
	if err != nil {
		return nil, err
	}

	body, err := serverRequest(ctx, &device.DeviceData, http.MethodGet, "/v1/devices/")
	if errors.Is(err, ErrDeviceUnlinked) {
		return nil, c.markUnlinked(acc, err)
	}

	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	return devicesFromResponse(body, device.ACIIdentityKeyPair, device.DeviceID)
}

func (c *meowClient) Unlink(ctx context.Context, opts UnlinkOptions) (Account, error) {
	if c.cli != nil {
		return Account{}, ErrAlreadyConnected
	}

	ctx = c.zlog.WithContext(ctx)

	acc, err := c.selectAccount()
	if err != nil {
		return Account{}, err
	}

	// Open the account (which may switch databases) before taking its lock.
	device, err := c.unlinkDevice(ctx, acc, opts)
	if err != nil {
		return Account{}, err
	}

	if c.lock == nil {
		c.lock, err = c.dir.Lock(acc.ACI)
		if err != nil {
			return Account{}, fmt.Errorf("%w: %s", err, acc.Number)
		}
	}

	if device != nil {
		err = c.removeDevice(ctx, device)
		if err != nil {
			return Account{}, err
		}
	}

	err = c.removeLocal(acc.ACI)
	if err != nil {
		return Account{}, err
	}

	return acc, nil
}

// unlinkDevice loads the device to remove from the server, or returns nil when the server is
// skipped: with LocalOnly, and for an account marked as unlinked (the server already removed it).
func (c *meowClient) unlinkDevice(ctx context.Context, acc Account, opts UnlinkOptions) (*mstore.Device, error) {
	if opts.LocalOnly || acc.Unlinked() {
		return nil, nil //nolint:nilnil // no device to remove is not an error
	}

	device, err := c.device(ctx)
	if errors.Is(err, ErrNotLinked) {
		// The device keys are gone (e.g. cleared after a logout), so the server can't be asked.
		return nil, fmt.Errorf("%w; use --local-only to delete the local data", err)
	}

	return device, err
}

// removeLocal deletes the account's local data while still holding its lock, then releases it.
func (c *meowClient) removeLocal(aci string) error {
	lock := c.lock
	c.lock = nil

	// Close the database first; the lock stays held until the files are gone.
	err := c.release()
	if err == nil {
		err = c.dir.RemoveAccount(aci)
	}

	return errors.Join(err, lock.Unlock())
}

// removeDevice deletes our device from the account on the server. A device the server already
// logged out counts as removed.
func (c *meowClient) removeDevice(ctx context.Context, device *mstore.Device) error {
	_, err := serverRequest(ctx, &device.DeviceData, http.MethodDelete,
		"/v1/devices/"+strconv.Itoa(device.DeviceID))
	if errors.Is(err, ErrDeviceUnlinked) {
		c.log.Info("device was already removed from the account", "error", err)

		return nil
	}

	if err != nil {
		return fmt.Errorf("remove device from account: %w (use --local-only to skip this)", err)
	}

	return nil
}

// serverRequest sends an authenticated REST request to the chat server and returns the body.
// 401 and 403 mean the server no longer knows this device: ErrDeviceUnlinked.
func serverRequest(ctx context.Context, device *mstore.DeviceData, method, path string) ([]byte, error) {
	username, password := device.BasicAuthCreds()

	resp, err := web.SendHTTPRequest(ctx, web.APIHostname, method, path,
		&web.HTTPReqOpt{Username: &username, Password: &password})
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", method, path, err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrDeviceUnlinked, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("%w: %s %s: HTTP %d", errServerStatus, method, path, resp.StatusCode)
	}

	return body, nil
}

// devicesFromResponse decodes a GET /v1/devices/ response. Names and creation times are
// encrypted to our ACI identity key; undecryptable values are left empty rather than failing.
func devicesFromResponse(body []byte, keys *libsignalgo.IdentityKeyPair, ownID int) ([]Device, error) {
	var list deviceList

	err := json.Unmarshal(body, &list)
	if err != nil {
		return nil, fmt.Errorf("decode device list: %w", err)
	}

	privateKey, err := keys.GetPrivateKey().Serialize()
	if err != nil {
		return nil, fmt.Errorf("serialize identity key: %w", err)
	}

	devices := make([]Device, 0, len(list.Devices))

	for _, info := range list.Devices {
		dev := Device{ID: info.ID, Name: deviceName(info.Name, keys), Current: info.ID == ownID}

		if info.LastSeen > 0 {
			dev.LastSeen = time.UnixMilli(info.LastSeen).UTC()
		}

		created, err := deviceCreatedAt(info, privateKey)
		if err == nil {
			dev.Created = created
		}

		devices = append(devices, dev)
	}

	return devices, nil
}

// deviceName decrypts a device name, falling back to the raw value for old plain-text names.
func deviceName(encoded string, keys *libsignalgo.IdentityKeyPair) string {
	if encoded == "" {
		return ""
	}

	wrapped, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return encoded
	}

	name, err := decryptDeviceName(wrapped, keys.GetPrivateKey())
	if err != nil {
		return string(wrapped)
	}

	return name
}

// decryptDeviceName reverses signalmeow.EncryptDeviceName (Signal's DeviceNameCipher).
// signalmeow.DecryptDeviceName can't be used: its synthetic-IV check never matches.
func decryptDeviceName(wrapped []byte, identityKey *libsignalgo.PrivateKey) (string, error) {
	var name signalpb.DeviceName

	err := proto.Unmarshal(wrapped, &name)
	if err != nil {
		return "", fmt.Errorf("decode device name: %w", err)
	}

	ephemeral, err := libsignalgo.DeserializePublicKey(name.GetEphemeralPublic())
	if err != nil {
		return "", fmt.Errorf("device name key: %w", err)
	}

	master, err := identityKey.Agree(ephemeral)
	if err != nil {
		return "", fmt.Errorf("device name key: %w", err)
	}

	block, err := aes.NewCipher(hmacSHA256(hmacSHA256(master, []byte("cipher")), name.GetSyntheticIv()))
	if err != nil {
		return "", fmt.Errorf("device name cipher: %w", err)
	}

	plain := make([]byte, len(name.GetCiphertext()))
	cipher.NewCTR(block, make([]byte, aes.BlockSize)).XORKeyStream(plain, name.GetCiphertext())

	const ivLen = 16
	if !hmac.Equal(hmacSHA256(hmacSHA256(master, []byte("auth")), plain)[:ivLen], name.GetSyntheticIv()) {
		return "", errDeviceName
	}

	return string(plain), nil
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)

	return mac.Sum(nil)
}

// deviceCreatedAt opens the sealed creation time. Like libsignal-service, the associated data
// is the device ID as one byte followed by the registration ID (big-endian int32).
func deviceCreatedAt(info deviceInfo, privateKey []byte) (time.Time, error) {
	if len(info.CreatedAtCiphertext) == 0 {
		return time.Time{}, errCreatedAt
	}

	plain, err := hpkeOpen(privateKey, info.CreatedAtCiphertext, []byte(createdAtInfo), createdAtAAD(info))
	if err != nil {
		return time.Time{}, err
	}

	const millisLen = 8
	if len(plain) != millisLen {
		return time.Time{}, fmt.Errorf("%w: %d bytes", errCreatedAt, len(plain))
	}

	return time.UnixMilli(int64(binary.BigEndian.Uint64(plain))).UTC(), nil //nolint:gosec // ms fit
}

func createdAtAAD(info deviceInfo) []byte {
	return binary.BigEndian.AppendUint32([]byte{byte(info.ID)}, uint32(info.RegistrationID)) //nolint:gosec // bit pattern
}
