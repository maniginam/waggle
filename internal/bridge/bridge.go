package bridge

import "context"

// Bridge is the interface every model provider implements.
type Bridge interface {
	Chat(ctx context.Context, messages []Message, opts ChatOpts) (string, error)
	Capabilities() []Capability
	Provider() string
}

type Capability string

const (
	CapChat     Capability = "chat"
	CapCode     Capability = "code"
	CapImageGen Capability = "image_gen"
	CapVision   Capability = "vision"
)

type ChatOpts struct {
	Model        string
	MaxTokens    int
	Temperature  float64
	SystemPrompt string
}

type Message struct {
	Role    string // "system", "user", "assistant"
	Content string
}

type Mode string

const (
	ModeMessageOnly     Mode = "message_only"
	ModeFullParticipant Mode = "full_participant"
)
