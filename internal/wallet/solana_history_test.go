package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	sol "github.com/gagliardetto/solana-go"
)

func TestSolanaHistoryRefreshFailureAndRecovery(t *testing.T) {
	for _, failure := range []string{"rpc-error", "nil-result", "short-result", "long-result", "height-error"} {
		for _, state := range []crosschainState{"submitted", "expired_unconfirmed"} {
			t.Run(failure+"/"+string(state), func(t *testing.T) {
				var recovered atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						ID     any    `json:"id"`
						Method string `json:"method"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						return
					}
					var result any
					switch req.Method {
					case "getGenesisHash":
						result = SolanaDevnetGenesis
					case "getBlockHeight":
						if failure == "height-error" && !recovered.Load() {
							json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "https://rpc.example/?token=private-secret"}})
							return
						}
						result = 300
					case "getSignatureStatuses":
						values := []any{map[string]any{"slot": 100, "err": nil, "confirmationStatus": "finalized", "confirmations": nil}}
						if !recovered.Load() {
							switch failure {
							case "rpc-error":
								json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "https://rpc.example/?token=private-secret"}})
								return
							case "nil-result":
								json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": nil})
								return
							case "short-result":
								values = []any{}
							case "long-result":
								values = append(values, nil)
							case "height-error":
								values = []any{nil}
							}
						}
						result = map[string]any{"context": map[string]int{"slot": 100}, "value": values}
					default:
						t.Errorf("unexpected Solana RPC %s", req.Method)
					}
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
				}))
				defer server.Close()
				s, err := NewSolanaService(server.URL, t.TempDir(), 2)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				s.records = []solanaJournalRecord{{Signature: solanaSignature(sol.Signature{}.String()), State: state, LastValid: 200}}
				if err := s.persist(); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(s.dir, "transactions.json")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				history, err := s.History(context.Background())
				if !errors.Is(err, errSolRPC) || len(history) != 1 {
					t.Fatalf("refresh must return retained records and sanitized error: %+v, %v", history, err)
				}
				if history[0].State != string(state) || history[0].Finalized || s.records[0].State != state || s.records[0].Finalized {
					t.Fatalf("failed refresh changed retained state: %+v, %+v", history, s.records)
				}
				if s.hasPending() != (state == "submitted") {
					t.Fatal("failed refresh changed pending gate")
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("failed refresh changed journal: %v", err)
				}
				recovered.Store(true)
				history, err = s.History(context.Background())
				if err != nil || len(history) != 1 || history[0].State != "finalized" || !history[0].Finalized || s.hasPending() {
					t.Fatalf("receipt recovery did not finalize retained record: %+v, %v", history, err)
				}
				after, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var stored []solanaDiskRecord
				if err := json.Unmarshal(after, &stored); err != nil || len(stored) != 1 || stored[0].State != "finalized" || !stored[0].Finalized {
					t.Fatalf("recovered state not persisted: %s, %v", after, err)
				}
			})
		}
	}
}

func TestSolanaHistoryReceiptDoesNotRequireHeight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		switch req.Method {
		case "getGenesisHash":
			result = SolanaDevnetGenesis
		case "getBlockHeight":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "height unavailable"}})
			return
		case "getSignatureStatuses":
			result = map[string]any{"context": map[string]int{"slot": 100}, "value": []any{map[string]any{"slot": 100, "err": nil, "confirmationStatus": "finalized", "confirmations": nil}}}
		default:
			t.Errorf("unexpected Solana RPC %s", req.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	s, err := NewSolanaService(server.URL, t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.records = []solanaJournalRecord{{Signature: solanaSignature(sol.Signature{}.String()), State: "submitted", LastValid: 200}}
	for range 2 {
		history, err := s.History(context.Background())
		if err != nil || len(history) != 1 || !history[0].Finalized || history[0].State != "finalized" {
			t.Fatalf("known receipt must not depend on height: %+v, %v", history, err)
		}
	}
}
