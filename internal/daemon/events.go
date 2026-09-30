package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
)

const (
	heartbeatInterval  = 15 * time.Second
	streamWriteTimeout = 10 * time.Second
)

type readyResponse struct {
	Cursor string `json:"cursor"`
}

func (s *server) events(writer http.ResponseWriter, requestHTTP *http.Request) {
	request, err := s.query(requestHTTP, true)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	request, page, err := s.streamStart(requestHTTP, request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")

	controller := http.NewResponseController(writer)

	err = writeEvent(controller, writer, "ready", request.Cursor, readyResponse{Cursor: request.Cursor})
	if err != nil {
		s.streamError(requestHTTP, err)
		return
	}

	err = s.stream(requestHTTP, controller, writer, request, page)
	s.streamError(requestHTTP, err)
}

func (s *server) streamStart(
	requestHTTP *http.Request, request app.MessagesRequest,
) (app.MessagesRequest, app.MessagesPage, error) {
	if request.Cursor == "" {
		// Snapshot all chats, even when the subscription filters one chat.
		tail, err := s.inbox.List(requestHTTP.Context(), app.MessagesRequest{Limit: 1})
		if err != nil {
			return request, app.MessagesPage{}, fmt.Errorf("stream tail: %w", err)
		}

		request.Cursor = tail.Cursor
	}

	page, err := s.inbox.List(requestHTTP.Context(), request)
	if err != nil {
		return request, page, fmt.Errorf("stream backlog: %w", err)
	}

	return request, page, nil
}

func (s *server) stream(
	requestHTTP *http.Request, controller *http.ResponseController, writer http.ResponseWriter,
	request app.MessagesRequest, page app.MessagesPage,
) error {
	for {
		for _, entry := range page.Entries {
			value := output.NewInboxEntryJSON(entry, app.Names{})

			err := writeEvent(controller, writer, "inbox", app.FormatCursor(entry.ID), value)
			if err != nil {
				return err
			}
		}

		request.Cursor = page.Cursor

		var err error

		page, err = s.inbox.Wait(requestHTTP.Context(), request, heartbeatInterval)
		if err != nil {
			return fmt.Errorf("stream wait: %w", err)
		}

		if len(page.Entries) == 0 {
			err = writeEvent(controller, writer, "", "", nil)
			if err != nil {
				return err
			}
		}
	}
}

func writeEvent(
	controller *http.ResponseController, writer http.ResponseWriter, name, eventID string, value any,
) error {
	var data []byte

	if value != nil {
		var err error

		data, err = json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
	}

	err := controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
	if err != nil {
		return fmt.Errorf("event write deadline: %w", err)
	}

	if name == "" {
		_, err = fmt.Fprint(writer, ": heartbeat\n\n")
	} else {
		_, err = fmt.Fprintf(writer, "event: %s\nid: %s\ndata: %s\n\n", name, eventID, data)
	}

	if err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	err = controller.Flush()
	if err != nil {
		return fmt.Errorf("flush event: %w", err)
	}

	err = controller.SetWriteDeadline(time.Time{})
	if err != nil {
		return fmt.Errorf("clear event write deadline: %w", err)
	}

	return nil
}

func (s *server) streamError(requestHTTP *http.Request, err error) {
	if requestHTTP.Context().Err() == nil {
		s.logger.WarnContext(requestHTTP.Context(), "daemon: event stream ended", "error", err)
	}
}
