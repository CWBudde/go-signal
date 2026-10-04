package signal

// Story is a received text or media story, including sent transcripts from other devices.
// Printing or storing a story does not send a read or viewed receipt.
type Story struct {
	Envelope

	AllowsReplies bool
	File          *Attachment
	Text          *StoryText
	Mentions      []Mention
}

func (*Story) isEvent() {}

// StoryText preserves the text card's presentation without rendering its colors in the terminal.
// Color pointers retain absent values and explicit zero. Colors are packed ARGB integers.
type StoryText struct {
	Text                string
	Style               string
	ForegroundColor     *uint32
	TextBackgroundColor *uint32
	BackgroundColor     *uint32
	Gradient            *StoryGradient
	Preview             *StoryPreview
}

// StoryGradient describes either modern multi-stop or legacy two-color backgrounds.
type StoryGradient struct {
	StartColor, EndColor, Angle *uint32
	Colors                      []uint32
	Positions                   []float32
}

// StoryPreview is a link card; Image uses the regular verified attachment download path.
type StoryPreview struct {
	URL, Title, Description string
	Date                    uint64
	Image                   *Attachment
}
