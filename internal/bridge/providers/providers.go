package providers

import (
	"fmt"
	"os"

	"github.com/maniginam/waggle/internal/bridge"
	"github.com/maniginam/waggle/internal/bridge/bedrock"
	"github.com/maniginam/waggle/internal/bridge/claude"
	"github.com/maniginam/waggle/internal/bridge/gemini"
	oapkg "github.com/maniginam/waggle/internal/bridge/openai"
)

// DefaultRegistry returns a Registry pre-loaded with all supported providers.
func DefaultRegistry() *bridge.Registry {
	r := bridge.NewRegistry()

	r.Register("openai", func() (bridge.Bridge, error) {
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
		}
		return oapkg.New("https://api.openai.com/v1", key, "gpt-4o",
			[]bridge.Capability{bridge.CapChat, bridge.CapCode, bridge.CapVision}), nil
	})

	r.Register("grok", func() (bridge.Bridge, error) {
		key := os.Getenv("XAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("XAI_API_KEY environment variable is required")
		}
		return oapkg.New("https://api.x.ai/v1", key, "grok-3",
			[]bridge.Capability{bridge.CapChat, bridge.CapImageGen}), nil
	})

	r.Register("ollama", func() (bridge.Bridge, error) {
		return oapkg.New("http://localhost:11434/v1", "", "llama3",
			[]bridge.Capability{bridge.CapChat, bridge.CapCode}), nil
	})

	r.Register("gemini", func() (bridge.Bridge, error) {
		key := os.Getenv("GOOGLE_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("GOOGLE_API_KEY environment variable is required")
		}
		return gemini.New(key, "gemini-2.5-pro"), nil
	})

	r.Register("claude-api", func() (bridge.Bridge, error) {
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
		}
		return claude.New(key, "claude-sonnet-4-6"), nil
	})

	r.Register("bedrock", func() (bridge.Bridge, error) {
		region := os.Getenv("AWS_REGION")
		accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
		secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
		if region == "" || accessKey == "" || secretKey == "" {
			return nil, fmt.Errorf("AWS_REGION, AWS_ACCESS_KEY_ID, and AWS_SECRET_ACCESS_KEY are required")
		}
		return bedrock.New(region, accessKey, secretKey, "claude-sonnet-4-6"), nil
	})

	return r
}
