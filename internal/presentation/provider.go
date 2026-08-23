package presentation

import (
	"context"
	"errors"
)

// ErrNotConfigured is returned when no presentation service has been set up.
// It is separate from a failure so the interface can say "nobody has turned
// this on yet" rather than "the deck could not be made".
var ErrNotConfigured = errors.New("presentation provider is not configured")

// ErrUpstream marks a failure that came from the presentation service rather
// than from kanpic. The two are told apart so the person who pressed the button
// is told which side to chase — a wrong address and an unreadable range are
// different problems with different owners.
var ErrUpstream = errors.New("presentation service failed")

// Result is what came back from making a deck. Warnings are the provider's own
// account of what it had to change; they are shown to the person who asked
// rather than swallowed, because a deck that quietly says less than its author
// meant is worse than one that says so.
type Result struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	SlideCount int       `json:"slide_count"`
	Template   string    `json:"template,omitempty"`
	EditURL    string    `json:"edit_url,omitempty"`
	Warnings   []string  `json:"warnings"`
	Source     SourceRef `json:"source"`
}

// Template is one design a deck can be made into.
type Template struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BuiltIn bool   `json:"built_in"`
}

// CreateRequest carries the deck and the choices that are the provider's to
// honour rather than kanpic's to decide.
type CreateRequest struct {
	Deck       Deck
	TemplateID string
}

// Provider is a presentation service kanpic can hand a deck to.
//
// Nothing outside this package's own implementations may know which product is
// behind it: the rest of kanpic sees decks and results. That is what keeps a
// second provider — another tool, another house's system — a new file rather
// than a change to the editor, the API and the analyzer.
type Provider interface {
	// Name identifies the provider in settings and in error messages.
	Name() string
	// Templates lists the designs available to the configured account.
	Templates(ctx context.Context) ([]Template, error)
	// Create makes the deck and returns where it now lives.
	Create(ctx context.Context, request CreateRequest) (Result, error)
	// Export renders the deck to a file. The format is a hint; a provider that
	// supports one format returns that one and says so in the content type.
	Export(ctx context.Context, id, format string) (data []byte, contentType, filename string, err error)
}
