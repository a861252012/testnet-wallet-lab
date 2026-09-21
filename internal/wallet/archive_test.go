package wallet

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func finalizedArchiveRecords(t *testing.T, count int) []*JournalRecord {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	defer wipePrivateKey(key)
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	records := make([]*JournalRecord, 0, count)
	for i := 0; i < count; i += 1 {
		tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(11155111), Nonce: uint64(i), To: &to, Gas: 21000, GasFeeCap: big.NewInt(2), GasTipCap: big.NewInt(1), Value: big.NewInt(1)}), types.LatestSignerForChainID(big.NewInt(11155111)), key)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := tx.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, &JournalRecord{Hash: TransactionHash(tx.Hash().Hex()), QuoteID: QuoteID(fmt.Sprint(i)), Nonce: uint64(i), SignedRaw: hexutil.Encode(raw), State: JournalSucceeded, Finalized: true, Version: 1, CreatedAt: time.Now().UTC()})
	}
	return records
}

func TestArchivePreservesHistoryAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	jm, err := NewJournalManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	jm.records = finalizedArchiveRecords(t, 900)
	jm.records[899].Finalized = false
	if err := jm.atomicSave(jm.records); err != nil {
		t.Fatal(err)
	}
	saved := append([]*JournalRecord{}, jm.records...)
	count, err := jm.ArchiveFinalized()
	if err != nil || count != 800 || len(jm.records) != 100 {
		t.Fatalf("archive count %d err %v", count, err)
	}
	restarted, err := NewJournalManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.ListHistory()) != 900 || restarted.FindByQuoteID("0") == nil {
		t.Fatal("lost archived history/idempotency")
	}
	if restarted.HasInFlightTx() {
		t.Fatal("archived success blocks new sends")
	}
	if len(restarted.RefreshItems()) != 1 {
		t.Fatal("unfinalized record not tracked")
	}
	// Simulate a crash after archive persistence but before shortening the active journal.
	if err := restarted.atomicSave(saved); err != nil {
		t.Fatal(err)
	}
	recovered, err := NewJournalManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.ListHistory()) != 900 {
		t.Fatal("duplicated history after interrupted compaction")
	}
}

func TestJournalArchivesTreatWalletDirectoryLiterally(t *testing.T) {
	for _, name := range []string{"wallet", "[wallet]", "wallet[", "wallet*"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			records := finalizedArchiveRecords(t, 3)
			// Create archives out of order; loading must retain filename order.
			for _, item := range []struct {
				path   string
				record *JournalRecord
			}{
				{filepath.Join(dir, "archive-b.json"), records[1]},
				{filepath.Join(dir, "archive-a.json"), records[0]},
				{filepath.Join(root, "wallet-other", "archive-other.json"), records[2]},
				{filepath.Join(root, "w", "archive-other.json"), records[2]},
			} {
				data, err := json.Marshal([]*journalRecordDisk{journalRecordToDisk(item.record)})
				if err != nil {
					t.Fatal(err)
				}
				if err := atomicWriteFile(item.path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				jm, err := NewJournalManager(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(jm.ListHistory()) != 2 || len(jm.archived) != 2 {
					t.Fatalf("restart loaded %d archives; want 2 from the literal directory", len(jm.archived))
				}
				for i, want := range records[:2] {
					if jm.archived[i].Hash != want.Hash {
						t.Fatal("archive filename order changed")
					}
					if got := jm.FindByQuoteID(string(want.QuoteID)); got == nil || got.Hash != want.Hash {
						t.Fatalf("archived quote %s lost its idempotency record", want.QuoteID)
					}
				}
				if jm.FindByHash(string(records[2].Hash)) != nil {
					t.Fatal("loaded a neighboring wallet's archive")
				}
			}
		})
	}
}

func TestJournalArchiveLoadErrors(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		jm, err := NewJournalManager(filepath.Join(t.TempDir(), "missing"))
		if err != nil || len(jm.ListHistory()) != 0 {
			t.Fatalf("missing directory must start empty: %v", err)
		}
	})
	t.Run("unreadable archive", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "archive-directory.json"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := NewJournalManager(dir); err == nil {
			t.Fatal("expected an error reading an archive directory")
		}
	})
	t.Run("malformed archive", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "archive-broken.json"), []byte("{"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewJournalManager(dir); err == nil {
			t.Fatal("expected malformed archive error")
		}
	})
}
