package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type tronHistoryTransport func(*http.Request) (*http.Response, error)

func (f tronHistoryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise scheduling and persistence with isolated journal records; signed
// transaction creation and restart validation are covered by tron_test.go.
func newTronHistoryService(t *testing.T, receipt func(*http.Request, string) (int, string)) *TronService {
	t.Helper()
	s, err := NewTronService("http://127.0.0.1", "", t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.records = []tronJournalRecord{
		{Signature: tronTransactionID(strings.Repeat("a", 64)), QuoteID: "old", State: "expired_unconfirmed", Expires: time.Now().Add(-time.Minute)},
		{Signature: tronTransactionID(strings.Repeat("b", 64)), QuoteID: "new", State: "submitted", Expires: time.Now().Add(time.Minute)},
	}
	s.http.Transport = tronHistoryTransport(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, ""
		switch r.URL.Path {
		case "/wallet/getblockbynum":
			body = `{"blockID":"` + TronShastaGenesis + `"}`
		default:
			var request struct{ Value string }
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			status, body = receipt(r, request.Value)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	return s
}

func tronHistorySolidBlock() string {
	data, _ := json.Marshal(map[string]any{"blockID": strings.Repeat("c", 64), "block_header": map[string]any{"raw_data": map[string]any{"timestamp": time.Now().Add(-time.Second).UnixMilli()}}})
	return string(data)
}

func tronHistoryReceipt(hash string) string {
	return `{"id":"` + hash + `","blockNumber":100,"fee":1000,"receipt":{"result":"SUCCESS"}}`
}

func TestTronHistoryIsolatesRecordFailuresAndPersistsProgress(t *testing.T) {
	for _, failure := range []string{"receipt-rpc", "wrong-receipt", "invalid-contract-result", "solid-rpc", "invalid-solid"} {
		t.Run(failure, func(t *testing.T) {
			failed := true
			newCalls := 0
			s := newTronHistoryService(t, func(r *http.Request, hash string) (int, string) {
				if r.URL.Path == "/walletsolidity/getnowblock" {
					if failed && failure == "solid-rpc" {
						return 503, "unavailable"
					}
					if failed && failure == "invalid-solid" {
						return 200, `{}`
					}
					return 200, tronHistorySolidBlock()
				}
				if hash == strings.Repeat("b", 64) {
					newCalls++
					return 200, tronHistoryReceipt(hash)
				}
				if failed {
					switch failure {
					case "receipt-rpc":
						return 503, "unavailable"
					case "wrong-receipt":
						return 200, tronHistoryReceipt(strings.Repeat("d", 64))
					case "invalid-contract-result":
						return 200, tronHistoryReceipt(hash)
					}
				}
				return 200, `{}`
			})
			if failure == "invalid-contract-result" {
				s.records[0].Contract = "synthetic-token"
			}
			old := s.records[0]
			for range 3 {
				if _, err := s.History(context.Background()); !errors.Is(err, errTronRPC) {
					t.Fatalf("failure was hidden: %v", err)
				}
				if s.records[0] != old || !s.records[1].Finalized || s.pending() || newCalls != 1 {
					t.Fatalf("failed record changed or newer transaction blocked: %+v, calls=%d", s.records, newCalls)
				}
			}
			data, err := os.ReadFile(filepath.Join(s.dir, "transactions.json"))
			var stored []tronDiskRecord
			if err != nil || json.Unmarshal(data, &stored) != nil || len(stored) != 2 || stored[0].Finalized || !stored[1].Finalized {
				t.Fatalf("partial progress not durable: %s, %v", data, err)
			}
			failed = false
			if _, err := s.History(context.Background()); err != nil || s.records[0].ExpiryCheckedBlock == "" || s.records[0].Finalized || len(s.records) != 2 {
				t.Fatalf("old expired record lost or cannot recover: %+v, %v", s.records, err)
			}
		})
	}
}

func TestTronHistoryDeadlineRotatesAndSavesProgress(t *testing.T) {
	var queried []string
	s := newTronHistoryService(t, func(r *http.Request, hash string) (int, string) {
		if r.URL.Path == "/walletsolidity/getnowblock" {
			return 200, tronHistorySolidBlock()
		}
		queried = append(queried, hash)
		if hash == strings.Repeat("a", 64) {
			<-r.Context().Done()
			return 503, "deadline"
		}
		return 200, tronHistoryReceipt(hash)
	})
	for round := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, err := s.History(ctx)
		cancel()
		if !errors.Is(err, errTronRPC) {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	if len(queried) != 3 || queried[0] != strings.Repeat("a", 64) || queried[1] != strings.Repeat("b", 64) || queried[2] != queried[0] || !s.records[1].Finalized || s.pending() {
		t.Fatalf("deadline starved the next record: requests=%v records=%+v", queried, s.records)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "transactions.json"))
	var stored []tronDiskRecord
	if err != nil || json.Unmarshal(data, &stored) != nil || len(stored) != 2 || !stored[1].Finalized {
		t.Fatalf("progress before deadline not persisted: %s, %v", data, err)
	}
}

func TestTronHistorySaveFailureStillFaults(t *testing.T) {
	s := newTronHistoryService(t, func(r *http.Request, hash string) (int, string) {
		if r.URL.Path == "/walletsolidity/getnowblock" {
			return 503, "unavailable"
		}
		return 200, tronHistoryReceipt(hash)
	})
	if err := os.Mkdir(filepath.Join(s.dir, "transactions.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.History(context.Background()); err == nil || errors.Is(err, errTronRPC) || !s.fault || s.records[1].Finalized {
		t.Fatalf("save failure was hidden or memory advanced: fault=%v err=%v records=%+v", s.fault, err, s.records)
	}
}
