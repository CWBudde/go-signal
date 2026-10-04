package output

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/cwbudde/go-signal/internal/signal"
)

// mentionText expands ranges using UTF-16 boundaries in the original text. Invalid or
// overlapping ranges leave text intact. The caller escapes the complete result for display.
func (p *Printer) mentionText(body string, mentions []signal.Mention) string {
	if len(mentions) == 0 {
		return body
	}

	boundaries := make(map[uint64]int)

	var units uint64
	for pos, char := range body {
		boundaries[units] = pos
		units += uint64(utf16.RuneLen(char)) //nolint:gosec // range yields valid Unicode scalar values
	}

	boundaries[units] = len(body)

	ordered := slices.Clone(mentions)
	slices.SortStableFunc(ordered, func(a, b signal.Mention) int { return cmp.Compare(a.Start, b.Start) })

	var out strings.Builder

	last := 0

	for _, mention := range ordered {
		start, startOK := boundaries[uint64(mention.Start)]

		end, endOK := boundaries[uint64(mention.Start)+uint64(mention.Length)]
		if mention.Length == 0 || mention.Recipient.IsZero() || !startOK || !endOK || start < last {
			continue
		}

		out.WriteString(body[last:start])
		out.WriteByte('@')
		out.WriteString(p.mentionName(mention.Recipient))

		last = end
	}

	out.WriteString(body[last:])

	return out.String()
}

func (p *Printer) mentionName(recipient signal.Recipient) string {
	if p.names.IsSelf(recipient) {
		return self
	}

	if name := p.names.Label(recipient); name != "" {
		return name
	}

	return recipient.String()
}
