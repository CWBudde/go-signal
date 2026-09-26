package output_test

import (
	"io"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
)

func TestPrinterFormat(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.Plain, output.JSON} {
		if got := output.New(io.Discard, format, time.UTC).Format(); got != format {
			t.Errorf("Format() = %v, want %v", got, format)
		}
	}
}
