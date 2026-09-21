package wallet

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestEscrowJournalBindsOrderToSignedTransaction(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	buyer := crypto.PubkeyToAddress(key.PublicKey)
	contract := common.HexToAddress("0x1111111111111111111111111111111111111111")
	seller := common.HexToAddress("0x2222222222222222222222222222222222222222")
	reference := OrderReference("persisted-order")
	for _, action := range []TransactionAction{ActionEscrowFund, ActionEscrowRelease, ActionEscrowRefund} {
		t.Run(string(action), func(t *testing.T) {
			method := map[TransactionAction]string{ActionEscrowFund: "fund", ActionEscrowRelease: "release", ActionEscrowRefund: "refund"}[action]
			args := []any{buyer, reference.key()}
			if action == ActionEscrowFund {
				args = []any{reference.key(), seller, big.NewInt(1_000_000)}
			}
			data, err := escrowABI.Pack(method, args...)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(11155111), To: &contract, Gas: 100000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Data: data}), types.LatestSignerForChainID(big.NewInt(11155111)), key)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := tx.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			record := &JournalRecord{Hash: TransactionHash(tx.Hash().Hex()), QuoteID: "escrow-test", State: JournalSucceeded, Action: action, SignedRaw: hexutil.Encode(raw), OrderID: reference}
			for _, legacy := range []bool{false, true} {
				stored := journalRecordToDisk(record)
				want := string(reference)
				if legacy {
					stored.OrderID = ""
					want = reference.key().Hex()
				}
				loaded, err := journalRecordFromDisk(stored, 11155111)
				if err != nil || string(loaded.OrderID) != want || loaded.EscrowBuyer != EVMAddress(buyer.Hex()) || loaded.EscrowContract != EVMAddress(contract.Hex()) {
					t.Fatalf("legacy=%v record=%+v err=%v", legacy, loaded, err)
				}
				// Both active and archived records expose recovery data without decoding again on each poll.
				manager := &JournalManager{records: []*JournalRecord{loaded}}
				history := manager.ListHistory()
				if history[0].OrderID != want || history[0].EscrowBuyer != buyer.Hex() || history[0].EscrowContract != contract.Hex() {
					t.Fatalf("history lost recovery fields: %+v", history)
				}
				manager.archived, manager.records = manager.records, nil
				if manager.ListHistory()[0] != history[0] {
					t.Fatal("archived order lost recovery fields")
				}
			}
			for _, invalid := range []string{"other-order", "<invalid>", common.HexToHash("0x123").Hex()} {
				stored := journalRecordToDisk(record)
				stored.OrderID = invalid
				if _, err := journalRecordFromDisk(stored, 11155111); err == nil {
					t.Fatalf("accepted altered order reference %q", invalid)
				}
			}
			stored := journalRecordToDisk(record)
			stored.Action = string(ActionETH)
			if _, err := journalRecordFromDisk(stored, 11155111); err == nil {
				t.Fatal("accepted order metadata on ordinary transfer")
			}
			stored.Action = string(ActionEscrowFund)
			if action == ActionEscrowFund {
				stored.Action = string(ActionEscrowRefund)
			}
			if _, err := journalRecordFromDisk(stored, 11155111); err == nil {
				t.Fatal("accepted action inconsistent with signed calldata")
			}
		})
	}
}
