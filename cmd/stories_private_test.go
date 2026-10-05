//nolint:goconst // Independent private-story CLI fixtures.
package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const privateStoryListID = "76543210-1234-4321-8234-123456789abc"

func privateStoriesFake() *signaltest.Fake {
	fake := storiesFake()
	fake.StoryAudienceSnapshots = map[string]signal.StoryAudiences{
		testAccount().ACI: {
			StorageVersion: 42,
			Audiences: []signal.StoryAudience{
				{
					ID: signal.MyStoryID,

					Name: "My Story",

					IsBlockList: true,

					AllowsReplies: true,

					Recipients: []signal.Recipient{{ACI: aliceACI}, {ACI: carolACI}},
				},

				{
					ID: privateStoryListID,

					Name: "Friends",

					AllowsReplies: false,

					Recipients: []signal.Recipient{{ACI: aliceACI}},
				},
			},
		},
	}

	return fake
}

func TestStoriesPrivateSend(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := privateStoriesFake()

			out, err := runSend(t, fake, "",
				"-o", format, "stories", sendCmd, "--distribution-list", privateStoryListID, "-m", "Friends only")
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "stories_private_send_"+format, out)

			sent := fake.Sent()
			if len(sent) != 1 ||
				sent[0].GroupID != "" ||
				sent[0].Story.DistributionListID != privateStoryListID ||
				sent[0].Story.AllowsReplies ||
				len(sent[0].Recipients) != 1 ||
				sent[0].Recipients[0].ACI != aliceACI ||
				len(fake.Receipts()) != 0 {
				t.Fatalf("sent %+v", sent)
			}
		})
	}
}

func TestStoriesMyStoryPartialSync(t *testing.T) {
	t.Parallel()

	fake := privateStoriesFake()
	fake.SendFailures = map[string]error{carolACI: errUnreachable}
	fake.StorySyncErr = errStorySyncRejected

	out, err := runSend(t, fake, "Private\n",
		"-o", formatJSON, "stories", sendCmd, "--my-story", "--stdin", "--no-replies")
	if !errors.Is(err, app.ErrSendFailed) ||
		!strings.Contains(out, "recipient unreachable") ||
		!strings.Contains(out, "transcript rejected") {
		t.Fatalf("%v\n%s", err, out)
	}

	golden(t, "stories_private_partial_json", out)

	if len(fake.Sent()) != 1 ||
		fake.Sent()[0].Story.AllowsReplies ||
		fake.Sent()[0].Story.DistributionListID != signal.MyStoryID {
		t.Fatal("wrong audience or duplicate submission")
	}
}

func TestStoriesAudiences(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := privateStoriesFake()

			out, err := run(t, fake, "-o", format, "stories", "audiences")
			if err != nil {
				t.Fatal(err)
			}

			if format == formatPlain && (!strings.Contains(out, "POLICY") || !strings.Contains(out, "exclusions")) {
				t.Fatalf("missing audience policy: %s", out)
			}

			golden(t, "stories_audiences_"+format, out)

			if len(fake.Sent()) != 0 || len(fake.Uploaded()) != 0 {
				t.Fatal("listing wrote a story")
			}
		})
	}
}

//nolint:funlen // Independent audience flag and snapshot refusal fixtures.
func TestStoriesPrivatePreflight(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		change func(*signaltest.Fake)
		args   []string
	}{
		{
			"unknown list",

			func(*signaltest.Fake) {},

			[]string{"--distribution-list", "99999999-1234-4321-8234-123456789abc"},
		},

		{
			"missing snapshot",

			func(f *signaltest.Fake) { f.StoryAudienceSnapshots = nil },

			[]string{"--my-story"},
		},

		{
			"empty",
			func(f *signaltest.Fake) {
				snapshot := f.StoryAudienceSnapshots[testAccount().ACI]
				snapshot.Audiences[0].Recipients = nil
				f.StoryAudienceSnapshots[testAccount().ACI] = snapshot
			},
			[]string{"--my-story"},
		},

		{
			"both audiences",

			func(*signaltest.Fake) {},

			[]string{"--my-story", "--distribution-list", privateStoryListID},
		},

		{
			"group and private",

			func(*signaltest.Fake) {},

			[]string{"--my-story", "--group", groupID},
		},

		{"invalid UUID", func(*signaltest.Fake) {}, []string{"--distribution-list", "malformed-private-list-id"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fake := privateStoriesFake()
			testCase.change(fake)
			args := append([]string{"stories", sendCmd, "-m", "Private"}, testCase.args...)

			_, err := runSend(t, fake, "", args...)
			if err == nil || len(fake.Sent()) != 0 || len(fake.Uploaded()) != 0 {
				t.Fatalf("err %v sent %+v", err, fake.Sent())
			}
		})
	}
}

func TestStoriesPrivateSelectedAccount(t *testing.T) {
	t.Parallel()

	fake := privateStoriesFake()
	other := signal.Account{ACI: bobACI, Number: "+15550102"}
	fake.Linked = append(fake.Linked, other)
	fake.StoryAudienceSnapshots[other.ACI] = signal.StoryAudiences{
		StorageVersion: 71,

		Audiences: []signal.StoryAudience{{ID: signal.MyStoryID, Recipients: []signal.Recipient{{ACI: carolACI}}}},
	}

	_, err := runSend(t, fake, "", "--account", other.Number, "stories", sendCmd, "--my-story", "-m", "Private")
	if err != nil {
		t.Fatal(err)
	}

	if sent := fake.Sent(); len(sent) != 1 || len(sent[0].Recipients) != 1 || sent[0].Recipients[0].ACI != carolACI {
		t.Fatalf("wrong account's audience: %+v", sent)
	}
}

func TestStoriesPrivatePlainFailure(t *testing.T) {
	t.Parallel()

	fake := privateStoriesFake()
	fake.SendFailures = map[string]error{carolACI: errUnreachable}
	fake.StorySyncErr = errStorySyncRejected

	out, err := runSend(t, fake, "", "stories", sendCmd, "--my-story", "-m", "Private")
	if !errors.Is(err, app.ErrSendFailed) ||
		!strings.Contains(out, "recipient unreachable") ||
		!strings.Contains(out, "transcript rejected") {
		t.Fatalf("%v\n%s", err, out)
	}

	golden(t, "stories_private_failed_plain", out)
}
