package signal

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrInvalidStyle means that a text style or its UTF-16 range is invalid.
var ErrInvalidStyle = errors.New("invalid text style")

// Style is one of Signal's supported text formats.
type Style string

const (
	StyleBold          Style = "bold"
	StyleItalic        Style = "italic"
	StyleSpoiler       Style = "spoiler"
	StyleStrikethrough Style = "strikethrough"
	StyleMonospace     Style = "monospace"
)

// TextStyle formats a range of a message body. Start and Length count UTF-16 code units;
// both ends must lie on rune boundaries. Ranges may overlap.
type TextStyle struct {
	Start  uint32
	Length uint32
	Style  Style
}

// CheckTextStyles validates supported styles and complete rune boundaries in body.
// It permits overlapping ranges and uses wide arithmetic to reject overflowing ends.
func CheckTextStyles(body string, styles []TextStyle) error {
	if len(styles) == 0 {
		return nil
	}

	if !utf8.ValidString(body) {
		return fmt.Errorf("%w: body must be valid UTF-8", ErrInvalidStyle)
	}

	boundaries := map[uint64]bool{0: true}

	var units uint64

	for _, char := range body {
		units += uint64(utf16.RuneLen(char)) //nolint:gosec // valid UTF-8 has positive rune lengths
		boundaries[units] = true
	}

	for _, style := range styles {
		if !style.Style.valid() {
			return fmt.Errorf("%w: unsupported style %q", ErrInvalidStyle, style.Style)
		}

		end := uint64(style.Start) + uint64(style.Length)
		if style.Length == 0 || !boundaries[uint64(style.Start)] || !boundaries[end] {
			return fmt.Errorf("%w: %d:%d must cover complete characters within the body", ErrInvalidStyle,
				style.Start, style.Length)
		}
	}

	return nil
}

func (style Style) valid() bool {
	switch style {
	case StyleBold, StyleItalic, StyleSpoiler, StyleStrikethrough, StyleMonospace:
		return true
	default:
		return false
	}
}
