package wallet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestActivityArchiveReadDoesNotHoldSendLock(t *testing.T) {
	s, _ := guardedFixture(t)
	path := filepath.Join(s.walletDir, "activity-archive-00000000000000000001.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := s.Activity(context.Background(), 1)
		readDone <- err
	}()
	// Leave the FIFO empty while checking sendMu. Open without blocking so an
	// Activity failure before the archive read makes this test fail promptly.
	var writer *os.File
	deadline := time.Now().Add(5 * time.Second)
	for writer == nil {
		fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if err == nil {
			writer = os.NewFile(uintptr(fd), path)
			break
		}
		if err != syscall.ENXIO || time.Now().After(deadline) {
			t.Fatalf("archive reader did not open FIFO: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer writer.Close()
	lockAcquired := s.sendMu.TryLock()
	if lockAcquired {
		s.sendMu.Unlock()
	}
	if _, err := writer.WriteString("[]"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("archive read did not complete")
	}
	if !lockAcquired {
		t.Fatal("archive I/O held the trading lock")
	}
}

func TestActivitySnapshotDuringCompaction(t *testing.T) {
	s := &Service{walletDir: t.TempDir()}
	ids := make([]string, 5000)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if _, err := s.addActivityHashes(ids[:1000]); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		for start := 1000; start < len(ids); start += 100 {
			if _, err := s.addActivityHashes(ids[start : start+100]); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- nil
	}()
	for range 40 {
		got, err := s.activityHashes()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) < 1000 || len(got) > len(ids) || !slices.Equal(got, ids[:len(got)]) {
			t.Fatalf("compaction snapshot lost or duplicated hashes: count=%d", len(got))
		}
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	got, err := s.activityHashes()
	if err != nil || !slices.Equal(got, ids) {
		t.Fatalf("final snapshot: count=%d err=%v", len(got), err)
	}
}
