//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
)

const storyListID = "76543210-1234-4321-8234-123456789abc"

func audienceRecord(id, name string, excluded bool, members ...string) *signalmeow.DecryptedStorageRecord {
	parsed := uuid.MustParse(id)

	return &signalmeow.DecryptedStorageRecord{
		StorageRecord: &signalpb.StorageRecord{
			Record: &signalpb.StorageRecord_StoryDistributionList{
				StoryDistributionList: &signalpb.StoryDistributionListRecord{
					Identifier: parsed[:],

					Name: name,

					RecipientServiceIds: members,

					IsBlockList: excluded,

					AllowsReplies: true,
				},
			},
		},
	}
}

//nolint:funlen // One projection fixture covers both privacy modes.
func TestStoryAudienceProjection(t *testing.T) {
	t.Parallel()

	self := privateStorySelf
	alice := privateStoryPeer
	bob := "10000000-0000-4000-8000-000000000003"
	contact := func(id string, blocked, sharing bool) *signalmeow.DecryptedStorageRecord {
		return &signalmeow.DecryptedStorageRecord{
			StorageRecord: &signalpb.StorageRecord{
				Record: &signalpb.StorageRecord_Contact{
					Contact: &signalpb.ContactRecord{
						Aci: id, Blocked: blocked, Whitelisted: sharing,
					},
				},
			},
		}
	}
	update := &signalmeow.StorageUpdate{
		Version: 19,

		NewRecords: []*signalmeow.DecryptedStorageRecord{
			contact(self, false, true),

			contact(alice, false, true),

			contact(bob, false, false),

			audienceRecord(signal.MyStoryID, "My Story", true, bob),

			audienceRecord(storyListID, "Friends", false, self, alice, alice),
		},
	}

	got, err := signal.ProjectStoryAudiences(update, self)
	if err != nil {
		t.Fatal(err)
	}

	want := []signal.StoryAudience{
		{
			ID: signal.MyStoryID,

			Name: "My Story",

			IsBlockList: true,

			AllowsReplies: true,

			Recipients: []signal.Recipient{{ACI: alice}},
		},

		{
			ID: storyListID,

			Name: "Friends",

			AllowsReplies: true,

			Recipients: []signal.Recipient{{ACI: alice}},
		},
	}
	if !reflect.DeepEqual(got.Audiences, want) || got.StorageVersion != 19 {
		t.Fatalf("got %+v", got)
	}

	update.NewRecords[3].StorageRecord.GetStoryDistributionList().RecipientServiceIds = nil

	if got.Audiences[0].Recipients[0].ACI != alice {
		t.Fatal("aliases input")
	}
}

func TestStoryAudienceProjectionRefusesIncomplete(t *testing.T) {
	t.Parallel()

	for _, update := range []*signalmeow.StorageUpdate{
		nil,

		{MissingRecords: []string{"unread"}},

		{
			NewRecords: []*signalmeow.DecryptedStorageRecord{audienceRecord(storyListID, "Friends", true)},
		},

		{
			NewRecords: []*signalmeow.DecryptedStorageRecord{
				audienceRecord(storyListID, "Friends", false, "PNI:76543210-1234-4321-8234-123456789abc"),
			},
		},

		{
			NewRecords: []*signalmeow.DecryptedStorageRecord{
				audienceRecord(storyListID, "Friends", false),

				audienceRecord(storyListID, "Duplicate", false),
			},
		},
	} {
		_, err := signal.ProjectStoryAudiences(update, privateStorySelf)
		if !errors.Is(err, signal.ErrStoryAudienceUnavailable) {
			t.Fatalf("got %v for %+v", err, update)
		}
	}
}

func TestStoryAudienceUnavailableContacts(t *testing.T) {
	t.Parallel()

	self := privateStorySelf

	aci := privateStoryPeer
	for _, contact := range []*signalpb.ContactRecord{
		{Aci: aci, Whitelisted: true, Hidden: true},

		{Aci: aci, Whitelisted: true, UnregisteredAtTimestamp: 123},
	} {
		update := &signalmeow.StorageUpdate{
			NewRecords: []*signalmeow.DecryptedStorageRecord{
				{
					StorageRecord: &signalpb.StorageRecord{Record: &signalpb.StorageRecord_Contact{Contact: contact}},
				},

				audienceRecord(signal.MyStoryID, "My Story", true),
			},
		}

		got, err := signal.ProjectStoryAudiences(update, self)
		if err != nil {
			t.Fatal(err)
		}

		if len(got.Audiences[0].Recipients) != 0 {
			t.Fatalf("ineligible contact included: %+v", got)
		}
	}
}

func TestStoryAudienceMalformedRecords(t *testing.T) {
	t.Parallel()

	for _, records := range [][]*signalmeow.DecryptedStorageRecord{
		{nil},

		{{StorageRecord: nil}},

		{audienceRecord(storyListID, "Friends", false, "malformed-story-member")},

		{
			{
				StorageRecord: &signalpb.StorageRecord{
					Record: &signalpb.StorageRecord_StoryDistributionList{
						StoryDistributionList: &signalpb.StoryDistributionListRecord{
							Identifier: []byte{1},
						},
					},
				},
			},
		},
	} {
		_, err := signal.ProjectStoryAudiences(&signalmeow.StorageUpdate{NewRecords: records}, privateStorySelf)
		if !errors.Is(err, signal.ErrStoryAudienceUnavailable) {
			t.Fatalf("got %v", err)
		}
	}
}

//nolint:cyclop // Independent assertions cover send and persistence boundaries.
func TestStoryAudiencePersistenceAndNoFallback(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openSeeded(t, dataDir)
	update := &signalmeow.StorageUpdate{
		Version: 44,

		NewRecords: []*signalmeow.DecryptedStorageRecord{audienceRecord(storyListID, "Friends", false, aliceUser)},
	}
	calls := 0
	fetch := func(context.Context) (*signalmeow.StorageUpdate, error) { calls++; return update, nil }

	got, err := signal.FetchStoryAudiences(t.Context(), client, fetch)
	if err != nil || len(got.Audiences) != 1 {
		t.Fatalf("%+v %v", got, err)
	}

	read := func() string {
		var raw string

		withStore(t, dataDir, func(_ *mstore.Device, data *store.Store) {
			var (
				present bool
				err     error
			)

			raw, present, err = data.Meta(t.Context(), "story_audiences")
			if err != nil || !present {
				t.Fatalf("meta: %v %t", err, present)
			}
		})

		return raw
	}
	previous := read()
	update = &signalmeow.StorageUpdate{Version: 45, MissingRecords: []string{"unavailable-story-record"}}

	_, err = signal.FetchStoryAudiences(t.Context(), client, fetch)
	if !errors.Is(err, signal.ErrStoryAudienceUnavailable) || read() != previous {
		t.Fatalf("partial overwrote snapshot: %v", err)
	}

	failure := errStorageDown

	_, err = signal.FetchStoryAudiences(t.Context(), client, func(context.Context) (*signalmeow.StorageUpdate, error) {
		calls++
		return nil, failure
	})
	if !errors.Is(err, failure) || read() != previous || calls != 3 {
		t.Fatalf("stale fallback or no fresh fetch: %v", err)
	}

	update = &signalmeow.StorageUpdate{Version: 46}

	got, err = signal.FetchStoryAudiences(t.Context(), client, fetch)
	if err != nil || len(got.Audiences) != 0 || read() == previous {
		t.Fatalf("removed list retained: %+v %v", got, err)
	}
}

func TestStoryAudienceBinaryAndDeletion(t *testing.T) {
	t.Parallel()

	self := privateStorySelf
	peer := uuid.MustParse(privateStoryPeer)
	live := audienceRecord(storyListID, "Friends", false)
	live.StorageRecord.GetStoryDistributionList().RecipientServiceIdsBinary = [][]byte{peer[:]}
	deleted := audienceRecord(signal.MyStoryID, "My Story", true)
	deleted.StorageRecord.GetStoryDistributionList().DeletedAtTimestamp = 123

	got, err := signal.ProjectStoryAudiences(
		&signalmeow.StorageUpdate{NewRecords: []*signalmeow.DecryptedStorageRecord{live, deleted}}, self,
	)
	if err != nil ||
		len(got.Audiences) != 1 ||
		len(got.Audiences[0].Recipients) != 1 ||
		got.Audiences[0].Recipients[0].ACI != peer.String() {
		t.Fatalf("%+v %v", got, err)
	}

	live.StorageRecord.GetStoryDistributionList().RecipientServiceIdsBinary = [][]byte{append([]byte{1}, peer[:]...)}

	_, err = signal.ProjectStoryAudiences(
		&signalmeow.StorageUpdate{NewRecords: []*signalmeow.DecryptedStorageRecord{live}}, self,
	)
	if !errors.Is(err, signal.ErrStoryAudienceUnavailable) {
		t.Fatalf("PNI binary accepted: %v", err)
	}
}

func TestStoryAudienceIgnoresContactsWithoutACI(t *testing.T) {
	t.Parallel()

	update := &signalmeow.StorageUpdate{
		NewRecords: []*signalmeow.DecryptedStorageRecord{
			{
				StorageRecord: &signalpb.StorageRecord{
					Record: &signalpb.StorageRecord_Contact{Contact: &signalpb.ContactRecord{Pni: carolPNI, Whitelisted: true}},
				},
			},

			{
				StorageRecord: &signalpb.StorageRecord{
					Record: &signalpb.StorageRecord_Contact{Contact: &signalpb.ContactRecord{E164: aliceE164, AciBinary: []byte{}}},
				},
			},

			audienceRecord(storyListID, "Friends", false, privateStoryPeer),
		},
	}

	got, err := signal.ProjectStoryAudiences(update, privateStorySelf)
	if err != nil || len(got.Audiences) != 1 || len(got.Audiences[0].Recipients) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestStoryAudiencePendingBlock(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	withStore(t, dataDir, func(_ *mstore.Device, data *store.Store) {
		err := data.SetBlockOverride(t.Context(), store.BlockOverride{ACI: aliceUser, Blocked: true, SetAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	})
	client := openSeeded(t, dataDir)

	snapshot, err := signal.FetchStoryAudiences(t.Context(), client,
		func(context.Context) (*signalmeow.StorageUpdate, error) {
			return &signalmeow.StorageUpdate{
				Version:    54,
				NewRecords: []*signalmeow.DecryptedStorageRecord{audienceRecord(storyListID, "Friends", false, aliceUser)},
			}, nil
		})
	if err != nil ||
		len(snapshot.Audiences) != 1 ||
		len(snapshot.Audiences[0].Recipients) != 0 {
		t.Fatalf("local block missed: %+v %v", snapshot, err)
	}
}
