package wallet

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sol "github.com/gagliardetto/solana-go"
)

func TestSolanaHistoryBatchesRotateAfterRPCFailure(t *testing.T) {
	calls := 0
	seen := map[string]bool{}
	var newest sol.Signature
	binary.BigEndian.PutUint32(newest[:4], 600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
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
			result = 300
		case "getSignatureStatuses":
			calls++
			var signatures []string
			if err := json.Unmarshal(req.Params[0], &signatures); err != nil {
				t.Error(err)
				return
			}
			if len(signatures) > 256 {
				t.Errorf("RPC batch exceeds limit: %d", len(signatures))
			}
			if calls == 1 {
				if signatures[0] != newest.String() {
					t.Error("newest transfer was not queried first")
				}
				http.Error(w, "fixture RPC unavailable", http.StatusGatewayTimeout)
				return
			}
			statuses := make([]any, len(signatures))
			for i, signature := range signatures {
				seen[signature] = true
				if signature == newest.String() {
					statuses[i] = map[string]any{"slot": 290, "err": nil, "confirmationStatus": "finalized", "confirmations": nil}
				}
			}
			result = map[string]any{"context": map[string]int{"slot": 300}, "value": statuses}
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	s, err := NewSolanaService(server.URL, t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 1; i <= 600; i++ {
		var signature sol.Signature
		binary.BigEndian.PutUint32(signature[:4], uint32(i))
		state, lastValid := crosschainState("expired_unconfirmed"), uint64(100)
		if i == 600 {
			state, lastValid = "submitted", 400
		}
		s.records = append(s.records, solanaJournalRecord{Signature: solanaSignature(signature.String()), State: state, LastValid: lastValid})
	}
	for attempt := range 4 {
		history, err := s.History(context.Background())
		if attempt == 0 {
			if !errors.Is(err, errSolRPC) {
				t.Fatalf("failed refresh must report the sanitized RPC error: %v", err)
			}
		} else if err != nil {
			t.Fatalf("history did not recover: %v", err)
		}
		if len(history) != 20 {
			t.Fatalf("history: %d, %v", len(history), err)
		}
	}
	if calls != 4 || len(seen) != 600 {
		t.Fatalf("unfair/boundless polling: calls=%d seen=%d", calls, len(seen))
	}
	if s.hasPending() || !s.records[599].Finalized {
		t.Fatal("new payment starved behind expired history")
	}
}

func TestSendRejectionDistinguishesUnknownOutcome(t *testing.T) {
	s, state := guardedFixture(t)
	q := ethQuote(t, s)
	_, err := s.Send(context.Background(), q.ID, "incorrect-password")
	var rejected *SendRejectedError
	if !errors.As(err, &rejected) || !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("password rejection lost its cause or classification: %v", err)
	}
	if len(s.journal.ListHistory()) != 0 {
		t.Fatal("password failure prepared a transaction")
	}
	sent, err := s.Send(context.Background(), q.ID, "fixture-password-123")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Send(context.Background(), q.ID, "incorrect-password")
	if err != nil || duplicate.Hash != sent.Hash {
		t.Fatalf("duplicate quote must return its existing transaction: %+v %v", duplicate, err)
	}
	state.mu.Lock()
	broadcasts := len(state.raws)
	state.mu.Unlock()
	if broadcasts != 1 {
		t.Fatalf("duplicate quote broadcast %d times", broadcasts)
	}
	s.storageFault.Store(true)
	_, err = s.Send(context.Background(), q.ID, "fixture-password-123")
	if err == nil || errors.As(err, &rejected) {
		t.Fatalf("storage fault must retain unknown outcome: %v", err)
	}
}
