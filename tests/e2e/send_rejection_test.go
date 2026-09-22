package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2ESendRejectsUnreadableKeystore(t *testing.T) {
	f := newEscrowE2E(t)
	quote := f.quote(0, map[string]string{
		"action": "eth", "to": f.addresses[1].Hex(), "amount": "0.000001",
	}, 200)
	wrongPassword := f.request(0, "POST", "/api/wallet/send", map[string]any{
		"quoteId": quote["id"], "password": "incorrect-fixture-password",
	}, 401)
	if wrongPassword["code"] != "send_rejected" {
		t.Fatal("password rejection lost its classification")
	}

	// Only this test's generated keyfile is replaced to force a read error.
	keyPath := filepath.Join(f.dirs[0], "keystore.json")
	backupPath := keyPath + ".fixture-backup"
	if err := os.Rename(keyPath, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(keyPath, 0700); err != nil {
		t.Fatal(err)
	}
	before := f.sendCount.Load()
	rejected := f.request(0, "POST", "/api/wallet/send", map[string]any{
		"quoteId": quote["id"], "password": escrowPassword,
	}, 500)
	message, ok := rejected["error"].(string)
	if !ok || message != "本機錢包儲存失敗，請檢查資料磁碟與權限" || strings.Contains(message, f.dirs[0]) {
		t.Fatal("storage error must use the public message without a local path")
	}
	if rejected["code"] != "send_rejected" || f.sendCount.Load() != before {
		t.Fatal("read failure must remain a known rejection without broadcasting")
	}

	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backupPath, keyPath); err != nil {
		t.Fatal(err)
	}
	f.send(0, quote)
	if f.sendCount.Load() != before+1 {
		t.Fatal("the same quote must remain usable after the keyfile is restored")
	}
}
