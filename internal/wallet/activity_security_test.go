package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActivityIndexLimitsPreserveExistingEvidence(t *testing.T) {
	s, _ := guardedFixture(t)
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if n, err := s.addActivityHashes(ids); err != nil || n != 1000 {
		t.Fatalf("fill: %d %v", n, err)
	}
	path := filepath.Join(s.walletDir, "activity.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.addActivityHashes(ids); err != nil || n != 0 {
		t.Fatalf("duplicates: %d %v", n, err)
	}
	for _, batch := range [][]string{{ids[0], "bad-hash"}} {
		if _, err := s.addActivityHashes(batch); err == nil {
			t.Fatal("invalid append accepted")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("rejected append changed evidence")
		}
	}
	// Adding beyond 1000 triggers archiving: older records are preserved in activity-archive-*.json
	if n, err := s.addActivityHashes([]string{fmt.Sprintf("0x%064x", 1001)}); err != nil || n != 1 {
		t.Fatalf("archive append %d %v", n, err)
	}
	archives, err := filepath.Glob(filepath.Join(s.walletDir, "activity-archive-*.json"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("no archive created: %v %v", archives, err)
	}
	activeData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var activeIDs []string
	if err := json.Unmarshal(activeData, &activeIDs); err != nil || len(activeIDs) > 1000 || len(activeData) > 128*1024 {
		t.Fatalf("active file exceeds bounds: len=%d bytes=%d", len(activeIDs), len(activeData))
	}
	s.Close()
	reopened, err := NewService(s.client, s.walletDir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.activityHashes(); err != nil || len(got) != 1001 {
		t.Fatalf("restart: %d %v", len(got), err)
	}
}

func TestActivityIndexRejectsOversizedExistingFilesWithoutRewriting(t *testing.T) {
	s, _ := guardedFixture(t)
	path := filepath.Join(s.walletDir, "activity.json")
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	many, _ := json.Marshal(ids)
	for _, data := range [][]byte{many, []byte(strings.Repeat(" ", 128*1024) + "[]")} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.activityHashes(); err == nil {
			t.Fatal("oversized file accepted")
		}
		if _, err := s.addActivityHashes([]string{ids[0]}); err == nil {
			t.Fatal("oversized file overwritten")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(data, after) {
			t.Fatal("existing evidence was truncated")
		}
	}
}
