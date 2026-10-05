//go:build cgo || libsignal_go

//nolint:paralleltest // these lifecycle fixtures temporarily replace the shared CDN transport.
package signal_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"google.golang.org/protobuf/proto"
)

//nolint:cyclop,funlen // one install/reopen/failed-reinstall lifecycle verifies atomic persistence.
func TestStickerPackInstallation(t *testing.T) {
	ref := signal.StickerReference{
		PackID:    strings.Repeat("ab", 16),
		PackKey:   make([]byte, 32),
		StickerID: 7,
	}
	image := []byte("GIF89apack-image")
	manifest, _ := proto.Marshal(&signalpb.Pack{
		Title:  new("Animals"),
		Author: new("Artist"),
		Cover: &signalpb.Pack_Sticker{
			Id:          new(uint32(8)),
			ContentType: new(stickerGIFType),
		},
		Stickers: []*signalpb.Pack_Sticker{
			{Id: new(uint32(7)), Emoji: new("cat"), ContentType: new(stickerGIFType)},
		},
	})
	requests := 0
	fail := false

	t.Cleanup(signal.SetSignalTransport(stickerTransport(func(request *http.Request) (*http.Response, error) {
		requests++

		if fail {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}

		data := image
		if strings.HasSuffix(request.URL.Path, "manifest.proto") {
			data = manifest
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(stickerBlob(t, ref.PackKey, data))),
		}, nil
	})))
	dir := seedAccount(t)
	client := openInboxClient(t, dir)

	pack, err := client.InstallStickerPack(t.Context(), ref)
	if err != nil || pack.Title != "Animals" || len(pack.Stickers) != 2 || requests != 3 {
		t.Fatalf("install %+v err %v requests %d", pack, err, requests)
	}

	pack.Stickers[0].Data.Image.Data[0] = 'x'

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openInboxClient(t, dir)
	fail = true

	got, err := client.FetchSticker(t.Context(), ref)
	if err != nil || !bytes.Equal(got.Image.Data, image) || requests != 3 {
		t.Fatalf("offline cache %+v %v requests %d", got, err, requests)
	}

	_, err = client.InstallStickerPack(t.Context(), ref)
	if err != nil || requests != 3 {
		t.Fatalf("idempotent %v requests %d", err, requests)
	}

	wrong := ref
	wrong.PackKey = bytes.Repeat([]byte{1}, 32)

	_, err = client.InstallStickerPack(t.Context(), wrong)
	if !errors.Is(err, signal.ErrStickerNotFound) {
		t.Fatalf("failed reinstall %v", err)
	}

	packs, err := client.StickerPacks(t.Context())
	if err != nil || len(packs) != 1 || !bytes.Equal(packs[0].Reference.PackKey, ref.PackKey) {
		t.Fatalf("preserved %+v %v", packs, err)
	}

	_, err = client.FetchSticker(t.Context(), wrong)
	if !errors.Is(err, signal.ErrStickerNotFound) {
		t.Fatalf("mismatched key reused cache: %v", err)
	}

	other := openInboxClient(t, seedAccount(t))

	packs, err = other.StickerPacks(t.Context())
	if err != nil || len(packs) != 0 {
		t.Fatalf("account isolation %+v %v", packs, err)
	}
}

//nolint:funlen // encrypted malformed/partial pack cases verify no cache record is written.
func TestStickerPackRejectsIncompleteInstall(t *testing.T) {
	ref := signal.StickerReference{
		PackID:  strings.Repeat("cd", 16),
		PackKey: make([]byte, 32),
	}
	first := &signalpb.Pack_Sticker{
		Id:          new(uint32(1)),
		ContentType: new(stickerGIFType),
	}

	second := &signalpb.Pack_Sticker{
		Id:          new(uint32(2)),
		ContentType: new(stickerGIFType),
	}
	for _, test := range []struct {
		name      string
		pack      *signalpb.Pack
		failImage bool
		want      error
	}{
		{
			"missing image",
			&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{first, second}},
			true,
			signal.ErrStickerNotFound,
		},
		{
			"duplicate item",
			&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{first, first}},
			false,
			signal.ErrInvalidSticker,
		},
		{
			"missing item ID",
			&signalpb.Pack{Stickers: []*signalpb.Pack_Sticker{{}}},
			false,
			signal.ErrInvalidSticker,
		},
		{"empty manifest", &signalpb.Pack{}, false, signal.ErrInvalidSticker},
		{
			"invalid cover",
			&signalpb.Pack{
				Cover:    &signalpb.Pack_Sticker{},
				Stickers: []*signalpb.Pack_Sticker{first},
			},
			false,
			signal.ErrInvalidSticker,
		},
		{
			"unsupported content",
			&signalpb.Pack{
				Stickers: []*signalpb.Pack_Sticker{
					{Id: new(uint32(1)), ContentType: new("text/html")},
				},
			},
			false,
			signal.ErrInvalidSticker,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			blob, err := proto.Marshal(test.pack)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(signal.SetSignalTransport(stickerTransport(func(request *http.Request) (*http.Response, error) {
				if test.failImage && strings.HasSuffix(request.URL.Path, "/2") {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				}

				data := []byte("GIF89aimage")
				if strings.HasSuffix(request.URL.Path, "manifest.proto") {
					data = blob
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(stickerBlob(t, ref.PackKey, data))),
				}, nil
			})))
			client := openInboxClient(t, seedAccount(t))

			_, err = client.InstallStickerPack(t.Context(), ref)
			if !errors.Is(err, test.want) {
				t.Fatalf("install %v want %v", err, test.want)
			}

			packs, err := client.StickerPacks(t.Context())
			if err != nil || len(packs) != 0 {
				t.Fatalf("partial pack persisted %+v %v", packs, err)
			}
		})
	}
}

func TestFetchStickerCoverOnly(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{
		PackID:    strings.Repeat("ef", 16),
		PackKey:   make([]byte, 32),
		StickerID: 8,
	}
	image := []byte("GIF89acover")
	pack := &signalpb.Pack{
		Cover: &signalpb.Pack_Sticker{
			Id:          new(uint32(8)),
			Emoji:       new("cover"),
			ContentType: new(stickerGIFType),
		},
		Stickers: []*signalpb.Pack_Sticker{
			{Id: new(uint32(7)), ContentType: new(stickerGIFType)},
		},
	}

	blob, err := proto.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}

	requests := 0
	client := &http.Client{Transport: stickerTransport(func(request *http.Request) (*http.Response, error) {
		requests++

		data := image
		if strings.HasSuffix(request.URL.Path, "manifest.proto") {
			data = blob
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(stickerBlob(t, ref.PackKey, data))),
		}, nil
	})}

	got, err := signal.FetchStickerWithHTTP(t.Context(), ref, client)
	if err != nil || got.Emoji != "cover" || !bytes.Equal(got.Image.Data, image) || requests != 2 {
		t.Fatalf("cover fetch %+v %v requests %d", got, err, requests)
	}
}

//nolint:funlen,cyclop // limit fixtures verify complete-or-absent cache state.
func TestStickerPackLimits(t *testing.T) {
	ref := signal.StickerReference{
		PackID:  strings.Repeat("ee", 16),
		PackKey: make([]byte, 32),
	}

	for _, test := range []struct {
		name      string
		count     int
		coverID   uint32
		imageSize int64
		want      error
	}{
		{"shared cover at limit", 200, 0, 0, nil},
		{"distinct cover at limit", 199, 199, 0, nil},
		{"distinct cover exceeds limit", 200, 200, 0, signal.ErrStickerTooLarge},
		{
			"image response exceeds budget",
			1,
			0,
			(100 << 20) + 65,
			signal.ErrStickerTooLarge,
		},
		{"per-image encrypted limit", 1, 0, (100 << 20) + 1, signal.ErrStickerTooLarge},
		{"cumulative image budget", 2, 0, (100 << 20) - 45, signal.ErrStickerTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			pack := &signalpb.Pack{
				Cover: &signalpb.Pack_Sticker{
					Id:          new(test.coverID),
					ContentType: new(stickerGIFType),
				},
			}
			for index := range test.count {
				pack.Stickers = append(pack.Stickers, &signalpb.Pack_Sticker{
					Id:          new(uint32(index)),
					ContentType: new(stickerGIFType),
				})
			}

			blob, err := proto.Marshal(pack)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(signal.SetSignalTransport(stickerTransport(func(request *http.Request) (*http.Response, error) {
				data := bytes.Repeat([]byte("GIF89alimit"), 10)

				size := test.imageSize
				if test.count == 2 && !strings.HasSuffix(request.URL.Path, "/1") {
					size = 0
				}

				if strings.HasSuffix(request.URL.Path, "manifest.proto") {
					data = blob
					size = 0
				}

				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: size,
					Body:          io.NopCloser(bytes.NewReader(stickerBlob(t, ref.PackKey, data))),
				}, nil
			})))
			client := openInboxClient(t, seedAccount(t))

			_, err = client.InstallStickerPack(t.Context(), ref)
			if !errors.Is(err, test.want) {
				t.Fatalf("limit %v want %v", err, test.want)
			}

			installed, err := client.StickerPacks(t.Context())
			if err != nil || (len(installed) == 1) != (test.want == nil) {
				t.Fatalf("cache after limit %+v %v", installed, err)
			}

			if test.want == nil && len(installed[0].Stickers) != 200 {
				t.Fatal("boundary installation omitted items")
			}
		})
	}
}
