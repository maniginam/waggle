package overseer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/maniginam/waggle/internal/model"
)

type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output() // stdout only
}

var gtAllowed = map[string]bool{"trail": true, "agents": true}

type GasTownSource struct {
	bin string
	run runner
}

func NewGasTownSource(bin string) *GasTownSource {
	return &GasTownSource{bin: bin, run: execRunner}
}

func (g *GasTownSource) Name() string { return "gastown" }

func (g *GasTownSource) Poll(ctx context.Context) (Snapshot, error) {
	out, err := g.runGT(ctx, "trail", "--json", "--limit", "20")
	if err != nil {
		log.Printf("overseer: gastown trail: %v", err)
		return Snapshot{}, nil // gt missing/broken -> empty, never fatal
	}
	return Snapshot{Items: parseTrail(out)}, nil
}

func (g *GasTownSource) runGT(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 0 || !gtAllowed[args[0]] {
		return nil, fmt.Errorf("gt subcommand not allowed: %v", args)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return g.run(cctx, g.bin, args...)
}

func parseTrail(out []byte) []Item {
	s := strings.TrimSpace(string(out))
	if s == "" || s == "null" {
		return nil
	}
	var commits []struct {
		Bead    string `json:"bead"`
		Agent   string `json:"agent"`
		Rig     string `json:"rig"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(s), &commits) != nil {
		return nil // tolerate warnings / non-JSON
	}
	items := make([]Item, 0, len(commits))
	for _, c := range commits {
		items = append(items, Item{
			Key: "gastown:commit:" + c.Bead,
			Event: &model.Event{
				Type:      "agent.commit",
				TaskID:    c.Bead,
				AgentID:   c.Agent,
				Payload:   map[string]any{"source": "gastown", "agent": c.Agent, "rig": c.Rig, "msg": c.Message},
				Timestamp: time.Now(),
			},
		})
	}
	return items
}
