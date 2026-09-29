package signal

import (
	"fmt"

	"github.com/google/uuid"
)

// CreateGroupOptions configures a new group. The creator is automatically its administrator;
// ordinary members can edit attributes and add members, and invite links are disabled.
type CreateGroupOptions struct {
	Title string
	// Members must have ACIs. Duplicates and our own ACI are ignored; an empty list is allowed.
	Members []Recipient
}

// Check validates the title and resolved member identifiers before any server mutation.
func (o CreateGroupOptions) Check() error {
	err := ValidateGroupTitle(o.Title)
	if err != nil {
		return err
	}

	for _, member := range o.Members {
		id, err := uuid.Parse(member.ACI)
		if err != nil || id == uuid.Nil {
			return fmt.Errorf("%w: group member %q needs a nonzero ACI", ErrUnresolvable, member.ACI)
		}
	}

	return nil
}

// UniqueMembers returns canonical ACIs in input order, excluding self and duplicates.
// Call Check first.
func (o CreateGroupOptions) UniqueMembers(self string) []Recipient {
	seen := map[uuid.UUID]bool{uuid.MustParse(self): true}
	members := make([]Recipient, 0, len(o.Members))

	for _, member := range o.Members {
		id := uuid.MustParse(member.ACI)
		if !seen[id] {
			seen[id] = true
			member.ACI = id.String()
			members = append(members, member)
		}
	}

	return members
}
