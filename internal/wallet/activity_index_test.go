package wallet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
)

func TestActivityIndexReusesSnapshotAndRebuildsAfterWrite(t *testing.T) {
	s := &Service{walletDir: t.TempDir()}
	ids := make([]string, 1002)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if _, err := s.addActivityHashes(ids[:1001]); err != nil {
		t.Fatal(err)
	}
	first, err := s.loadActivityIndex()
	if err != nil {
		t.Fatal(err)
	}
	// Hide the directory after warming the index: another directory/archive scan
	// would return an empty history. This is only an I/O probe, not a supported
	// live storage repair; restore it before any writes.
	hidden := s.walletDir + "-hidden"
	if err := os.Rename(s.walletDir, hidden); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(hidden, s.walletDir) })
	for range 3 {
		got, err := s.loadActivityIndex()
		if err != nil || got != first || !slices.Equal(got.ids, ids[:1001]) {
			t.Fatalf("warm index was rebuilt or changed: %v", err)
		}
	}
	if err := os.Rename(hidden, s.walletDir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.addActivityHashes(ids[1001:]); err != nil {
		t.Fatal(err)
	}
	updated, err := s.loadActivityIndex()
	if err != nil || updated == first || !slices.Equal(updated.ids, ids) {
		t.Fatalf("write did not refresh index: %v", err)
	}
	if !slices.Equal(first.ids, ids[:1001]) || first.seen[ids[1001]] {
		t.Fatal("refresh mutated a published snapshot")
	}
	restarted := &Service{walletDir: s.walletDir}
	got, err := restarted.activityHashes()
	if err != nil || !slices.Equal(got, ids) {
		t.Fatalf("restart did not rebuild index: %v", err)
	}
}

func TestActivityIndexInvalidatedAfterPartialArchiveFailure(t *testing.T) {
	s := &Service{walletDir: t.TempDir()}
	ids := make([]string, 2500)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if _, err := s.addActivityHashes(ids[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.loadActivityIndex(); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected archive write failure")
	writes := 0
	s.writeActivityArchive = func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes == 2 {
			return failure
		}
		return atomicWriteFile(path, data, mode)
	}
	if _, err := s.addActivityHashes(ids[1:]); !errors.Is(err, failure) {
		t.Fatalf("expected second archive write failure, got %v", err)
	}
	got, err := s.activityHashes()
	if err != nil || !slices.Equal(got, ids[:1000]) {
		t.Fatalf("stale cache hid partially persisted hashes: count=%d err=%v", len(got), err)
	}
	s.writeActivityArchive = nil
	if n, err := s.addActivityHashes(ids); err != nil || n != 1500 {
		t.Fatalf("retry did not deduplicate persisted chunks: count=%d err=%v", n, err)
	}
	got, err = s.activityHashes()
	if err != nil || !slices.Equal(got, ids) {
		t.Fatalf("retry history lost ordering or hashes: count=%d err=%v", len(got), err)
	}
}

func TestActivityCachedIndexMergesNewJournalTransactions(t *testing.T) {
	s, _ := guardedFixture(t)
	ids := make([]string, 20)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if _, err := s.addActivityHashes(ids); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Activity(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	q := ethQuote(t, s)
	sent, err := s.Send(context.Background(), q.ID, "fixture-password-123")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		first, err := s.Activity(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if first.TotalTransactions != 21 || first.Pages != 2 || first.Transactions[0].Hash != sent.Hash {
			t.Fatalf("journal addition missing or duplicated: %+v", first)
		}
		last, err := s.Activity(context.Background(), 2)
		if err != nil || len(last.Transactions) != 1 || last.Transactions[0].Hash != ids[0] {
			t.Fatalf("page boundary changed: %+v %v", last, err)
		}
		if _, err := s.addActivityHashes([]string{sent.Hash}); err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkActivityIndexWarm(b *testing.B) {
	for _, count := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("hashes-%d", count), func(b *testing.B) {
			s := &Service{walletDir: b.TempDir()}
			ids := make([]string, count)
			for i := range ids {
				ids[i] = fmt.Sprintf("0x%064x", i+1)
			}
			if _, err := s.addActivityHashes(ids); err != nil {
				b.Fatal(err)
			}
			if _, err := s.loadActivityIndex(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := s.loadActivityIndex(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
