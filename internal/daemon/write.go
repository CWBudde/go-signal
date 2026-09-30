package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
)

const maxBodyBytes = 1 << 20

var errMediaType = errors.New("Content-Type must be application/json")

type sendRequest struct {
	Recipients []string `json:"recipients"`
	Text       string   `json:"text"`
}
type sendResponse struct {
	OK    bool            `json:"ok"`
	Send  output.SendJSON `json:"send"`
	Error *apiError       `json:"error,omitempty"`
}
type markReadRequest struct {
	Chat   string `json:"chat"`
	Cursor string `json:"cursor"`
}
type markReadResponse struct {
	OK       bool      `json:"ok"`
	Messages int       `json:"messages"`
	Senders  int       `json:"senders"`
	Error    *apiError `json:"error,omitempty"`
}

func decodeBody(writer http.ResponseWriter, requestHTTP *http.Request, out any) error {
	body, err := io.ReadAll(http.MaxBytesReader(writer, requestHTTP.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read JSON body: %w", err)
	}

	body = bytes.TrimSpace(body)

	mediaType, _, err := mime.ParseMediaType(requestHTTP.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errMediaType
	}

	if len(body) == 0 || body[0] != '{' {
		return invalid("body must be one JSON object")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	err = decoder.Decode(out)
	if err != nil {
		return invalid("JSON: %v", err)
	}

	var extra any

	err = decoder.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		return invalid("body must be one JSON object")
	}

	return nil
}

func (s *server) writable(writer http.ResponseWriter, requestHTTP *http.Request) bool {
	if s.opts.ReadOnly {
		s.writeError(writer, requestHTTP, http.StatusForbidden, "forbidden", "daemon is read-only")
		return false
	}

	return true
}

func (s *server) send(writer http.ResponseWriter, requestHTTP *http.Request) {
	if !s.writable(writer, requestHTTP) {
		return
	}

	var request sendRequest

	err := decodeBody(writer, requestHTTP, &request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	result, err := s.app.Send(requestHTTP.Context(), app.SendRequest{Recipients: request.Recipients, Body: request.Text})
	if err != nil && len(result.Results) == 0 {
		s.fail(writer, requestHTTP, err)
		return
	}

	response := sendResponse{OK: err == nil, Send: output.NewSendJSON(result, app.Names{})}
	if err != nil {
		response.Error = &apiError{Code: "send_failed", Message: err.Error()}
	}

	s.writeJSON(writer, requestHTTP, http.StatusOK, response)
}

func (s *server) markRead(writer http.ResponseWriter, requestHTTP *http.Request) {
	if !s.writable(writer, requestHTTP) {
		return
	}

	var request markReadRequest

	err := decodeBody(writer, requestHTTP, &request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	if request.Cursor != "" {
		err := validCursor(request.Cursor)
		if err != nil {
			s.fail(writer, requestHTTP, err)
			return
		}
	}

	chat, err := s.resolveChat(requestHTTP, request.Chat)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	result, err := s.inbox.MarkRead(requestHTTP.Context(), app.MarkReadRequest{Chat: chat, Cursor: request.Cursor})

	response := markReadResponse{OK: err == nil, Messages: result.Messages, Senders: result.Senders}
	if err != nil {
		response.Error = &apiError{Code: "mark_read_failed", Message: err.Error()}
	}

	s.writeJSON(writer, requestHTTP, http.StatusOK, response)
}
