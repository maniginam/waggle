package bedrock

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSignRequest(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-v2/invoke", strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")

	signTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	signRequest(req, "AKID", "SECRET", "us-east-1", "bedrock", signTime)

	auth := req.Header.Get("Authorization")
	if auth == "" {
		t.Fatal("expected Authorization header")
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("expected AWS4-HMAC-SHA256 prefix, got %s", auth[:30])
	}
	if !strings.Contains(auth, "AKID") {
		t.Error("expected access key ID in Authorization header")
	}

	amzDate := req.Header.Get("X-Amz-Date")
	if amzDate != "20260101T000000Z" {
		t.Errorf("expected X-Amz-Date 20260101T000000Z, got %s", amzDate)
	}
}

func TestSignRequestDeterministic(t *testing.T) {
	makeReq := func() *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "https://bedrock-runtime.us-east-1.amazonaws.com/invoke", strings.NewReader("body"))
		r.Header.Set("Content-Type", "application/json")
		return r
	}

	signTime := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	req1 := makeReq()
	req2 := makeReq()
	signRequest(req1, "AK", "SK", "us-east-1", "bedrock", signTime)
	signRequest(req2, "AK", "SK", "us-east-1", "bedrock", signTime)

	if req1.Header.Get("Authorization") != req2.Header.Get("Authorization") {
		t.Error("same inputs should produce same signature")
	}
}
