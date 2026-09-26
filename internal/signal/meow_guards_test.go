//go:build cgo || purego

package signal_test

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

// sendTo is a valid request to alice.
func sendTo(atts ...signal.UploadedAttachment) signal.SendRequest {
	return signal.SendRequest{
		Recipients: []signal.Recipient{{ACI: aliceUser}}, Body: "hi", Attachments: atts,
	}
}

func TestOperationsNeedConnect(t *testing.T) {
	t.Parallel()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	ctx := t.Context()

	_, err = client.Upload(ctx, nil)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Upload: %v, want ErrNotConnected", err)
	}

	_, err = client.Send(ctx, sendTo())
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Send: %v, want ErrNotConnected", err)
	}

	err = client.SendReceipt(ctx, signal.Recipient{ACI: aliceUser}, signal.ReceiptRead, []uint64{1})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("SendReceipt: %v, want ErrNotConnected", err)
	}

	_, err = client.Sync(ctx, signal.SyncOptions{})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Sync: %v, want ErrNotConnected", err)
	}
}

func TestConnectedChecksRequests(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := openOffline(t, seedAccount(t))

	err := client.Connect(ctx)
	if !errors.Is(err, signal.ErrAlreadyConnected) {
		t.Errorf("second Connect: %v, want ErrAlreadyConnected", err)
	}

	_, err = client.Send(ctx, signal.SendRequest{Body: "to nobody"})
	if !errors.Is(err, signal.ErrInvalidSendRequest) {
		t.Errorf("Send without recipients: %v, want ErrInvalidSendRequest", err)
	}

	_, err = client.Send(ctx, sendTo(signal.UploadedAttachment{ID: "elsewhere", Filename: "a.jpg"}))
	if !errors.Is(err, signal.ErrUnknownAttachment) {
		t.Errorf("Send with a foreign attachment: %v, want ErrUnknownAttachment", err)
	}

	err = client.SendReceipt(ctx, signal.Recipient{ACI: aliceUser}, signal.ReceiptType(99), []uint64{1})
	if !errors.Is(err, signal.ErrInvalidReceipt) {
		t.Errorf("SendReceipt of an unknown type: %v, want ErrInvalidReceipt", err)
	}

	err = client.SendReceipt(ctx, signal.Recipient{Number: aliceE164}, signal.ReceiptRead, []uint64{1})
	if !errors.Is(err, signal.ErrUnresolvable) {
		t.Errorf("SendReceipt without an ACI: %v, want ErrUnresolvable", err)
	}
}

func TestConnectedGroupsWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := openOffline(t, seedAccount(t))

	// Without known groups, nothing is fetched.
	groups, err := client.Groups(ctx)
	if err != nil || len(groups) != 0 {
		t.Errorf("Groups = %+v, %v; want none", groups, err)
	}

	unknown := base64.StdEncoding.EncodeToString(randomKey(t))

	for _, ref := range []string{"not base64!", unknown} {
		_, err = client.Group(ctx, ref)
		if !errors.Is(err, signal.ErrUnknownGroup) {
			t.Errorf("Group(%q): %v, want ErrUnknownGroup", ref, err)
		}

		_, err = client.LeaveGroup(ctx, ref, signal.LeaveOptions{})
		if !errors.Is(err, signal.ErrUnknownGroup) {
			t.Errorf("LeaveGroup(%q): %v, want ErrUnknownGroup", ref, err)
		}
	}
}

func TestResolveWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := openOffline(t, seedAccount(t))

	got, err := client.Resolve(ctx, []signal.Recipient{{ACI: aliceUser}})
	if err != nil || len(got) != 1 || got[0].ACI != aliceUser {
		t.Errorf("Resolve(ACI) = %+v, %v", got, err)
	}

	_, err = client.Resolve(ctx, []signal.Recipient{{}})
	if !errors.Is(err, signal.ErrUnresolvable) {
		t.Errorf("Resolve(empty) = %v, want ErrUnresolvable", err)
	}

	// An unknown number needs contact discovery, which needs the signalmeow client.
	_, err = client.Resolve(ctx, []signal.Recipient{{Number: aliceE164}})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Resolve(uncached number) = %v, want ErrNotConnected", err)
	}
}

func TestLostConnectionFailsOperations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := openOffline(t, seedAccount(t), signal.SendOnly())
	uploaded := signal.AddUpload(client, signal.OutgoingAttachment{
		Filename: "photo.png", ContentType: pngType, Width: 4, Height: 3,
	})

	if uploaded.Filename != "photo.png" || uploaded.ContentType != pngType {
		t.Errorf("uploaded = %+v", uploaded)
	}

	signal.LoseConnection(client)

	_, err := client.Upload(ctx, nil)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("Upload: %v, want ErrDeviceUnlinked", err)
	}

	// The message is built (with the attachment) before the lost connection is noticed.
	_, err = client.Send(ctx, sendTo(uploaded))
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("Send: %v, want ErrDeviceUnlinked", err)
	}

	err = client.SendReceipt(ctx, signal.Recipient{ACI: aliceUser}, signal.ReceiptDelivery, []uint64{1})
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("SendReceipt: %v, want ErrDeviceUnlinked", err)
	}

	_, err = client.Groups(ctx)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("Groups: %v, want ErrDeviceUnlinked", err)
	}

	_, err = client.Sync(ctx, signal.SyncOptions{})
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("Sync: %v, want ErrDeviceUnlinked", err)
	}
}

func TestClosedClientRefusesOperations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := openOffline(t, seedAccount(t))

	err := client.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = client.Upload(ctx, nil)
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("Upload: %v, want ErrClosed", err)
	}

	_, err = client.Send(ctx, sendTo())
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("Send: %v, want ErrClosed", err)
	}

	err = client.SendReceipt(ctx, signal.Recipient{ACI: aliceUser}, signal.ReceiptRead, []uint64{1})
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("SendReceipt: %v, want ErrClosed", err)
	}

	_, err = client.Groups(ctx)
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("Groups: %v, want ErrClosed", err)
	}

	_, err = client.GroupTitles(ctx)
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("GroupTitles: %v, want ErrClosed", err)
	}

	_, err = client.Sync(ctx, signal.SyncOptions{})
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("Sync: %v, want ErrClosed", err)
	}

	_, err = client.Resolve(ctx, []signal.Recipient{{ACI: aliceUser}})
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("Resolve: %v, want ErrClosed", err)
	}
}
