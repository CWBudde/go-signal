//go:build cgo || libsignal_go

package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

const storyAudiencesMetaKey = "story_audiences"

type storyContacts struct {
	connections map[string]bool
	blocked     map[string]bool
}

func projectStoryAudiences(update *signalmeow.StorageUpdate, self string) (StoryAudiences, error) {
	if update == nil || len(update.MissingRecords) != 0 {
		return StoryAudiences{}, ErrStoryAudienceUnavailable
	}

	contacts, err := storyContactsFrom(update)
	if err != nil {
		return StoryAudiences{}, err
	}

	result := StoryAudiences{StorageVersion: update.Version, Audiences: []StoryAudience{}}
	seen := make(map[string]bool)

	for _, record := range update.NewRecords {
		list := record.StorageRecord.GetStoryDistributionList()
		if list == nil {
			continue
		}

		identifier, err := uuid.FromBytes(list.GetIdentifier())
		if err != nil || seen[identifier.String()] {
			return StoryAudiences{}, ErrStoryAudienceUnavailable
		}

		seen[identifier.String()] = true

		if list.GetDeletedAtTimestamp() != 0 {
			continue
		}

		audience, err := projectStoryList(list, identifier.String(), self, contacts)
		if err != nil {
			return StoryAudiences{}, err
		}

		result.Audiences = append(result.Audiences, audience)
	}

	slices.SortFunc(result.Audiences, func(a, b StoryAudience) int { return strings.Compare(a.ID, b.ID) })

	return result, nil
}

func storyContactsFrom(update *signalmeow.StorageUpdate) (storyContacts, error) {
	contacts := storyContacts{connections: make(map[string]bool), blocked: make(map[string]bool)}

	for _, record := range update.NewRecords {
		if record == nil || record.StorageRecord == nil {
			return storyContacts{}, ErrStoryAudienceUnavailable
		}

		contact := record.StorageRecord.GetContact()
		if contact == nil {
			continue
		}

		if !storyContactHasACI(contact) {
			continue
		}

		identifier, err := signalmeow.ParseStringOrBinaryUUID(contact.GetAci(), contact.GetAciBinary())
		if err != nil {
			return storyContacts{}, ErrStoryAudienceUnavailable
		}

		if identifier == uuid.Nil {
			continue
		} // PNI-only contacts cannot receive ACI stories.

		key := identifier.String()
		contacts.blocked[key] = contacts.blocked[key] || storyContactUnavailable(contact)
		contacts.connections[key] = contacts.connections[key] || storyContactConnection(contact)
	}

	return contacts, nil
}

func projectStoryList(
	list *signalpb.StoryDistributionListRecord, identifier, self string, contacts storyContacts,
) (StoryAudience, error) {
	if list.GetIsBlockList() && identifier != MyStoryID {
		return StoryAudience{}, ErrStoryAudienceUnavailable
	}

	members, err := storyListMembers(list)
	if err != nil {
		return StoryAudience{}, err
	}

	audience := StoryAudience{
		ID:            identifier,
		Name:          list.GetName(),
		IsBlockList:   list.GetIsBlockList(),
		AllowsReplies: list.GetAllowsReplies(),
		Recipients:    []Recipient{},
	}
	if identifier == MyStoryID {
		audience.Name = "My Story"
	}

	targets := storyListTargets(audience.IsBlockList, contacts.connections, members)
	for aci := range targets {
		if aci != self && !contacts.blocked[aci] {
			audience.Recipients = append(audience.Recipients, Recipient{ACI: aci})
		}
	}

	slices.SortFunc(audience.Recipients, func(a, b Recipient) int { return strings.Compare(a.ACI, b.ACI) })

	return audience, nil
}

func storyListMembers(list *signalpb.StoryDistributionListRecord) (map[string]bool, error) {
	members := make(map[string]bool)
	// Binary IDs supersede the legacy string representation, as in the official clients.
	if len(list.GetRecipientServiceIdsBinary()) != 0 {
		for _, raw := range list.GetRecipientServiceIdsBinary() {
			identifier, err := uuid.FromBytes(raw)
			if err != nil || identifier == uuid.Nil {
				return nil, ErrStoryAudienceUnavailable
			}

			members[identifier.String()] = true
		}

		return members, nil
	}

	for _, raw := range list.GetRecipientServiceIds() {
		identifier, err := uuid.Parse(raw)
		if err != nil || identifier == uuid.Nil || identifier.String() != raw {
			return nil, ErrStoryAudienceUnavailable
		}

		members[raw] = true
	}

	return members, nil
}

// StoryAudiences reads a fresh complete phone-defined audience snapshot. It never falls
// back to persisted data when storage fails, preventing sends to an obsolete audience.
func (c *meowClient) StoryAudiences(ctx context.Context) (StoryAudiences, error) {
	if !c.begin(&c.sending) {
		return StoryAudiences{}, ErrClosed
	}
	defer c.sending.Done()

	if c.cancelLoops == nil {
		return StoryAudiences{}, ErrNotConnected
	}

	err := c.connectionLost()
	if err != nil {
		return StoryAudiences{}, err
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	return c.freshStoryAudiences(ctx, c.connDevice.ACI.String(),
		func(ctx context.Context) (*signalmeow.StorageUpdate, error) {
			return c.fetchStorage(c.zlog.WithContext(ctx), cli, 0)
		})
}

func (c *meowClient) storeStoryAudiences(
	ctx context.Context, update *signalmeow.StorageUpdate, self string,
) (StoryAudiences, error) {
	snapshot, err := projectStoryAudiences(update, self)
	if err != nil {
		return StoryAudiences{}, err
	}

	pending, err := c.pendingOverrides(ctx, time.Now())
	if err != nil {
		return StoryAudiences{}, err
	}

	for i := range snapshot.Audiences {
		snapshot.Audiences[i].Recipients = slices.DeleteFunc(snapshot.Audiences[i].Recipients, func(r Recipient) bool {
			return pending[r.ACI]
		})
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return StoryAudiences{}, fmt.Errorf("encode story audiences: %w", err)
	}

	err = c.data.SetMeta(ctx, storyAudiencesMetaKey, string(raw))
	if err != nil {
		return StoryAudiences{}, fmt.Errorf("store story audiences: %w", err)
	}

	return snapshot, nil
}

func (c *meowClient) freshStoryAudiences(
	ctx context.Context, self string, fetch func(context.Context) (*signalmeow.StorageUpdate, error),
) (StoryAudiences, error) {
	update, err := fetch(ctx)
	if err != nil {
		return StoryAudiences{}, fmt.Errorf("story audiences: %w", err)
	}

	return c.storeStoryAudiences(ctx, update, self)
}

func storyContactUnavailable(contact *signalpb.ContactRecord) bool {
	return contact.GetBlocked() || contact.GetHidden() || contact.GetUnregisteredAtTimestamp() != 0
}

func storyContactConnection(contact *signalpb.ContactRecord) bool {
	return contact.GetWhitelisted() ||
		contact.GetSystemGivenName() != "" ||
		contact.GetSystemFamilyName() != "" ||
		contact.GetSystemNickname() != ""
}

func storyListTargets(exclusions bool, connections, members map[string]bool) map[string]bool {
	if !exclusions {
		return members
	}

	targets := make(map[string]bool)

	for aci, connection := range connections {
		if connection && !members[aci] {
			targets[aci] = true
		}
	}

	return targets
}

func storyContactHasACI(contact *signalpb.ContactRecord) bool {
	return contact.GetAci() != "" || len(contact.GetAciBinary()) != 0
}
