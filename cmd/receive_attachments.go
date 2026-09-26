package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

// eventPrinter prints received events. With a download dir (--download-attachments), it saves
// the attachments of a message first, so that the output can show their paths.
type eventPrinter struct {
	out   *output.Printer
	app   *app.App
	dir   string
	names *app.NameBook
}

// prepareDownloadDir checks (and creates) the --download-attachments dir, if one is given,
// before anything is received.
func prepareDownloadDir(dir string) error {
	if dir == "" {
		return nil
	}

	err := app.PrepareDownloadDir(dir)
	if err != nil {
		return fmt.Errorf("--download-attachments: %w", err)
	}

	return nil
}

// newEventPrinter returns an eventPrinter that shows the names of the contacts in the store (see
// app.NameBook), loaded now and reloaded as events name users without a name.
func newEventPrinter(ctx context.Context, out *output.Printer, client signal.Client, dir string) eventPrinter {
	use := app.New(client)

	names, err := use.NameBook(ctx)
	if err != nil {
		slog.Debug("names not loaded", "error", err)
	}

	out.SetNames(names.Names())

	return eventPrinter{out: out, app: use, dir: dir, names: names}
}

func (p eventPrinter) print(ctx context.Context, evt signal.Event) error {
	reloaded, err := p.names.Refresh(ctx, evt)
	if err != nil {
		slog.Debug("names not reloaded", "error", err)
	}

	if reloaded {
		p.out.SetNames(p.names.Names())
	}

	msg, ok := evt.(*signal.Message)
	if !ok || p.dir == "" || len(msg.Attachments) == 0 {
		return p.out.Event(evt) //nolint:wrapcheck // the caller wraps it
	}

	saved := p.app.SaveAttachments(ctx, app.SaveAttachmentsRequest{Dir: p.dir, Message: msg})

	for i, outcome := range saved {
		if outcome.Err != nil {
			slog.Warn("attachment not saved", "sender", msg.Sender.String(), "timestamp", msg.Timestamp,
				"attachment", i+1, "error", outcome.Err)

			continue
		}

		slog.Debug("attachment saved", "path", outcome.Path)
	}

	return p.out.SavedMessage(msg, saved) //nolint:wrapcheck // the caller wraps it
}
