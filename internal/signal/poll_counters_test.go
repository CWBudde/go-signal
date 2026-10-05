package signal_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPollVoteCounterCanonicalAuthor(t *testing.T) {
	t.Parallel()

	req := signal.PollVoteCounterRequest{
		Chat:   signal.Chat{Recipient: signal.Recipient{ACI: selfACI}},
		Author: signal.Recipient{ACI: strings.ToUpper("abcdefab-cdef-4abc-8def-abcdefabcdef")}, Timestamp: 42,
	}
	if !errors.Is(req.Check(), signal.ErrInvalidPoll) {
		t.Fatal("noncanonical author accepted as a separate counter")
	}
}
