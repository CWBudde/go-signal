package daemon

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
)

type connectionJSON struct {
	State     string    `json:"state"`
	Since     time.Time `json:"since"`
	LastEvent time.Time `json:"lastEvent"`
	Error     string    `json:"error,omitempty"`
}
type healthResponse struct {
	Version    string         `json:"version"`
	Account    string         `json:"account"`
	Connection connectionJSON `json:"connection"`
}
type messagesResponse struct {
	Messages []output.InboxEntryJSON `json:"messages"`
	Cursor   string                  `json:"cursor"`
	More     bool                    `json:"more"`
}

func (s *server) health(writer http.ResponseWriter, requestHTTP *http.Request) {
	account, err := s.app.AccountShow(requestHTTP.Context())
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	connection := s.inbox.Connection()

	status := connectionJSON{State: connection.State.String(), Since: connection.Since, LastEvent: connection.LastEvent}
	if connection.Err != nil {
		status.Error = connection.Err.Error()
	}

	s.writeJSON(writer, requestHTTP, http.StatusOK, healthResponse{
		Version: s.opts.Version, Account: account.ACI, Connection: status,
	})
}

func validCursor(cursor string) error {
	if cursor == "" {
		return invalid("cursor must be a nonnegative int64 string")
	}

	for _, char := range cursor {
		if char < '0' || char > '9' {
			return invalid("cursor must be a nonnegative int64 string")
		}
	}

	_, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil {
		return invalid("cursor must be a nonnegative int64 string")
	}

	return nil
}

func (s *server) resolveChat(requestHTTP *http.Request, arg string) (string, error) {
	if arg == "" {
		return "", nil
	}

	chat, err := s.app.ResolveChat(requestHTTP.Context(), arg)
	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}

	return chat.Key(), nil
}

func (s *server) query(requestHTTP *http.Request, stream bool) (app.MessagesRequest, error) {
	values, err := url.ParseQuery(requestHTTP.URL.RawQuery)
	if err != nil {
		return app.MessagesRequest{}, invalid("query: %v", err)
	}

	request := app.MessagesRequest{Limit: app.DefaultMessagesLimit}

	request.Chat, err = s.resolveChat(requestHTTP, values.Get("chat"))
	if err != nil {
		return request, err
	}

	cursor, err := queryCursor(values, requestHTTP.Header.Get("Last-Event-ID"), stream)
	if err != nil {
		return request, err
	}

	request.Cursor = cursor
	if stream {
		return request, nil
	}

	request.Limit, err = queryLimit(values)
	if err != nil {
		return request, err
	}

	request.Since, err = querySince(values)

	return request, err
}

func queryCursor(values url.Values, lastEventID string, stream bool) (string, error) {
	cursor, supplied := values["cursor"]
	if stream && lastEventID != "" {
		cursor, supplied = []string{lastEventID}, true
	}

	if !supplied {
		return "", nil
	}

	if len(cursor) != 1 {
		return "", invalid("supply one cursor")
	}

	return cursor[0], validCursor(cursor[0])
}

func queryLimit(values url.Values) (int, error) {
	limits, supplied := values["limit"]
	if !supplied {
		return app.DefaultMessagesLimit, nil
	}

	if len(limits) != 1 {
		return 0, invalid("supply one limit")
	}

	limit, err := strconv.Atoi(limits[0])
	if err != nil || limit < 1 || limit > app.MaxMessagesLimit {
		return 0, invalid("limit must be between 1 and %d", app.MaxMessagesLimit)
	}

	return limit, nil
}

func querySince(values url.Values) (time.Time, error) {
	since, supplied := values["since"]
	if !supplied {
		return time.Time{}, nil
	}

	if len(since) != 1 {
		return time.Time{}, invalid("supply one since")
	}

	timestamp, err := time.Parse(time.RFC3339, since[0])
	if err != nil {
		return time.Time{}, invalid("since must be an RFC3339 time")
	}

	return timestamp, nil
}

func (s *server) messages(writer http.ResponseWriter, requestHTTP *http.Request) {
	request, err := s.query(requestHTTP, false)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	page, err := s.inbox.List(requestHTTP.Context(), request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	response := messagesResponse{
		Messages: make([]output.InboxEntryJSON, 0, len(page.Entries)),
		Cursor:   page.Cursor, More: page.More,
	}
	for _, entry := range page.Entries {
		response.Messages = append(response.Messages, output.NewInboxEntryJSON(entry, app.Names{}))
	}

	s.writeJSON(writer, requestHTTP, http.StatusOK, response)
}
