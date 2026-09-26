package output

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// maxTitleWidth bounds group titles in the plain `groups list` table.
const maxTitleWidth = 40

// GroupJSON is the "group" object of docs/json.md. The MCP server can return it as structured
// content. The master key is left out on purpose: it grants access to the group.
type GroupJSON struct {
	ID                string                 `json:"id"`
	Title             string                 `json:"title"`
	Description       string                 `json:"description,omitempty"`
	Revision          uint32                 `json:"revision"`
	Membership        string                 `json:"membership"`
	Role              string                 `json:"role,omitempty"`
	TimerSeconds      int64                  `json:"timerSeconds"`
	AnnouncementsOnly bool                   `json:"announcementsOnly"`
	Members           []GroupMemberJSON      `json:"members,omitzero"`
	Pending           []PendingMemberJSON    `json:"pending,omitzero"`
	Requesting        []RequestingMemberJSON `json:"requesting,omitzero"`
	LeftAt            time.Time              `json:"leftAt,omitzero"`
	Error             string                 `json:"error,omitempty"`
}

// GroupMemberJSON is an entry of GroupJSON.Members: a recipient plus the member's role.
type GroupMemberJSON struct {
	recipientJSON

	Role             string `json:"role"`
	JoinedAtRevision uint32 `json:"joinedAtRevision"`
}

// PendingMemberJSON is an entry of GroupJSON.Pending: an invited recipient.
type PendingMemberJSON struct {
	recipientJSON

	Role      string        `json:"role"`
	AddedBy   recipientJSON `json:"addedBy,omitzero"`
	InvitedAt time.Time     `json:"invitedAt,omitzero"`
}

// RequestingMemberJSON is an entry of GroupJSON.Requesting: a recipient who asked to join.
type RequestingMemberJSON struct {
	recipientJSON

	RequestedAt time.Time `json:"requestedAt,omitzero"`
}

type groupsDoc struct {
	Version int         `json:"version"`
	Groups  []GroupJSON `json:"groups"`
}

type groupDoc struct {
	Version int       `json:"version"`
	Group   GroupJSON `json:"group"`
}

// LeftGroupJSON is the "left" object of docs/json.md.
type LeftGroupJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Membership is what we gave up: member, pending (an invitation) or requesting.
	Membership string          `json:"membership"`
	Revision   uint32          `json:"revision"`
	Promoted   []recipientJSON `json:"promoted"`
	LeftAt     time.Time       `json:"leftAt,omitzero"`
}

type leftDoc struct {
	Version int           `json:"version"`
	Left    LeftGroupJSON `json:"left"`
}

// NewGroupJSON converts group to its JSON form. A group that couldn't be fetched (group.Err) has
// no member lists.
func NewGroupJSON(group signal.Group) GroupJSON {
	return newGroupJSON(group, bareRecipient)
}

// newGroupJSON converts group, with rcpt converting its members.
func newGroupJSON(group signal.Group, rcpt func(signal.Recipient) recipientJSON) GroupJSON {
	out := GroupJSON{
		ID:                group.ID,
		Title:             group.Title,
		Description:       group.Description,
		Revision:          group.Revision,
		Membership:        group.Membership.String(),
		TimerSeconds:      int64(group.Timer / time.Second),
		AnnouncementsOnly: group.AnnouncementsOnly,
		LeftAt:            utc(group.LeftAt),
	}

	if group.Role != signal.GroupRoleUnknown {
		out.Role = group.Role.String()
	}

	if group.Err != nil {
		out.Error = group.Err.Error()

		return out
	}

	out.Members = make([]GroupMemberJSON, 0, len(group.Members))
	for _, member := range group.Members {
		out.Members = append(out.Members, GroupMemberJSON{
			recipientJSON:    rcpt(member.Recipient),
			Role:             member.Role.String(),
			JoinedAtRevision: member.JoinedAtRevision,
		})
	}

	out.Pending = make([]PendingMemberJSON, 0, len(group.Pending))
	for _, pending := range group.Pending {
		out.Pending = append(out.Pending, PendingMemberJSON{
			recipientJSON: rcpt(pending.Recipient),
			Role:          pending.Role.String(),
			AddedBy:       rcpt(pending.AddedBy),
			InvitedAt:     utc(pending.InvitedAt),
		})
	}

	out.Requesting = make([]RequestingMemberJSON, 0, len(group.Requesting))
	for _, requesting := range group.Requesting {
		out.Requesting = append(out.Requesting, RequestingMemberJSON{
			recipientJSON: rcpt(requesting.Recipient),
			RequestedAt:   utc(requesting.RequestedAt),
		})
	}

	return out
}

// NewLeftGroupJSON converts the result of leaving a group to its JSON form.
func NewLeftGroupJSON(res signal.LeaveResult) LeftGroupJSON {
	return newLeftGroupJSON(res, bareRecipient)
}

// newLeftGroupJSON converts res, with rcpt converting the promoted members.
func newLeftGroupJSON(res signal.LeaveResult, rcpt func(signal.Recipient) recipientJSON) LeftGroupJSON {
	out := LeftGroupJSON{
		ID:         res.Group.ID,
		Title:      res.Group.Title,
		Membership: res.Group.Membership.String(),
		Revision:   res.Revision,
		Promoted:   make([]recipientJSON, 0, len(res.Promoted)),
		LeftAt:     utc(res.Group.LeftAt),
	}

	for _, promoted := range res.Promoted {
		out.Promoted = append(out.Promoted, rcpt(promoted))
	}

	return out
}

// Groups prints the known groups (`groups list`): ID, title, member count and our role.
func (p *Printer) Groups(groups []signal.Group) error {
	if p.format == JSON {
		doc := groupsDoc{Version: SchemaVersion, Groups: make([]GroupJSON, 0, len(groups))}
		for _, group := range groups {
			doc.Groups = append(doc.Groups, newGroupJSON(group, p.recipient))
		}

		return p.writeJSON(doc)
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "ID\tTITLE\tMEMBERS\tROLE")

	for _, group := range groups {
		members := "-"
		if group.Err == nil {
			members = strconv.Itoa(len(group.Members))
		}

		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
			group.ID, orDash(truncate(oneLine(group.Title), maxTitleWidth)), members, ourRole(group))
	}

	return flush(table)
}

// Group prints one group (`groups show`): its details, then the members, pending (invited) and
// requesting members.
func (p *Printer) Group(group signal.Group) error {
	if p.format == JSON {
		return p.writeJSON(groupDoc{Version: SchemaVersion, Group: newGroupJSON(group, p.recipient)})
	}

	table := tabwriter.NewWriter(p.w, 0, 0, 1, ' ', 0)
	fmt.Fprintf(table, "ID:\t%s\n", group.ID)
	fmt.Fprintf(table, "Title:\t%s\n", orDash(oneLine(group.Title)))
	fmt.Fprintf(table, "Description:\t%s\n", orDash(oneLine(group.Description)))
	fmt.Fprintf(table, "Revision:\t%d\n", group.Revision)
	fmt.Fprintf(table, "Our role:\t%s\n", ourRole(group))
	fmt.Fprintf(table, "Disappearing messages:\t%s\n", timerText(group.Timer))
	fmt.Fprintf(table, "Who can send:\t%s\n", choose(group.AnnouncementsOnly, "only admins", "all members"))

	err := flush(table)
	if err != nil {
		return err
	}

	table = tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintf(table, "\nMembers (%d):\n", len(group.Members))

	for _, member := range group.Members {
		fmt.Fprintf(table, "  %s\t%s\n", p.who(member.Recipient), member.Role)
	}

	if len(group.Pending) > 0 {
		fmt.Fprintf(table, "\nInvited (%d):\n", len(group.Pending))

		for _, pending := range group.Pending {
			fmt.Fprintf(table, "  %s\t%s\tinvited by %s\t%s\n",
				p.who(pending.Recipient), pending.Role, p.who(pending.AddedBy), p.dateTime(pending.InvitedAt))
		}
	}

	if len(group.Requesting) > 0 {
		fmt.Fprintf(table, "\nRequesting to join (%d):\n", len(group.Requesting))

		for _, requesting := range group.Requesting {
			fmt.Fprintf(table, "  %s\t%s\n", p.who(requesting.Recipient), p.dateTime(requesting.RequestedAt))
		}
	}

	return flush(table)
}

// LeftGroup confirms `groups leave`.
func (p *Printer) LeftGroup(res signal.LeaveResult) error {
	if p.format == JSON {
		return p.writeJSON(leftDoc{Version: SchemaVersion, Left: newLeftGroupJSON(res, p.recipient)})
	}

	group := res.Group
	name := strconv.Quote(group.Title) + " (" + group.ID + ")"

	var out strings.Builder

	switch group.Membership {
	case signal.MembershipPending:
		out.WriteString("Declined the invitation to " + name + ".")
	case signal.MembershipRequesting:
		out.WriteString("Cancelled the request to join " + name + ".")
	case signal.MembershipMember, signal.MembershipNone:
		out.WriteString("Left " + name + ".")
	}

	for _, promoted := range res.Promoted {
		out.WriteString("\nPromoted " + p.who(promoted) + " to admin.")
	}

	return p.writeLine(out.String())
}

// ourRole describes how we belong to group in plain output.
func ourRole(group signal.Group) string {
	switch {
	case group.Membership == signal.MembershipMember:
		return group.Role.String()
	case group.Membership == signal.MembershipPending:
		return "invited"
	case group.Membership == signal.MembershipRequesting:
		return "requesting"
	case !group.LeftAt.IsZero():
		return "left"
	default:
		return "not a member"
	}
}

// timerText formats a disappearing messages timer in the largest unit that fits exactly, e.g.
// "1w", "8h" or "90s"; zero is "off".
func timerText(timer time.Duration) string {
	if timer <= 0 {
		return "off"
	}

	const (
		day  = 24 * time.Hour
		week = 7 * day
	)

	for _, unit := range []struct {
		size time.Duration
		name string
	}{{week, "w"}, {day, "d"}, {time.Hour, "h"}, {time.Minute, "m"}} {
		if timer%unit.size == 0 {
			return strconv.FormatInt(int64(timer/unit.size), 10) + unit.name
		}
	}

	return strconv.FormatInt(int64(timer/time.Second), 10) + "s"
}
