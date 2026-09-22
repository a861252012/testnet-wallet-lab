package wallet

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestEscrowSpeedupJournalRecovery(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender := crypto.PubkeyToAddress(key.PublicKey)
	contract, other := common.Address{1}, common.Address{2}
	const reference = OrderReference("speedup-persisted-order")
	for _, action := range []TransactionAction{ActionEscrowFund, ActionEscrowRelease, ActionEscrowRefund} {
		t.Run(string(action), func(t *testing.T) {
			buyer := sender
			if action == ActionEscrowRefund {
				buyer = other
			}
			method := map[TransactionAction]string{ActionEscrowFund: "fund", ActionEscrowRelease: "release", ActionEscrowRefund: "refund"}[action]
			args := []any{buyer, reference.key()}
			if action == ActionEscrowFund {
				args = []any{reference.key(), other, big.NewInt(1_000_000)}
			}
			data, err := escrowABI.Pack(method, args...)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := types.SignNewTx(key, types.LatestSignerForChainID(big.NewInt(11155111)), &types.DynamicFeeTx{ChainID: big.NewInt(11155111), Nonce: 7, To: &contract, Gas: 100000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Data: data})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := tx.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			for _, legacy := range []struct {
				action    TransactionAction
				reference OrderReference
			}{{ActionSpeedup, reference}, {ActionSpeedup, ""}, {"", ""}} {
				saved := legacy.reference
				manager, err := NewJournalManager(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				record := &JournalRecord{Hash: TransactionHash(tx.Hash().Hex()), QuoteID: "speedup-test", State: JournalSubmitted, Action: legacy.action, Nonce: 7, SignedRaw: hexutil.Encode(raw), OrderID: saved, EscrowAction: ActionETH, EscrowBuyer: "spoofed", EscrowContract: "spoofed"}
				if _, err := manager.AppendAtomic(record); err != nil {
					t.Fatalf("speedup append: %v", err)
				}
				want := string(saved)
				if saved == "" {
					want = reference.key().Hex()
				}
				for round := range 2 {
					loaded := manager.FindByHash(tx.Hash().Hex())
					if loaded == nil || loaded.Action != legacy.action || loaded.EscrowAction != action || string(loaded.OrderID) != want || loaded.EscrowBuyer != EVMAddress(buyer.Hex()) || loaded.EscrowContract != EVMAddress(contract.Hex()) || loaded.SignedRaw != hexutil.Encode(raw) {
						t.Fatalf("round %d did not restore signed intent", round)
					}
					history := manager.ListHistory()
					encoded, err := json.Marshal(history[0])
					if err != nil {
						t.Fatal(err)
					}
					var item map[string]any
					if err := json.Unmarshal(encoded, &item); err != nil {
						t.Fatal(err)
					}
					if item["escrowAction"] != string(action) || item["orderId"] != want {
						t.Fatal("history lost original action", item)
					}
					manager, err = NewJournalManager(manager.walletDir)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestEscrowSpeedupJournalRejectsFalseMetadata(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	contract, seller := common.Address{1}, common.Address{2}
	const reference = OrderReference("signed-order")
	data, err := escrowABI.Pack("fund", reference.key(), seller, big.NewInt(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wrong reference", "wrong action", "ordinary action", "ordinary action without reference", "cancel action without reference", "cancel metadata", "cancel calldata", "truncated calldata", "trailing calldata", "nonzero value", "invalid signature"} {
		t.Run(name, func(t *testing.T) {
			unsigned := &types.DynamicFeeTx{ChainID: big.NewInt(11155111), To: &contract, Gas: 100000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Data: append([]byte(nil), data...)}
			record := &JournalRecord{QuoteID: "invalid-speedup", State: JournalSubmitted, Action: ActionSpeedup, OrderID: reference}
			switch name {
			case "wrong reference":
				record.OrderID = "unsigned-order"
			case "wrong action":
				record.Action = ActionEscrowRefund
			case "ordinary action":
				record.Action = ActionETH
			case "ordinary action without reference":
				record.Action, record.OrderID = ActionETH, ""
			case "cancel action without reference":
				record.Action, record.OrderID = ActionCancel, ""
			case "cancel metadata":
				record.Action = ActionCancel
				unsigned.Data = nil
			case "cancel calldata":
				unsigned.Data = nil
			case "truncated calldata":
				unsigned.Data = unsigned.Data[:len(unsigned.Data)-1]
			case "trailing calldata":
				unsigned.Data = append(unsigned.Data, make([]byte, 32)...)
			case "nonzero value":
				unsigned.Value = big.NewInt(1)
			}
			tx := types.NewTx(unsigned)
			if name != "invalid signature" {
				tx, err = types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(11155111)), key)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, err := tx.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			record.Hash, record.SignedRaw = TransactionHash(tx.Hash().Hex()), hexutil.Encode(raw)
			manager, err := NewJournalManager(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.AppendAtomic(record); err == nil {
				t.Fatal("accepted false escrow metadata")
			}
			if _, err := journalRecordFromDisk(journalRecordToDisk(record), 11155111); err == nil {
				t.Fatal("reloaded false escrow metadata")
			}
			if len(manager.ListHistory()) != 0 {
				t.Fatal("rejected record changed history")
			}
		})
	}
}
