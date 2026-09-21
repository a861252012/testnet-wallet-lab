package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestE2EEscrowOrderRecovery(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "saved-reference"
		if legacy {
			name = "legacy-key"
		}
		t.Run(name, func(t *testing.T) {
			f := newEscrowE2E(t)
			const reference = "recover-funded-order"
			f.approve(0, "1000000")
			funded := f.send(0, f.quote(0, map[string]string{"action": "escrow_fund", "to": f.addresses[1].Hex(), "amount": "1", "orderId": reference}, 200))
			f.history(0)
			f.checkReceipt(funded, "Funded", reference, 1_000_000)
			f.servers[0].Close()
			f.services[0].Close()
			want := reference
			if legacy {
				// Recreate the old disk format; signed transaction bytes remain unchanged.
				file := filepath.Join(f.dirs[0], "journal.json")
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				var records []map[string]json.RawMessage
				if err := json.Unmarshal(data, &records); err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					delete(record, "orderId")
				}
				data, err = json.Marshal(records)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
				want = crypto.Keccak256Hash([]byte(reference)).Hex()
			}
			f.open(0)
			history := f.request(0, "GET", "/api/wallet/history", nil, 200)["transactions"].([]any)
			found := false
			for _, value := range history {
				tx := value.(map[string]any)
				if tx["hash"] == funded["hash"] {
					found = true
					if tx["orderId"] != want || tx["escrowBuyer"] != f.addresses[0].Hex() || tx["escrowContract"] != f.contract.Hex() {
						t.Fatalf("lost recovery fields after restart: %v", tx)
					}
				}
			}
			if !found || f.order(0, want)["state"] != "funded" {
				t.Fatal("could not recover funded order")
			}
			if os.Getenv("RUN_BROWSER_E2E") == "1" {
				before := f.sendCount.Load()
				cmd := exec.Command("node", filepath.Join("..", "browser", "escrow-recovery.cjs"))
				cmd.Env = append(os.Environ(), "E2E_BACKEND=simulated", "E2E_BUYER_URL="+f.servers[0].URL, "E2E_ORDER_ID="+want)
				output, err := cmd.CombinedOutput()
				t.Log(string(output))
				if err != nil {
					t.Fatal(err)
				}
				if f.sendCount.Load() != before {
					t.Fatal("viewing recovery sent a transaction")
				}
			}
			// The recovered identifier must work for both settlement paths, not only lookup.
			action, person, to, event := "escrow_release", 0, f.addresses[1], "Released"
			if legacy {
				action, person, to, event = "escrow_refund", 1, f.addresses[0], "Refunded"
			}
			settled := f.send(person, f.quote(person, map[string]string{"action": action, "to": to.Hex(), "buyer": f.addresses[0].Hex(), "orderId": want}, 200))
			f.history(person)
			f.checkReceipt(settled, event, reference, 1_000_000)
			for i := range 2 {
				if _, err := f.services[i].History(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
