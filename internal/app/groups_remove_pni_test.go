package app_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupsRemovePNIResolution(t *testing.T) {
	t.Parallel()

	for _, byNumber := range []bool{true, false} {
		t.Run(map[bool]string{true: "number-derived PNI", false: "username"}[byNumber], func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			fake.Directory = []signal.Recipient{{ACI: aliceACI, PNI: carolACI, Number: aliceNumber, Username: "alice.42"}}
			group := fake.GroupInfo[groupID]
			group.Members = group.Members[:1]
			group.Pending = []signal.PendingMember{{Recipient: signal.Recipient{PNI: carolACI}}}
			fake.GroupInfo[groupID] = group

			arg := "@alice.42"
			if byNumber {
				arg = aliceNumber
			}

			got, err := open(t, fake).GroupsRemoveMembers(t.Context(), app.RemoveGroupMembersRequest{
				Group: groupID, Members: []string{arg},
			})
			if byNumber {
				if err != nil || len(got.Pending) != 0 || len(got.Members) != 1 || got.Revision != 5 {
					t.Fatalf("number PNI-only invitation = %+v, %v", got, err)
				}
			} else if !errors.Is(err, signal.ErrInvalidGroupMember) || !reflect.DeepEqual(fake.GroupInfo[groupID], group) {
				t.Fatalf("username revoked PNI invitation: %+v, %v", got, err)
			}
		})
	}
}
