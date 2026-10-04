package app_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const firstRuneBold = "0:1:bold"

func TestSendStylesWithMentionAndEdit(t *testing.T) {
	t.Parallel()

	for _, edit := range []uint64{0, sentAt - 1} {
		fake := directory()
		fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: aliceACI}}}

		_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
			Recipients: []string{aliceNumber, "group:" + groupID}, Body: "😀 @{self} café",
			Styles:     []string{"0:9:bold", "3:1:italic", "5:4:monospace", "5:2:spoiler", "6:2:strikethrough"},
			EditTarget: edit,
		})
		if err != nil {
			t.Fatal(err)
		}

		want := []signal.TextStyle{
			{Start: 0, Length: 9, Style: signal.StyleBold},
			{Start: 3, Length: 1, Style: signal.StyleItalic},
			{Start: 5, Length: 4, Style: signal.StyleMonospace},
			{Start: 5, Length: 2, Style: signal.StyleSpoiler},
			{Start: 6, Length: 2, Style: signal.StyleStrikethrough},
		}

		sent := fake.Sent()
		if len(sent) != 2 {
			t.Fatalf("sent %+v, want direct and group requests", sent)
		}

		for _, req := range sent {
			if req.Body != "😀 \uFFFC café" || !reflect.DeepEqual(req.Styles, want) ||
				len(req.Mentions) != 1 || req.Mentions[0].Start != 3 || req.EditTarget != edit {
				t.Errorf("sent %+v, want styles %+v with mention and edit %d", req, want, edit)
			}
		}
	}
}

func TestInvalidStylesBeforeConnect(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{
		"bold", "0:1", "0:1:bold:extra", "-1:1:bold", "0:-1:bold", "0:0:bold", "0:1:unknown",
		"4294967296:1:bold", "0:4294967296:bold", "4294967295:2:bold", "0:10:bold",
		"1:1:bold", firstRuneBold, "8:2:bold", "9:1:bold", "0:1:",
	} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
				Recipients: []string{aliceNumber}, Body: "😀 @{self} café", Styles: []string{arg},
				Attachments: []string{"missing-attachment"},
			})
			if !errors.Is(err, signal.ErrInvalidStyle) || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Errorf("send = %v, connects %v, sent %+v", err, fake.Connects(), fake.Sent())
			}
		})
	}
}

func TestStylesNeedNonblankBody(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"", "  ", "\xff"} {
		fake := directory()

		_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
			Recipients: []string{aliceNumber}, Body: body, Styles: []string{firstRuneBold},
			Attachments: []string{"missing-attachment"},
		})
		if !errors.Is(err, signal.ErrInvalidStyle) || len(fake.Connects()) != 0 {
			t.Errorf("body %q: error %v, connects %v", body, err, fake.Connects())
		}
	}
}

func TestStyledNoteToSelfWithAttachmentAndQuote(t *testing.T) {
	t.Parallel()

	fake := directory()
	attachment := writeFile(t, "styled.txt", []byte("attachment content"))

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{app.SelfRecipient}, Body: "styled", Styles: []string{"0:6:bold"},
		Attachments: []string{attachment}, Quote: selfQuote, QuoteText: "original text",
	})
	if err != nil {
		t.Fatal(err)
	}

	sent := fake.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %+v, want one note to self", sent)
	}

	req := sent[0]
	if len(req.Recipients) != 1 || req.Recipients[0].ACI != testAccount().ACI ||
		len(req.Attachments) != 1 || req.Quote == nil || req.Quote.Text != "original text" ||
		!reflect.DeepEqual(req.Styles, []signal.TextStyle{{Length: 6, Style: signal.StyleBold}}) {
		t.Errorf("styled note to self lost content: %+v", req)
	}
}
