package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	pollYes        = "Yes"
	pollTimeoutArg = "timeout"
	pollQuestion   = "Lunch?"
	pollOptionsArg = "optionIndexes"
	pollDurableArg = "durable"
)

const (
	pollCreateTool   = "poll_create"
	pollVoteTool     = "poll_vote"
	pollCloseTool    = "poll_close"
	pollShowTool     = "poll_show"
	pollRecipientArg = "recipient"
	pollTargetArg    = "target"
)

//nolint:cyclop,funlen // verifies lifecycle outcomes and absence of unintended writes.
func TestMCPPollLifecycle(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, aliceACI))

	var created struct {
		Operation       string
		TargetAuthor    struct{ ACI string }
		TargetTimestamp uint64
	}
	call(t,
		session,
		pollCreateTool,
		map[string]any{
			pollRecipientArg: aliceACI,
			"question":       pollQuestion,
			"options": []string{
				pollYes,
				"No",
			},
		},
		&created)

	if created.Operation != "create" || created.TargetAuthor.ACI != testAccount().ACI || created.TargetTimestamp == 0 {
		t.Fatalf("creation %+v", created)
	}

	target := aliceACI + ":1000"

	var vote struct {
		VoteCount     uint32
		OptionIndexes []uint32
	}
	call(t,
		session,
		pollVoteTool,
		map[string]any{
			pollRecipientArg: aliceACI,
			pollTargetArg:    target,
			pollOptionsArg:   []uint32{0},
		},
		&vote)

	if vote.VoteCount != 1 || len(vote.OptionIndexes) != 1 {
		t.Fatalf("vote %+v", vote)
	}

	call(t, session, pollVoteTool, map[string]any{pollRecipientArg: aliceACI, pollTargetArg: target, "clear": true}, &vote)

	if vote.VoteCount != 2 || len(vote.OptionIndexes) != 0 {
		t.Fatalf("withdrawal %+v", vote)
	}

	call(t,
		session,
		pollCloseTool,
		map[string]any{
			pollRecipientArg: aliceACI,
			timestampArg:     created.TargetTimestamp,
		},
		&struct{}{})

	sent := fake.Sent()
	if len(sent) != 4 ||
		sent[0].PollCreate == nil ||
		sent[1].PollVote == nil ||
		sent[2].PollVote == nil ||
		sent[3].PollClose == nil {
		t.Fatalf("requests %+v", sent)
	}

	msg := photoMessage(1000)
	msg.Attachments = nil
	msg.Poll = &signal.Poll{Question: pollQuestion, Options: []string{pollYes, "No"}}
	waitForPush(t, session, fake, msg)

	var state struct {
		CreationPresent      bool
		Completeness, Source string
		Observations         int
	}
	call(t,
		session,
		pollShowTool,
		map[string]any{
			pollRecipientArg: aliceACI,
			pollTargetArg:    target,
			pollDurableArg:   true,
		},
		&state)

	if !state.CreationPresent ||
		state.Completeness != "unknown" ||
		state.Source != pollDurableArg ||
		state.Observations != 1 ||
		len(fake.Receipts()) != 0 {
		t.Fatalf("state %+v", state)
	}
}

func pollWriteArgs() map[string]map[string]any {
	return map[string]map[string]any{
		pollCreateTool: {pollRecipientArg: aliceACI, "question": pollQuestion, "options": []string{pollYes, "No"}},
		pollVoteTool:   {pollRecipientArg: aliceACI, pollTargetArg: aliceACI + ":1000", pollOptionsArg: []uint32{0}},
		pollCloseTool:  {pollRecipientArg: aliceACI, timestampArg: uint64(1000)},
	}
}

func TestMCPPollWritePolicies(t *testing.T) {
	t.Parallel()

	for tool, args := range pollWriteArgs() {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()

			for _, readOnly := range []bool{false, true} {
				fake := writeFake()
				session := connectWith(t,
					fake,
					mcp.Options{
						ReadOnly: readOnly,
						Confirm:  true,
					},
					testClient{options: &sdk.ClientOptions{ElicitationHandler: func(context.Context,
						*sdk.ElicitRequest) (*sdk.ElicitResult,
						error,
					) {
						t.Error("disallowed write asked confirmation")
						return nil, errUnexpectedElicit
					}}}, allow(t))

				result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: tool, Arguments: args})
				if err == nil && !result.IsError {
					t.Fatalf("permitted %s readOnly=%t", tool, readOnly)
				}

				if len(fake.Sent()) != 0 {
					t.Fatal("rejection sent content")
				}
			}
		})
	}
}

func TestMCPPollConfirmationBeforeReservation(t *testing.T) {
	t.Parallel()

	for tool, args := range pollWriteArgs() {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()

			fake := writeFake()
			action := stickerDecline
			prompts := 0
			session := connectWith(t,
				fake,
				mcp.Options{Confirm: true},
				testClient{options: &sdk.ClientOptions{ElicitationHandler: func(_ context.Context,
					req *sdk.ElicitRequest) (*sdk.ElicitResult,
					error,
				) {
					prompts++

					if !strings.Contains(req.Params.Message, "poll") {
						t.Errorf("prompt %q", req.Params.Message)
					}

					return &sdk.ElicitResult{Action: action}, nil
				}}}, allow(t, aliceACI))

			rejected := callRaw(t, session, tool, args)
			if !rejected.IsError || len(fake.Sent()) != 0 {
				t.Fatalf("decline %+v", rejected)
			}

			action = accept

			var out struct{ VoteCount uint32 }
			call(t, session, tool, args, &out)

			if prompts != 2 || len(fake.Sent()) != 1 {
				t.Fatalf("prompts %d writes %d", prompts, len(fake.Sent()))
			}

			if tool == pollVoteTool && out.VoteCount != 1 {
				t.Fatalf("declined vote consumed counter: %d", out.VoteCount)
			}
		})
	}
}

func TestMCPPollInvalidDoesNotConfirmOrReserve(t *testing.T) {
	t.Parallel()

	fake := writeFake()

	session := connectWith(t,
		fake,
		mcp.Options{Confirm: true},
		testClient{options: &sdk.ClientOptions{ElicitationHandler: func(context.Context,
			*sdk.ElicitRequest) (*sdk.ElicitResult,
			error,
		) {
			t.Error("invalid call asked confirmation")
			return nil, errUnexpectedElicit
		}}}, allow(t, aliceACI))
	for _, args := range []map[string]any{
		{pollRecipientArg: aliceACI, pollTargetArg: aliceACI + ":1000", pollOptionsArg: []uint32{0}, "voteCount": 0},
		{pollRecipientArg: aliceACI, pollTargetArg: aliceACI + ":1000", pollOptionsArg: []uint32{0}, "clear": true},
		{pollRecipientArg: aliceACI, "groupId": familyID, pollTargetArg: aliceACI + ":1000", pollOptionsArg: []uint32{0}},
	} {
		if res := callRaw(t, session, pollVoteTool, args); !res.IsError {
			t.Fatalf("invalid vote allowed %+v", args)
		}
	}

	if len(fake.Sent()) != 0 {
		t.Fatal("invalid vote sent")
	}
}

func TestMCPPollPartialGroupDelivery(t *testing.T) {
	t.Parallel()

	for tool, args := range pollWriteArgs() {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			delete(args, pollRecipientArg)
			args["groupId"] = familyID
			fake := writeFake()
			fake.Groups[familyID] = append(fake.Groups[familyID], signal.Recipient{ACI: bobACI})
			fake.SendFailures = map[string]error{bobACI: signal.ErrUntrustedIdentity}
			session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, app.GroupPrefix+familyID))
			res := callRaw(t, session, tool, args)

			out := deliveryOutput(t, res)
			if !res.IsError || out.Timestamp == 0 {
				t.Fatalf("partial %+v", res)
			}

			checkPartialGroup(t, out)

			if len(fake.Sent()) != 1 {
				t.Fatal("partial operation retried")
			}

			if tool == pollVoteTool {
				args["voteCount"] = nil

				var vote struct{ VoteCount uint32 }

				raw, err := json.Marshal(callRaw(t, session, tool, args).StructuredContent)
				if err != nil {
					t.Fatal(err)
				}

				err = json.Unmarshal(raw, &vote)
				if err != nil {
					t.Fatal(err)
				}

				if vote.VoteCount != 2 {
					t.Fatalf("failed vote counter %d", vote.VoteCount)
				}
			}
		})
	}
}

//nolint:cyclop // verifies lifecycle outcomes and absence of unintended writes.
func TestMCPPollShowReadOnlyAndPruning(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{ReadOnly: true, InboxMaxCount: 1}, testClient{})
	msg := photoMessage(1000)
	msg.Attachments = nil
	msg.Poll = &signal.Poll{Question: pollQuestion, Options: []string{pollYes, "No"}}
	waitForPush(t, session, fake, msg)

	pushed := fake.Push(photoMessage(1001))
	if !pushed {
		t.Fatal("push failed")
	}

	var after messages
	call(t, session, messagesWait, map[string]any{cursor: "1", pollTimeoutArg: 30}, &after)

	args := map[string]any{pollRecipientArg: aliceACI, pollTargetArg: aliceACI + ":1000"}

	var bounded output.PollStateJSON
	call(t, session, pollShowTool, args, &bounded)

	if bounded.CreationPresent || bounded.Scanned != 1 || len(fake.Receipts()) != 0 {
		t.Fatalf("bounded %+v", bounded)
	}

	args[pollDurableArg] = true

	var durable output.PollStateJSON
	call(t, session, pollShowTool, args, &durable)

	if !durable.CreationPresent ||
		durable.Source != pollDurableArg ||
		durable.Observations != 1 ||
		durable.Completeness != "unknown" {
		t.Fatalf("durable %+v", durable)
	}

	for _, invalid := range []map[string]any{
		{pollRecipientArg: aliceACI, pollTargetArg: aliceACI + ":1000", pollDurableArg: true, "scanLimit": 1000},
		{pollRecipientArg: aliceACI, pollTargetArg: aliceACI + ":1000", "scanLimit": 0},
		{pollRecipientArg: aliceNumber, pollTargetArg: aliceACI + ":1000"},
		{pollRecipientArg: aliceACI, pollTargetArg: "self:1000"},
	} {
		if res := callRaw(t, session, pollShowTool, invalid); !res.IsError {
			t.Fatalf("invalid show %+v", invalid)
		}
	}

	if len(fake.Sent()) != 0 || len(fake.Receipts()) != 0 {
		t.Fatal("show sent content or receipts")
	}
}

func TestMCPPollVoteExplicitAndExhausted(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, aliceACI))
	args := map[string]any{
		pollRecipientArg: aliceACI,
		pollTargetArg:    aliceACI + ":1000",
		pollOptionsArg:   []uint32{0},
		"voteCount":      uint32(8),
	}

	var out struct{ VoteCount uint32 }
	call(t, session, pollVoteTool, args, &out)

	if out.VoteCount != 8 {
		t.Fatalf("explicit %+v", out)
	}

	delete(args, "voteCount")
	call(t, session, pollVoteTool, args, &out)

	if out.VoteCount != 9 {
		t.Fatalf("automatic %+v", out)
	}

	args["voteCount"] = uint32(1<<32 - 1)
	call(t, session, pollVoteTool, args, &out)
	delete(args, "voteCount")

	res := callRaw(t, session, pollVoteTool, args)
	if !res.IsError || !strings.Contains(text(res), signal.ErrPollVoteExhausted.Error()) || len(fake.Sent()) != 3 {
		t.Fatalf("exhaustion %+v", res)
	}
}
