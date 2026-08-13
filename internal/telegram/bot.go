package telegram

import (
	"context"
	"log"
	"time"

	"github.com/maniginam/waggle/internal/event"
)

type Bot struct {
	cfg      Config
	tg       *Client
	router   *Router
	notifier *Notifier
	digester *Digester
}

func New(hub *event.Hub, cfg Config) *Bot {
	tg := NewClient(cfg.TelegramBaseURL)
	wg := NewWaggleClient(cfg.APIBaseURL)
	notifier := NewNotifier(hub, tg, cfg.AllowedChats)
	handler := NewHandler(tg, wg, notifier)
	var nl NLParser
	if p, ok := NewClaudeNLParser(); ok {
		nl = p
	}
	return &Bot{
		cfg:      cfg,
		tg:       tg,
		router:   NewRouter(cfg, handler, nl),
		notifier: notifier,
		digester: NewDigester(wg, tg, cfg.AllowedChats),
	}
}

func (b *Bot) Run(ctx context.Context) {
	go b.notifier.Run(ctx)

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.digester.SendDigest(ctx)
			}
		}
	}()

	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := b.tg.GetUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("telegram getUpdates: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("telegram dispatch panic: %v", r)
					}
				}()
				b.router.Dispatch(ctx, u)
			}()
		}
	}
}
