package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorResponse struct {
	Error apiError `json:"error"`
}

var errInvalidRequest = errors.New("invalid request")

func (s *server) handler(token string) http.Handler {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(writer http.ResponseWriter, requestHTTP *http.Request) {
		s.writeError(writer, requestHTTP, http.StatusForbidden, "forbidden", "cross-origin request rejected")
	}))
	routes := protection.Handler(http.HandlerFunc(s.route))

	return http.HandlerFunc(func(writer http.ResponseWriter, requestHTTP *http.Request) {
		got, found := strings.CutPrefix(requestHTTP.Header.Get("Authorization"), "Bearer ")
		if !found || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="go-signal"`)
			s.writeError(writer, requestHTTP, http.StatusUnauthorized, "unauthorized", "valid bearer token required")

			return
		}

		routes.ServeHTTP(writer, requestHTTP)
	})
}

//nolint:cyclop // one explicit case per authenticated API route.
func (s *server) route(writer http.ResponseWriter, requestHTTP *http.Request) {
	var handler http.HandlerFunc

	method := http.MethodGet

	switch requestHTTP.URL.Path {
	case "/v1/health":
		handler = s.health
	case "/v1/messages":
		if requestHTTP.Method == http.MethodPost {
			s.send(writer, requestHTTP)
			return
		}

		handler = s.messages
	case "/v1/polls":
		if requestHTTP.Method == http.MethodPost {
			s.pollCreate(writer, requestHTTP)
			return
		}

		handler = s.pollShow
	case "/v1/polls/vote":
		handler, method = s.pollVote, http.MethodPost
	case "/v1/polls/close":
		handler, method = s.pollClose, http.MethodPost
	case "/v1/events":
		handler = s.events
	case "/v1/mark-read":
		handler, method = s.markRead, http.MethodPost
	default:
		s.writeError(writer, requestHTTP, http.StatusNotFound, "not_found", "route not found")
		return
	}

	if requestHTTP.Method == method {
		handler(writer, requestHTTP)
		return
	}

	allowed := method
	if requestHTTP.URL.Path == "/v1/messages" || requestHTTP.URL.Path == "/v1/polls" {
		allowed = "GET, POST"
	}

	writer.Header().Set("Allow", allowed)
	s.writeError(writer, requestHTTP, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func (s *server) writeJSON(writer http.ResponseWriter, requestHTTP *http.Request, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	err := json.NewEncoder(writer).Encode(value)
	if err != nil {
		s.logger.DebugContext(requestHTTP.Context(), "daemon: write JSON response", "error", err)
	}
}

func (s *server) writeError(writer http.ResponseWriter, requestHTTP *http.Request, status int, code, message string) {
	s.writeJSON(writer, requestHTTP, status, errorResponse{Error: apiError{Code: code, Message: message}})
}

func (s *server) fail(writer http.ResponseWriter, requestHTTP *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"

	var oversized *http.MaxBytesError
	switch {
	case errors.As(err, &oversized):
		status, code = http.StatusRequestEntityTooLarge, "request_too_large"
	case errors.Is(err, errMediaType):
		status, code = http.StatusUnsupportedMediaType, "unsupported_media_type"
	case errors.Is(err, signal.ErrInvalidPoll), errors.Is(err, app.ErrInvalidTarget),
		errors.Is(err, errInvalidRequest), errors.Is(err, app.ErrInvalidCursor),
		errors.Is(err, app.ErrAmbiguousGroup), errors.Is(err, app.ErrInvalidRecipient),
		errors.Is(err, app.ErrNoRecipients), errors.Is(err, app.ErrEmptyMessage),
		errors.Is(err, signal.ErrNotOnSignal), errors.Is(err, signal.ErrUnknownGroup), errors.Is(err, signal.ErrUnresolvable):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, signal.ErrPollVoteExhausted):
		status, code = http.StatusConflict, "poll_vote_exhausted"
	case errors.Is(err, app.ErrRecipientNotAllowed):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, signal.ErrDeviceUnlinked), errors.Is(err, signal.ErrConnectionFailed),
		errors.Is(err, signal.ErrClosed),
		errors.Is(err, signal.ErrNotConnected), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusServiceUnavailable, "unavailable"
	}

	s.writeError(writer, requestHTTP, status, code, err.Error())
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidRequest, fmt.Sprintf(format, args...))
}
