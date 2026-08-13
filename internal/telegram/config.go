package telegram

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Token           string
	AllowedChats    []int64
	Port            string
	APIBaseURL      string
	TelegramBaseURL string
}

func ConfigFromEnv() Config {
	port := os.Getenv("WAGGLE_PORT")
	if port == "" {
		port = "4740"
	}
	token := os.Getenv("WAGGLE_TELEGRAM_TOKEN")
	var chats []int64
	for _, part := range strings.Split(os.Getenv("WAGGLE_TELEGRAM_ALLOWED_CHATS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			chats = append(chats, id)
		}
	}
	return Config{
		Token:           token,
		AllowedChats:    chats,
		Port:            port,
		APIBaseURL:      "http://localhost:" + port,
		TelegramBaseURL: "https://api.telegram.org/bot" + token,
	}
}

func (c Config) ChatAllowed(id int64) bool {
	for _, a := range c.AllowedChats {
		if a == id {
			return true
		}
	}
	return false
}
