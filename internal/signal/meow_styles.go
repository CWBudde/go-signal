//go:build cgo || libsignal_go

package signal

import "github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"

// addTextStyles retains the mention ranges already on msg and appends independent style ranges.
func addTextStyles(msg *signalpb.DataMessage, styles []TextStyle) error {
	if len(styles) == 0 {
		return nil
	}

	err := CheckTextStyles(msg.GetBody(), styles)
	if err != nil {
		return err
	}

	wireStyles := map[Style]signalpb.BodyRange_Style{
		StyleBold: signalpb.BodyRange_BOLD, StyleItalic: signalpb.BodyRange_ITALIC,
		StyleSpoiler: signalpb.BodyRange_SPOILER, StyleStrikethrough: signalpb.BodyRange_STRIKETHROUGH,
		StyleMonospace: signalpb.BodyRange_MONOSPACE,
	}
	for _, style := range styles {
		msg.BodyRanges = append(msg.BodyRanges, &signalpb.BodyRange{
			Start: new(style.Start), Length: new(style.Length),
			AssociatedValue: &signalpb.BodyRange_Style_{Style: wireStyles[style.Style]},
		})
	}

	return nil
}
