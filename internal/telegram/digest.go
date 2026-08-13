package telegram

import "context"

type Digester struct {
	wg    *WaggleClient
	tg    *Client
	chats []int64
}

func NewDigester(wg *WaggleClient, tg *Client, chats []int64) *Digester {
	return &Digester{wg: wg, tg: tg, chats: chats}
}

func (d *Digester) buildDigest(ctx context.Context) (string, error) {
	body, err := d.wg.WhatsNext()
	if err != nil {
		return "", err
	}
	return "Daily Waggle digest:\n" + string(body), nil
}

func (d *Digester) SendDigest(ctx context.Context) {
	msg, err := d.buildDigest(ctx)
	if err != nil {
		msg = "digest error: " + err.Error()
	}
	for _, chat := range d.chats {
		d.tg.SendMessage(ctx, chat, msg, nil)
	}
}
