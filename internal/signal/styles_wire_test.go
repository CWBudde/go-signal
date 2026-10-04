//go:build cgo || libsignal_go

package signal_test

import (
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestDataMessageStylesAndMentions(t *testing.T) {
	t.Parallel()

	req := signal.SendRequest{
		Body:     "😀 \uFFFC café",
		Mentions: []signal.Mention{{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: sendACI}}},
		Styles: []signal.TextStyle{
			{Start: 0, Length: 9, Style: signal.StyleBold},
			{Start: 3, Length: 1, Style: signal.StyleItalic},
			{Start: 5, Length: 4, Style: signal.StyleSpoiler},
			{Start: 5, Length: 2, Style: signal.StyleStrikethrough},
			{Start: 6, Length: 2, Style: signal.StyleMonospace},
		},
	}

	msg, err := signal.DataMessage(req, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	var decoded signalpb.DataMessage

	err = proto.Unmarshal(encoded, &decoded)
	if err != nil {
		t.Fatal(err)
	}

	ranges := decoded.GetBodyRanges()

	aci := uuid.MustParse(sendACI)
	if len(ranges) != 6 || !reflect.DeepEqual(ranges[0].GetMentionAciBinary(), aci[:]) {
		t.Fatalf("body ranges = %v, want mention and five styles", ranges)
	}

	for i, style := range []signalpb.BodyRange_Style{
		signalpb.BodyRange_BOLD, signalpb.BodyRange_ITALIC,
		signalpb.BodyRange_SPOILER, signalpb.BodyRange_STRIKETHROUGH, signalpb.BodyRange_MONOSPACE,
	} {
		got, want := ranges[i+1], req.Styles[i]
		if got.GetStyle() != style || got.GetStart() != want.Start || got.GetLength() != want.Length {
			t.Errorf("range = %v, want %+v (%v)", got, want, style)
		}
	}
}

func TestStyledEditEnvelope(t *testing.T) {
	t.Parallel()

	msg, err := signal.DataMessage(signal.SendRequest{
		Body: "styled replacement", Styles: []signal.TextStyle{{Length: 6, Style: signal.StyleBold}},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	edit := signal.WrapOutgoing(msg, 123)
	if edit.GetEditMessage().GetTargetSentTimestamp() != 123 ||
		!proto.Equal(edit.GetEditMessage().GetDataMessage(), msg) {
		t.Errorf("edit lost replacement body ranges: %v", edit)
	}
}
