package analyzer

import (
	"fmt"
	"os"

	"github.com/schollz/progressbar/v3"
)

// Progress is an interface for tracking progress of operations
type Progress interface {
	// Add increments the progress by 1
	Add(int) error
	// Close cleans up any resources used by the progress tracker
	Close()
}

// NoopProgress is a progress tracker that does nothing
type NoopProgress struct{}

func (p *NoopProgress) Add(int) error { return nil }
func (p *NoopProgress) Close()        {}

// NewNoopProgress creates a new no-op progress tracker
func NewNoopProgress() *NoopProgress {
	return &NoopProgress{}
}

// BarProgress wraps a progressbar.ProgressBar to implement the Progress interface
type BarProgress struct {
	bar *progressbar.ProgressBar
}

func (p *BarProgress) Add(n int) error {
	return p.bar.Add(n)
}

func (p *BarProgress) Close() {
	fmt.Fprint(os.Stderr, "\r\033[K")
}

// NewBarProgress creates a new progress bar tracker
func NewBarProgress(total int) *BarProgress {
	return &BarProgress{
		bar: progressbar.NewOptions(total,
			progressbar.OptionSetDescription("Generating embeddings"),
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionShowCount(),
			progressbar.OptionShowIts(),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "=",
				SaucerHead:    ">",
				SaucerPadding: " ",
				BarStart:      "[",
				BarEnd:        "]",
			})),
	}
}
