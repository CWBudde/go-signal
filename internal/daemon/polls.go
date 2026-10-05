package daemon

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
)

type pollCreateRequest struct {
	GroupID      string   `json:"groupId"`
	Recipient    string   `json:"recipient"`
	Question     string   `json:"question"`
	Options      []string `json:"options"`
	SingleChoice bool     `json:"singleChoice"`
}
type pollVoteRequest struct {
	GroupID       string   `json:"groupId"`
	Recipient     string   `json:"recipient"`
	Target        string   `json:"target"`
	OptionIndexes []uint32 `json:"optionIndexes"`
	VoteCount     *uint32  `json:"voteCount"`
	Clear         bool     `json:"clear"`
}
type pollCloseRequest struct {
	GroupID   string `json:"groupId"`
	Recipient string `json:"recipient"`
	Timestamp uint64 `json:"timestamp"`
}
type pollSendResponse struct {
	OK    bool                `json:"ok"`
	Poll  output.PollSendJSON `json:"poll"`
	Error *apiError           `json:"error,omitempty"`
}

func (s *server) pollCreate(writer http.ResponseWriter, requestHTTP *http.Request) {
	if !s.writable(writer, requestHTTP) {
		return
	}

	var request pollCreateRequest

	err := decodeBody(writer, requestHTTP, &request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	result, err := s.app.PollCreate(requestHTTP.Context(),
		app.PollCreateRequest{
			GroupID:      request.GroupID,
			Recipient:    request.Recipient,
			Question:     request.Question,
			Options:      request.Options,
			SingleChoice: request.SingleChoice,
		})
	s.pollSendResult(writer, requestHTTP, result, err)
}

func (s *server) pollVote(writer http.ResponseWriter, requestHTTP *http.Request) {
	if !s.writable(writer, requestHTTP) {
		return
	}

	var request pollVoteRequest

	err := decodeBody(writer, requestHTTP, &request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	vote := app.PollVoteRequest{
		GroupID:       request.GroupID,
		Recipient:     request.Recipient,
		Target:        request.Target,
		OptionIndexes: request.OptionIndexes,
		Clear:         request.Clear,
	}
	if request.VoteCount != nil {
		if *request.VoteCount == 0 {
			s.fail(writer, requestHTTP, invalid("voteCount must be positive when supplied"))
			return
		}

		vote.VoteCount = *request.VoteCount
	}

	result, err := s.app.PollVote(requestHTTP.Context(), vote)
	s.pollSendResult(writer, requestHTTP, result, err)
}

func (s *server) pollClose(writer http.ResponseWriter, requestHTTP *http.Request) {
	if !s.writable(writer, requestHTTP) {
		return
	}

	var request pollCloseRequest

	err := decodeBody(writer, requestHTTP, &request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	result, err := s.app.PollClose(requestHTTP.Context(),
		app.PollCloseRequest{
			GroupID:   request.GroupID,
			Recipient: request.Recipient,
			Target:    request.Timestamp,
		})
	s.pollSendResult(writer, requestHTTP, result, err)
}

func (s *server) pollSendResult(
	writer http.ResponseWriter, requestHTTP *http.Request, result app.PollSendResult, err error,
) {
	if err != nil && !errors.Is(err, app.ErrSendFailed) {
		s.fail(writer, requestHTTP, err)
		return
	}

	response := pollSendResponse{OK: err == nil, Poll: output.NewPollSendJSON(result, app.Names{})}
	if err != nil {
		response.Error = &apiError{Code: "send_failed", Message: err.Error()}
	}

	s.writeJSON(writer, requestHTTP, http.StatusOK, response)
}

func (s *server) pollShow(writer http.ResponseWriter, requestHTTP *http.Request) {
	request, err := pollQuery(requestHTTP.URL.RawQuery)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	state, err := s.app.PollShow(requestHTTP.Context(), request)
	if err != nil {
		s.fail(writer, requestHTTP, err)
		return
	}

	s.writeJSON(writer, requestHTTP, http.StatusOK, struct {
		PollState output.PollStateJSON `json:"pollState"`
	}{output.NewPollStateJSON(state, app.Names{})})
}

func pollQuery(raw string) (app.PollShowRequest, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return app.PollShowRequest{}, invalid("query: %v", err)
	}

	err = checkPollQuery(values)
	if err != nil {
		return app.PollShowRequest{}, err
	}

	request := app.PollShowRequest{
		GroupID:   values.Get("groupId"),
		Recipient: values.Get("recipient"),
		Target:    values.Get("target"),
	}
	if _, ok := values["durable"]; ok {
		request.Durable, err = strconv.ParseBool(values.Get("durable"))
		if err != nil {
			return request, invalid("durable must be a boolean")
		}
	}

	if _, ok := values["scanLimit"]; ok {
		if request.Durable {
			return request, invalid("scanLimit cannot accompany durable")
		}

		request.ScanLimit, err = strconv.Atoi(values.Get("scanLimit"))
		if err != nil || request.ScanLimit < 1 {
			return request, invalid("scanLimit must be between 1 and %d", app.MaxPollScanLimit)
		}
	}

	return request, nil
}

func checkPollQuery(values url.Values) error {
	for key, value := range values {
		switch key {
		case "groupId", "recipient", "target", "durable", "scanLimit":
		default:
			return invalid("unknown poll query parameter %q", key)
		}

		if len(value) != 1 {
			return invalid("supply one %s", key)
		}
	}

	return nil
}
