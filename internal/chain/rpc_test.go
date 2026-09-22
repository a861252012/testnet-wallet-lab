package chain

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
)

func TestCallContractPreservesOnlyRevertErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		code       int
		message    string
		data       any
		wantRevert bool
	}{
		{name: "geth revert data", code: 3, message: "contract call failed", data: "0xcf479181", wantRevert: true},
		{name: "execution reverted", code: -32000, message: "execution reverted: balance too low", data: "0xcf479181", wantRevert: true},
		{name: "execution reverted without data", code: -32000, message: "execution reverted", wantRevert: true},
		{name: "VM execution error", code: -32015, message: "VM execution error.", data: "0xcf479181", wantRevert: true},
		{name: "rate limit", code: -32005, message: "rate limit: private-api-key"},
		{name: "provider address", code: -32000, message: "0x1111111111111111111111111111111111111111 is not available"},
		{name: "unrelated error data", code: -32005, message: "quota exceeded", data: "private-api-key"},
		{name: "invalid revert data", code: 3, message: "provider error", data: "not-hex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     any    `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
				if req.Method == "eth_chainId" {
					response["result"] = "0xaa36a7"
				} else {
					errorResponse := map[string]any{"code": tc.code, "message": tc.message}
					if tc.data != nil {
						errorResponse["data"] = tc.data
					}
					response["error"] = errorResponse
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			to := common.Address{1}
			_, err = client.CallContract(context.Background(), ethereum.CallMsg{To: &to}, nil)
			if !tc.wantRevert {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("provider error was not redacted: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.message {
				t.Fatalf("revert message changed: %v", err)
			}
			dataErr, ok := errors.AsType[rpc.DataError](err)
			if !ok || dataErr.ErrorData() != tc.data {
				t.Fatalf("revert data changed: %v", err)
			}
		})
	}
}

func TestFinalizedNonceRequiresTaggedNetworkEvidence(t *testing.T) {
	for _, scenario := range []string{"success", "unsupported", "RPC error", "wrong network", "network changed", "malformed nonce"} {
		t.Run(scenario, func(t *testing.T) {
			chainChecks, nonceChecks := 0, 0
			account := common.Address{1}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     any      `json:"id"`
					Method string   `json:"method"`
					Params []string `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
				switch req.Method {
				case "eth_chainId":
					chainChecks++
					response["result"] = "0xaa36a7"
					if scenario == "wrong network" || scenario == "network changed" && chainChecks > 1 {
						response["result"] = "0x1"
					}
				case "eth_getTransactionCount":
					nonceChecks++
					if len(req.Params) != 2 || req.Params[0] != account.Hex() || req.Params[1] != "finalized" {
						t.Errorf("wrong nonce query: %v", req.Params)
					}
					response["result"] = "0x2"
					if scenario == "malformed nonce" {
						response["result"] = "2"
					}
					if scenario == "unsupported" || scenario == "RPC error" {
						delete(response, "result")
						code := -32602
						if scenario == "RPC error" {
							code = -32000
						}
						response["error"] = map[string]any{"code": code, "message": "unavailable"}
					}
				default:
					t.Errorf("unexpected method: %s", req.Method)
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			// Simulate a previously cached network identity; recovery must recheck it.
			client.networkVerified.Store(true)
			nonce, err := client.FinalizedNonceAt(context.Background(), account)
			if scenario == "success" {
				if err != nil || nonce != 2 || chainChecks != 2 || nonceChecks != 1 {
					t.Fatalf("nonce=%d err=%v checks=%d/%d", nonce, err, chainChecks, nonceChecks)
				}
			} else if err == nil || nonce != 0 {
				t.Fatalf("accepted invalid evidence: %d %v", nonce, err)
			}
		})
	}
}
