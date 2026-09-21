package erc4337

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBundlerResponseEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"result", `{"jsonrpc":"2.0","id":1,"result":"accepted"}`, true},
		{"pending receipt", `{"jsonrpc":"2.0","id":1,"result":null}`, true},
		{"wrong id", `{"jsonrpc":"2.0","id":2,"result":"accepted"}`, false},
		{"string id", `{"jsonrpc":"2.0","id":"1","result":"accepted"}`, false},
		{"version", `{"jsonrpc":"1.0","id":1,"result":"accepted"}`, false},
		{"missing outcome", `{"jsonrpc":"2.0","id":1}`, false},
		{"both outcomes", `{"jsonrpc":"2.0","id":1,"result":null,"error":{"code":-32000,"message":"failure"}}`, false},
		{"trailing payload", `{"jsonrpc":"2.0","id":1,"result":null}{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
			defer server.Close()
			client, err := NewClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			result := json.RawMessage(`"unchanged"`)
			err = client.call(context.Background(), "fixture", nil, &result)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
			if !tc.valid && string(result) != `"unchanged"` {
				t.Fatal("invalid envelope modified result")
			}
		})
	}
}

type countingBundlerBody struct {
	remaining, read int
	closed          bool
}

func (b *countingBundlerBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := range n {
		p[i] = ' '
	}
	b.remaining -= n
	b.read += n
	return n, nil
}
func (b *countingBundlerBody) Close() error { b.closed = true; return nil }

type bundlerTransport func(*http.Request) (*http.Response, error)

func (f bundlerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBundlerResponseReadIsBounded(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := &countingBundlerBody{remaining: 10 * maxBundlerResponseBytes}
			client, _ := NewClient("http://unused.invalid")
			client.httpClient = &http.Client{Transport: bundlerTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
			})}
			err := client.call(context.Background(), "fixture", nil, nil)
			if err == nil || body.read > maxBundlerResponseBytes+1 || !body.closed {
				t.Fatalf("unbounded response: read=%d closed=%v err=%v", body.read, body.closed, err)
			}
			if status != http.StatusOK && body.read != 0 {
				t.Fatal("HTTP error page must not be read or echoed")
			}
		})
	}
	// The cap applies to decoded bytes, including a syntactically valid oversized JSON result.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":"`+strings.Repeat("x", maxBundlerResponseBytes)+`"}`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	var result string
	if err := client.call(context.Background(), "fixture", nil, &result); err == nil || result != "" {
		t.Fatal("oversized JSON result accepted")
	}
}
