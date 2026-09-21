package wallet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestActivityArchivesTreatWalletDirectoryLiterally(t *testing.T) {
	for _, name := range []string{"wallet", "[wallet]", "wallet*"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			client := mockRPC(t, func(method string, _ json.RawMessage) any {
				if method == "eth_chainId" {
					return "0xaa36a7"
				}
				return nil
			})
			s, err := NewService(client, dir, 2, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			// 萬用字元不能使相鄰錢包的收支紀錄混入目前錢包。
			otherHash := fmt.Sprintf("0x%064x", 999999)
			otherData, _ := json.Marshal([]string{otherHash})
			if err := atomicWriteFile(filepath.Join(root, "wallet-other", "activity-archive-00000000000000000001.json"), otherData, 0600); err != nil {
				t.Fatal(err)
			}

			ids := make([]string, 1802)
			for i := range ids {
				ids[i] = fmt.Sprintf("0x%064x", i+1)
			}
			if n, err := s.addActivityHashes(ids[:1000]); err != nil || n != 1000 {
				t.Fatalf("initial append: %d %v", n, err)
			}
			if n, err := s.addActivityHashes(ids[1000:1001]); err != nil || n != 1 {
				t.Fatalf("archive append: %d %v", n, err)
			}
			if got, err := s.activityHashes(); err != nil || !slices.Equal(got, ids[:1001]) {
				t.Fatalf("history after archiving: got %d hashes, want 1001 in insertion order; err=%v", len(got), err)
			}
			if n, err := s.addActivityHashes(ids[:1]); err != nil || n != 0 {
				t.Fatalf("archived duplicate: %d %v", n, err)
			}

			// 模擬封存時間晚於目前時鐘；重啟後的新檔仍須排在既有封存之後。
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if matched, _ := filepath.Match("activity-archive-*.json", entry.Name()); matched {
					if err := os.Rename(filepath.Join(dir, entry.Name()), filepath.Join(dir, "activity-archive-04000000000000000000.json")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, err := NewService(client, dir, 2, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if got, err := restarted.activityHashes(); err != nil || !slices.Equal(got, ids[:1001]) {
				t.Fatalf("history after restart: got %d hashes, want 1001 in insertion order; err=%v", len(got), err)
			}
			if n, err := restarted.addActivityHashes(ids[1001:]); err != nil || n != len(ids)-1001 {
				t.Fatalf("second archive append: %d %v", n, err)
			}
			if got, err := restarted.activityHashes(); err != nil || !slices.Equal(got, ids) {
				t.Fatalf("history after second archive: got %d hashes, want %d in insertion order; err=%v", len(got), len(ids), err)
			}
		})
	}
}
