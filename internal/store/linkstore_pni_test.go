//go:build cgo && !purego

package store_test

import (
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

func TestLinkStoreDeviceByPNI(t *testing.T) {
	t.Parallel()

	links := openDir(t, io.Discard).NewLinkStore(zerolog.Nop())
	t.Cleanup(func() { _ = links.Close() })

	data := newDevice(t, uuid.MustParse(testACI))

	_, err := links.DeviceByPNI(t.Context(), data.PNI)
	if err == nil {
		t.Fatal("lookup before PutDevice should fail")
	}

	err = links.PutDevice(t.Context(), data)
	if err != nil {
		t.Fatalf("put device: %v", err)
	}

	device, err := links.DeviceByPNI(t.Context(), data.PNI)
	if err != nil || device == nil || device.ACI != data.ACI {
		t.Fatalf("device = %+v, %v; want the one stored", device, err)
	}

	device, err = links.DeviceByPNI(t.Context(), uuid.New())
	if err != nil || device != nil {
		t.Errorf("unknown PNI: device = %+v, %v; want none", device, err)
	}
}
