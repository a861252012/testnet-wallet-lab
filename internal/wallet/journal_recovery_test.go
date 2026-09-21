package wallet

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestJournalVersionUpdateAfterArchiveIsStale(t *testing.T) {
	jm, err := NewJournalManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jm.records = signedArchiveRecords(t, 900, 900)
	target := *jm.records[0]
	if count, err := jm.ArchiveFinalized(); err != nil || count != 800 {
		t.Fatalf("archive: count=%d err=%v", count, err)
	}
	// Archival itself need not change the version, but the result is still stale.
	updated, err := jm.UpdateStateAtomicIfVersion(string(target.Hash), target.Version, "pending", "", "", "")
	if err != nil || updated {
		t.Fatalf("archived update: updated=%v err=%v", updated, err)
	}
	if got := jm.FindByHash(string(target.Hash)); got == nil || *got != target {
		t.Fatal("stale update changed the archived record")
	}
	missing := signedJournalRecord(t, "missing")
	if updated, err := jm.UpdateStateAtomicIfVersion(string(missing.Hash), 1, "pending", "", "", ""); err == nil || updated {
		t.Fatalf("missing record must remain an error: updated=%v err=%v", updated, err)
	}
}

func TestRetryConcurrentArchivePreservesTerminalState(t *testing.T) {
	for _, broadcastFails := range []bool{false, true} {
		name := "broadcast success"
		if broadcastFails {
			name = "broadcast error"
		}
		t.Run(name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			client := mockRPC(t, func(method string, params json.RawMessage) any {
				switch method {
				case "eth_chainId":
					return "0xaa36a7"
				case "eth_sendRawTransaction":
					close(started)
					<-release
					if broadcastFails {
						return nil
					}
					var args []string
					if err := json.Unmarshal(params, &args); err != nil || len(args) != 1 {
						t.Errorf("invalid broadcast arguments: %s", params)
						return nil
					}
					raw, err := hexutil.Decode(args[0])
					if err != nil {
						t.Error(err)
						return nil
					}
					var tx types.Transaction
					if err := tx.UnmarshalBinary(raw); err != nil {
						t.Error(err)
						return nil
					}
					return tx.Hash().Hex()
				}
				return nil
			})
			svc, err := NewService(client, t.TempDir(), 2, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				unblock()
				svc.Close()
			}()
			records := signedArchiveRecords(t, 900, 799)
			target := records[799]
			target.State, target.Finalized = JournalBroadcastUnknown, false
			svc.journal.records = records
			if err := svc.journal.atomicSave(records); err != nil {
				t.Fatal(err)
			}
			type retryResult struct {
				response *SendResponse
				err      error
			}
			result := make(chan retryResult, 1)
			go func() {
				response, err := svc.Retry(context.Background(), string(target.Hash))
				result <- retryResult{response: response, err: err}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("retry did not reach broadcast")
			}
			updated, err := svc.journal.UpdateStateAtomicIfVersion(string(target.Hash), target.Version, "succeeded", "100", "0.001", "", true)
			if err != nil || !updated {
				t.Fatalf("finalize: updated=%v err=%v", updated, err)
			}
			if count, err := svc.journal.ArchiveFinalized(); err != nil || count != 800 {
				t.Fatalf("archive: count=%d err=%v", count, err)
			}
			before := *svc.journal.FindByHash(string(target.Hash))
			unblock()
			select {
			case got := <-result:
				if got.err != nil {
					t.Fatalf("retry failed after archival: %v", got.err)
				}
				if got.response == nil || got.response.Hash != string(target.Hash) || got.response.State != "succeeded" {
					t.Fatalf("retry must return the latest terminal response: %+v", got.response)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("retry did not finish")
			}
			if svc.storageFault.Load() {
				t.Fatal("concurrent archival must not set storageFault")
			}
			if got := svc.journal.FindByHash(string(target.Hash)); got == nil || *got != before {
				t.Fatal("retry overwrote the archived terminal record")
			}
			restarted, err := NewJournalManager(svc.journal.walletDir)
			if err != nil {
				t.Fatal(err)
			}
			if got := restarted.FindByHash(string(target.Hash)); got == nil || *got != before {
				t.Fatal("terminal record did not survive restart")
			}
		})
	}
}
