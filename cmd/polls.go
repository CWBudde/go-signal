package cmd

import (
	"fmt"
	"strconv"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

const pollGroupFlag = "group"

func newPollsCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use: "polls", Short: "Create, vote on and inspect polls", Args: cobra.NoArgs,
	}
	cmd.AddCommand(newPollCreateCmd(clients, printers, appOpts), newPollVoteCmd(clients, printers, appOpts),
		newPollCloseCmd(clients, printers, appOpts), newPollShowCmd(clients, printers, appOpts))

	return cmd
}

func newPollCreateCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups, recipients []string
		req                app.PollCreateRequest
	)

	cmd := &cobra.Command{
		Use:   "create (--group <id> | --recipient <user>) --question <text> --option <text>...",
		Short: "Create a poll in one chat",
		Long: `Create a poll with 2–10 ordered options. Multiple selections are allowed by default;
--single-choice allows only one. The question and each option must be nonblank and at most
100 UTF-16 code units. Text is preserved as supplied. Supply exactly one --group (canonical
base64 ID) or --recipient (ACI, +number, @username or self).
The result has the poll creator and timestamp for future votes and received results.
Incoming events stay on the server for the next receive; partial delivery is reported without retries.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, recipient, err := onePollDestination(groups, recipients)
			if err != nil {
				return err
			}

			req.GroupID, req.Recipient = groupID, recipient

			err = req.Check()
			if err != nil {
				return fmt.Errorf("poll create: %w", err)
			}

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.PollSendResult, error) { return a.PollCreate(cmd.Context(), req) },
				(*output.Printer).PollSend)
		},
	}
	flags := cmd.Flags()
	pollDestinationFlags(cmd, &groups, &recipients)
	flags.StringVar(&req.Question, "question", "", "poll question")
	flags.StringArrayVar(&req.Options, "option", nil, "answer text in order (repeatable)")
	flags.BoolVar(&req.SingleChoice, "single-choice", false, "allow only one selected option")
	cobra.CheckErr(cmd.MarkFlagRequired("question"))
	cobra.CheckErr(cmd.MarkFlagRequired("option"))

	return cmd
}

func newPollVoteCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups, recipients []string
		options            []string
		req                app.PollVoteRequest
	)

	cmd := &cobra.Command{
		Use:   "vote (--group <id> | --recipient <user>) --target <author>:<timestamp> [--vote-count <number>]",
		Short: "Change or withdraw your selections on a poll",
		Long: `Vote on a poll identified by its creator and creation timestamp in milliseconds.
The creator can be an ACI, number, @username or self. --option is a zero-based index (0–9)
and can be repeated; duplicate indexes are rejected. Use --clear instead of options to withdraw.
Omit --vote-count to reserve the next durable account-local counter, including withdrawals.
Own-device votes observed while receiving advance it; failed sends still consume a reservation.
Use a positive --vote-count to override when coordinating with unseen other-device activity.
Local counters cannot establish the latest vote on another device. Supply one --group or --recipient.
The phone validates selections against the original poll; this command does not require it locally.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, recipient, err := onePollDestination(groups, recipients)
			if err != nil {
				return err
			}

			req.GroupID, req.Recipient = groupID, recipient
			if cmd.Flags().Changed("vote-count") && req.VoteCount == 0 {
				return fmt.Errorf("%w: explicit vote count must be positive", signal.ErrInvalidPoll)
			}

			req.OptionIndexes, err = pollOptionIndexes(options)
			if err != nil {
				return err
			}

			err = req.Check()
			if err != nil {
				return fmt.Errorf("poll vote: %w", err)
			}

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.PollSendResult, error) { return a.PollVote(cmd.Context(), req) },
				(*output.Printer).PollSend)
		},
	}
	flags := cmd.Flags()
	pollDestinationFlags(cmd, &groups, &recipients)
	flags.StringVar(&req.Target, "target", "", "poll creator and sent timestamp: <author>:<timestamp>")
	flags.Uint32Var(&req.VoteCount, "vote-count", 0,
		"explicit positive counter override (default: automatic local allocation)")
	flags.StringArrayVar(&options, "option", nil, "zero-based selected index (repeatable)")
	flags.BoolVar(&req.Clear, "clear", false, "withdraw your selections")
	cobra.CheckErr(cmd.MarkFlagRequired("target"))
	cmd.MarkFlagsMutuallyExclusive("option", "clear")

	return cmd
}

func newPollCloseCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups, recipients []string
		req                app.PollCloseRequest
	)

	cmd := &cobra.Command{
		Use:   "close (--group <id> | --recipient <user>) --target <timestamp>",
		Short: "Close your own poll", Args: cobra.NoArgs,
		Long: `Close a poll created by your account, using its creation timestamp in milliseconds.
Only the creator can close a poll. Supply one --group or --recipient.
Partial delivery is reported without retries.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, recipient, err := onePollDestination(groups, recipients)
			if err != nil {
				return err
			}

			req.GroupID, req.Recipient = groupID, recipient

			err = req.Check()
			if err != nil {
				return fmt.Errorf("poll close: %w", err)
			}

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.PollSendResult, error) { return a.PollClose(cmd.Context(), req) },
				(*output.Printer).PollSend)
		},
	}
	pollDestinationFlags(cmd, &groups, &recipients)
	cmd.Flags().Uint64Var(&req.Target, "target", 0, "creation timestamp (ms) of your poll")
	cobra.CheckErr(cmd.MarkFlagRequired("target"))

	return cmd
}

//nolint:funlen // Offline command preflight, flags and rendering stay together.
func newPollShowCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups, recipients []string
		req                app.PollShowRequest
	)

	cmd := &cobra.Command{
		Use:   "show (--group <id> | --recipient <ACI>) --target <ACI>:<timestamp>",
		Short: "Show retained observations of a poll", Args: cobra.NoArgs,
		Long: `Show poll results from bounded local inbox history, or use --durable for stored poll projections.
Durable projections collect ordinary receive, daemon and MCP observations before acknowledgement,
seed existing retained inbox history once, and survive inbox pruning. Outgoing sends are not observations.
The durable view cannot be combined with --scan-limit; missed events keep completeness unknown.
It does not connect or send. Supply a canonical group ID or direct chat ACI and the creator ACI with timestamp.
The account lock is required: stop the daemon/MCP process before running this command.
Ordinary receive and outgoing sends do not populate this inbox. Pruning, missed events and
the scan limit can hide creation, votes or closure. Completeness is always unknown;
an unobserved closure does not mean that the poll is open. --scan-limit bounds all chat entries
examined, not just poll events (default 1000, maximum 10000).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, recipient, err := onePollDestination(groups, recipients)
			if err != nil {
				return err
			}

			req.GroupID, req.Recipient = groupID, recipient
			if req.Durable {
				if cmd.Flags().Changed("scan-limit") {
					return fmt.Errorf("%w: --durable cannot use --scan-limit", signal.ErrInvalidPoll)
				}

				req.ScanLimit = 0
			}

			err = req.Check()
			if err != nil {
				return fmt.Errorf("poll show: %w", err)
			}

			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			use := app.New(client, appOpts...)

			state, err := use.PollShow(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.PollState(state)
		},
	}
	pollDestinationFlags(cmd, &groups, &recipients)
	cmd.Flags().StringVar(&req.Target, "target", "", "canonical creator ACI and sent timestamp: <ACI>:<timestamp>")
	cmd.Flags().BoolVar(&req.Durable, "durable", false, "read durable poll observations independent of inbox retention")
	cmd.Flags().IntVar(&req.ScanLimit, "scan-limit", app.DefaultPollScanLimit, "maximum retained chat entries examined")
	cobra.CheckErr(cmd.MarkFlagRequired("target"))

	return cmd
}

func pollDestinationFlags(cmd *cobra.Command, groups, recipients *[]string) {
	cmd.Flags().StringArrayVar(groups, pollGroupFlag, nil, "one canonical base64 group ID")
	cmd.Flags().StringArrayVar(recipients, "recipient", nil,
		"one direct recipient: ACI, +number, @username or self (show: canonical ACI)")
	cmd.MarkFlagsOneRequired(pollGroupFlag, "recipient")
	cmd.MarkFlagsMutuallyExclusive(pollGroupFlag, "recipient")
}

func onePollDestination(groups, recipients []string) (string, string, error) {
	if len(groups)+len(recipients) != 1 {
		return "", "", fmt.Errorf("%w: supply --group or --recipient exactly once", signal.ErrInvalidPoll)
	}

	if len(groups) == 1 {
		return groups[0], "", nil
	}

	return "", recipients[0], nil
}

func pollOptionIndexes(options []string) ([]uint32, error) {
	indexes := make([]uint32, 0, len(options))
	for _, option := range options {
		index, err := strconv.ParseUint(option, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%w: option %q must be a zero-based uint32 index", signal.ErrInvalidPoll, option)
		}

		indexes = append(indexes, uint32(index))
	}

	return indexes, nil
}
