package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// parseStyles validates explicit offsets in the body after mention substitution, before
// connecting, resolving users or reading attachments.
func parseStyles(body string, args []string) ([]signal.TextStyle, error) {
	if len(args) == 0 {
		return nil, nil
	}

	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w: styles need non-blank message text", signal.ErrInvalidStyle)
	}

	var styles []signal.TextStyle

	for _, arg := range args {
		style, err := parseStyle(arg)
		if err != nil {
			return nil, err
		}

		styles = append(styles, style)
	}

	body, _ = parseMentions(body)

	err := signal.CheckTextStyles(body, styles)
	if err != nil {
		return nil, err //nolint:wrapcheck // Send adds action context
	}

	return styles, nil
}

const styleArgumentParts = 3

func parseStyle(arg string) (signal.TextStyle, error) {
	parts := strings.Split(arg, ":")
	if len(parts) != styleArgumentParts {
		return signal.TextStyle{}, fmt.Errorf("%w: %q: want start:length:STYLE", signal.ErrInvalidStyle, arg)
	}

	start, startErr := strconv.ParseUint(parts[0], 10, 32)

	length, lengthErr := strconv.ParseUint(parts[1], 10, 32)
	if startErr != nil || lengthErr != nil {
		return signal.TextStyle{}, fmt.Errorf("%w: %q: offsets must be unsigned 32-bit integers", signal.ErrInvalidStyle, arg)
	}

	return signal.TextStyle{
		Start: uint32(start), Length: uint32(length), Style: signal.Style(strings.ToLower(parts[2])),
	}, nil
}
