package push

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/maniginam/waggle/internal/store"
)

type Notifier struct {
	store      *store.Store
	vapidPub   string
	vapidPriv  string
	vapidEmail string
}

type vapidKeys struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

func NewNotifier(s *store.Store) (*Notifier, error) {
	n := &Notifier{
		store:      s,
		vapidEmail: "mailto:waggle@localhost",
	}
	if err := n.loadOrGenerateKeys(); err != nil {
		return nil, err
	}
	return n, nil
}

func (n *Notifier) loadOrGenerateKeys() error {
	home, _ := os.UserHomeDir()
	keyPath := filepath.Join(home, ".waggle", "vapid.json")

	if data, err := os.ReadFile(keyPath); err == nil {
		var keys vapidKeys
		if json.Unmarshal(data, &keys) == nil && keys.PublicKey != "" {
			n.vapidPub = keys.PublicKey
			n.vapidPriv = keys.PrivateKey
			return nil
		}
	}

	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return err
	}
	n.vapidPub = pub
	n.vapidPriv = priv

	keys := vapidKeys{PublicKey: pub, PrivateKey: priv}
	data, _ := json.MarshalIndent(keys, "", "  ")
	os.MkdirAll(filepath.Dir(keyPath), 0755)
	os.WriteFile(keyPath, data, 0600)
	log.Printf("generated VAPID keys, saved to %s", keyPath)
	return nil
}

func (n *Notifier) PublicKey() string {
	return n.vapidPub
}

type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url,omitempty"`
}

func (n *Notifier) Send(payload PushPayload) {
	subs, err := n.store.ListPushSubscriptions()
	if err != nil || len(subs) == 0 {
		return
	}

	data, _ := json.Marshal(payload)

	for _, sub := range subs {
		s := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				Auth:   sub.Auth,
				P256dh: sub.P256dh,
			},
		}
		resp, err := webpush.SendNotification(data, s, &webpush.Options{
			Subscriber:      n.vapidEmail,
			VAPIDPublicKey:  n.vapidPub,
			VAPIDPrivateKey: n.vapidPriv,
			TTL:             60,
		})
		if err != nil {
			log.Printf("push failed to %s: %v", sub.Endpoint[:40], err)
			// Remove invalid subscriptions (410 Gone)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 410 || resp.StatusCode == 404 {
			n.store.DeletePushSubscription(sub.Endpoint)
			log.Printf("removed stale push subscription")
		}
	}
}
