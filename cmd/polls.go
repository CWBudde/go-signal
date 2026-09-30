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
		Use: "polls", Short: "Create, vote on and inspect group polls", Args: cobra.NoArgs,
	}
	cmd.AddCommand(newPollCreateCmd(clients, printers, appOpts), newPollVoteCmd(clients, printers, appOpts),
		newPollCloseCmd(clients, printers, appOpts), newPollShowCmd(clients, printers, appOpts))

	return cmd
}

func newPollCreateCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups []string
		req    app.PollCreateRequest
	)

	cmd := &cobra.Command{
		Use:   "create --group <id> --question <text> --option <text>...",
		Short: "Create a poll in one group",
		Long: `Create a poll with 2–10 ordered options. Multiple selections are allowed by default;
--single-choice allows only one. The question and each option must be nonblank and at most
100 UTF-16 code units. Text is preserved as supplied. --group is the canonical base64 group ID.
The result has the poll creator and timestamp for future votes and received results.
Incoming events stay on the server for the next receive; partial delivery is reported without retries.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := onePollGroup(groups)
			if err != nil {
				return err
			}

			req.GroupID = groupID

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
	flags.StringArrayVar(&groups, pollGroupFlag, nil, "one canonical base64 group ID")
	flags.StringVar(&req.Question, "question", "", "poll question")
	flags.StringArrayVar(&req.Options, "option", nil, "answer text in order (repeatable)")
	flags.BoolVar(&req.SingleChoice, "single-choice", false, "allow only one selected option")
	cobra.CheckErr(cmd.MarkFlagRequired(pollGroupFlag))
	cobra.CheckErr(cmd.MarkFlagRequired("question"))
	cobra.CheckErr(cmd.MarkFlagRequired("option"))

	return cmd
}

func newPollVoteCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups  []string
		options []string
		req     app.PollVoteRequest
	)

	cmd := &cobra.Command{
		Use:   "vote --group <id> --target <author>:<timestamp> --vote-count <number>",
		Short: "Change or withdraw your selections on a group poll",
		Long: `Vote on a poll identified by its creator and creation timestamp in milliseconds.
The creator can be an ACI, number, @username or self. --option is a zero-based index (0–9)
and can be repeated; duplicate indexes are rejected. Use --clear instead of options to withdraw.
--vote-count is a positive uint32 counter: start at 1 and increase it for every change,
including withdrawals. Coordinate counters with your other devices; retained inbox history
cannot safely determine the next counter. --group is exactly one canonical base64 group ID.
The phone validates selections against the original poll; this command does not require it locally.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := onePollGroup(groups)
			if err != nil {
				return err
			}

			req.GroupID = groupID

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
	flags.StringArrayVar(&groups, pollGroupFlag, nil, "one canonical base64 group ID")
	flags.StringVar(&req.Target, "target", "", "poll creator and sent timestamp: <author>:<timestamp>")
	flags.Uint32Var(&req.VoteCount, "vote-count", 0, "positive counter, increased for each vote change")
	flags.StringArrayVar(&options, "option", nil, "zero-based selected index (repeatable)")
	flags.BoolVar(&req.Clear, "clear", false, "withdraw your selections")
	cobra.CheckErr(cmd.MarkFlagRequired(pollGroupFlag))
	cobra.CheckErr(cmd.MarkFlagRequired("target"))
	cobra.CheckErr(cmd.MarkFlagRequired("vote-count"))
	cmd.MarkFlagsMutuallyExclusive("option", "clear")

	return cmd
}

func newPollCloseCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups []string
		req    app.PollCloseRequest
	)

	cmd := &cobra.Command{
		Use: "close --group <id> --target <timestamp>", Short: "Close your own group poll", Args: cobra.NoArgs,
		Long: `Close a poll created by your account, using its creation timestamp in milliseconds.
Only the creator can close a poll. --group is exactly one canonical base64 group ID.
Partial delivery is reported without retries.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := onePollGroup(groups)
			if err != nil {
				return err
			}

			req.GroupID = groupID

			err = req.Check()
			if err != nil {
				return fmt.Errorf("poll close: %w", err)
			}

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.PollSendResult, error) { return a.PollClose(cmd.Context(), req) },
				(*output.Printer).PollSend)
		},
	}
	cmd.Flags().StringArrayVar(&groups, pollGroupFlag, nil, "one canonical base64 group ID")
	cmd.Flags().Uint64Var(&req.Target, "target", 0, "creation timestamp (ms) of your poll")
	cobra.CheckErr(cmd.MarkFlagRequired(pollGroupFlag))
	cobra.CheckErr(cmd.MarkFlagRequired("target"))

	return cmd
}

func newPollShowCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups []string
		req    app.PollShowRequest
	)

	cmd := &cobra.Command{
		Use:   "show --group <id> --target <ACI>:<timestamp>",
		Short: "Show retained observations of a group poll", Args: cobra.NoArgs,
		Long: `Show poll results from bounded local inbox history collected by daemon or MCP receiving.
It does not connect or send. Supply the canonical group ID and creator ACI with timestamp.
The account lock is required: stop the daemon/MCP process before running this command.
Ordinary receive and outgoing sends do not populate this inbox. Pruning, missed events and
the scan limit can hide creation, votes or closure. Completeness is always unknown;
an unobserved closure does not mean that the poll is open. --scan-limit bounds all chat entries
examined, not just poll events (default 1000, maximum 10000).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, err := onePollGroup(groups)
			if err != nil {
				return err
			}

			req.GroupID = groupID

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
	cmd.Flags().StringArrayVar(&groups, pollGroupFlag, nil, "one canonical base64 group ID")
	cmd.Flags().StringVar(&req.Target, "target", "", "canonical creator ACI and sent timestamp: <ACI>:<timestamp>")
	cmd.Flags().IntVar(&req.ScanLimit, "scan-limit", app.DefaultPollScanLimit, "maximum retained chat entries examined")
	cobra.CheckErr(cmd.MarkFlagRequired(pollGroupFlag))
	cobra.CheckErr(cmd.MarkFlagRequired("target"))

	return cmd
}

func onePollGroup(groups []string) (string, error) {
	if len(groups) != 1 {
		return "", fmt.Errorf("%w: supply --group exactly once", signal.ErrInvalidPoll)
	}

	return groups[0], nil
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
