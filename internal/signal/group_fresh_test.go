//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

func TestGroupFetchCannotAuthorizeFromCachedState(t *testing.T) { //nolint:cyclop,funlen,gocognit // fresh state
	t.Parallel()

	for _, useMasterKey := range []bool{false, true} {
		name := "group ID"
		if useMasterKey {
			name = "master key"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dataDir := seedAccount(t)
			masterKey := base64.StdEncoding.EncodeToString(randomKey(t))
			groupID := storeGroupKey(t, dataDir, masterKey)
			client := openOffline(t, dataDir, signal.SendOnly())
			gid := types.GroupIdentifier(groupID)
			cached := rawGroup(time.Now())
			cached.GroupIdentifier = gid
			cached.GroupMasterKey = types.SerializedGroupMasterKey(masterKey)
			cache := signalmeow.NewGroupCache(libsignalgo.NewACIServiceID(uuid.MustParse(seededACI)))
			seedGroupCache(cache, cached)

			// Resolution still uses the real account store. The fresh fetch's key loader
			// fails before any network request; cached retrieval would skip that failure.
			cli := &signalmeow.Client{
				GroupCache: cache,
				Store:      &mstore.Device{GroupStore: failedGroupFetchStore{}},
			}
			t.Cleanup(signal.InstallGroupClient(client, cli))

			groups, err := client.Groups(t.Context())
			if err != nil || len(groups) != 1 || groups[0].Role != signal.GroupRoleAdmin {
				t.Fatalf("cached group fixture: %+v, %v", groups, err)
			}

			ref := groupID
			if useMasterKey {
				ref = masterKey
			}

			for _, description := range []string{"fresh", cached.Description} {
				seedGroupCache(cache, cached)

				_, updateErr := client.UpdateGroup(t.Context(), ref, signal.GroupUpdate{Description: new(description)})
				if !errors.Is(updateErr, errTest) {
					t.Fatalf("UpdateGroup authorized cached state: %v", updateErr)
				}
			}

			for _, role := range []signal.GroupRole{signal.GroupRoleAdmin, signal.GroupRoleMember} {
				seedGroupCache(cache, cached)

				_, roleErr := client.SetGroupMemberRole(t.Context(), ref,
					[]signal.Recipient{{ACI: memberACI}}, role)
				if !errors.Is(roleErr, errTest) {
					t.Fatalf("SetGroupMemberRole authorized cached state: %v", roleErr)
				}
			}

			for _, banned := range []bool{true, false} {
				seedGroupCache(cache, cached)

				_, banErr := client.SetGroupBanned(t.Context(), ref, []signal.Recipient{{ACI: memberACI}}, banned)
				if !errors.Is(banErr, errTest) {
					t.Fatalf("SetGroupBanned authorized cached state: %v", banErr)
				}
			}

			// Restore the stale fixture so Group independently proves its fresh-fetch behavior.
			seedGroupCache(cache, cached)

			group, err := client.Group(t.Context(), ref)
			if !errors.Is(err, errTest) {
				t.Fatalf("Group returned cached authorization %+v, %v; want current fetch error %v", group, err, errTest)
			}
		})
	}
}

type failedGroupFetchStore struct {
	mstore.GroupStore
}

func (failedGroupFetchStore) MasterKeyFromGroupIdentifier(context.Context,
	types.GroupIdentifier,
) (types.SerializedGroupMasterKey, error) {
	return "", errTest
}

// seedGroupCache populates the pinned fork's real private cache before it is used by any
// goroutine. Its public Put requires production-signed endorsements, unavailable locally.
// Reflection is confined to fixture setup; retrieval, cache eligibility and eviction are real.
func seedGroupCache(cache *signalmeow.GroupCache, group *signalmeow.Group) {
	field := reflect.ValueOf(cache).Elem().FieldByName("data")
	data := reflect.NewAt(field.Type(), field.Addr().UnsafePointer()).Elem()
	entry := reflect.New(data.Type().Elem().Elem())
	entry.Elem().FieldByName("Group").Set(reflect.ValueOf(group))
	entry.Elem().FieldByName("SendEndorsementCache").Set(reflect.ValueOf(&signalmeow.SendEndorsementCache{
		Expiration: time.Now().Add(time.Hour),
	}))
	data.SetMapIndex(reflect.ValueOf(group.GroupIdentifier), entry)
}
