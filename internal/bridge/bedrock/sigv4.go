package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

func signRequest(req *http.Request, accessKey, secretKey, region, service string, t time.Time) {
	datestamp := t.Format("20060102")
	amzdate := t.Format("20060102T150405Z")

	req.Header.Set("X-Amz-Date", amzdate)
	req.Header.Set("Host", req.URL.Host)

	// Read and hash payload
	var payloadHash string
	if req.Body != nil {
		bodyBytes, _ := io.ReadAll(req.Body)
		payloadHash = hashSHA256(bodyBytes)
		req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
	} else {
		payloadHash = hashSHA256(nil)
	}

	// Canonical headers
	signedHeaderKeys := []string{"content-type", "host", "x-amz-date"}
	sort.Strings(signedHeaderKeys)

	var canonicalHeaders strings.Builder
	for _, key := range signedHeaderKeys {
		val := req.Header.Get(key)
		if key == "host" {
			val = req.URL.Host
		}
		canonicalHeaders.WriteString(key + ":" + strings.TrimSpace(val) + "\n")
	}
	signedHeaders := strings.Join(signedHeaderKeys, ";")

	// Canonical request
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Path,
		req.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	// String to sign
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", datestamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzdate,
		credentialScope,
		hashSHA256([]byte(canonicalRequest)),
	}, "\n")

	// Signing key
	signingKey := deriveKey(secretKey, datestamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature,
	))
}

func deriveKey(secret, datestamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hashSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
