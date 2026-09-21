package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
)

// Activity archives must not be loaded as signed transaction journals.
func TestActivityArchiveAvoidsJournalGlob(t *testing.T) {
	s, _ := guardedFixture(t)
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if n, err := s.addActivityHashes(ids); err != nil || n != 1001 {
		t.Fatalf("addActivityHashes failed: %d %v", n, err)
	}

	// 檢查 activity-archive-*.json 存在
	activityArchives, err := filepath.Glob(filepath.Join(s.walletDir, "activity-archive-*.json"))
	if err != nil || len(activityArchives) == 0 {
		t.Fatalf("expected activity-archive files, got %v (err: %v)", activityArchives, err)
	}

	// Activity filenames stay outside the transaction archive namespace.
	journalArchives, err := filepath.Glob(filepath.Join(s.walletDir, "archive-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range journalArchives {
		base := filepath.Base(p)
		if strings.HasPrefix(base, "activity-archive-") {
			t.Fatalf("journal glob matched activity archive: %s", base)
		}
	}

	// 驗證 journalManager.loadArchives 不會因 activity 封存檔而報錯
	jm, err := NewJournalManager(s.walletDir, 11155111)
	if err != nil {
		t.Fatalf("loadArchives failed: %v", err)
	}
	if len(jm.archived) != 0 {
		t.Fatalf("expected 0 archived journal records, got %d", len(jm.archived))
	}
}

// TestActivityArchiveDualHardBoundaries 驗證封存切分邏輯同時兼顧 1000 筆與 128KB 雙重硬邊界。
func TestActivityArchiveDualHardBoundaries(t *testing.T) {
	s, _ := guardedFixture(t)
	total := 2500
	ids := make([]string, total)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if n, err := s.addActivityHashes(ids); err != nil || n != total {
		t.Fatalf("addActivityHashes: %d %v", n, err)
	}

	// 檢查所有封存檔與活躍檔均符合 <= 1000 筆與 <= 128KB
	archives, err := filepath.Glob(filepath.Join(s.walletDir, "activity-archive-*.json"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("no archives created: %v", err)
	}

	allFiles := append([]string{filepath.Join(s.walletDir, "activity.json")}, archives...)
	for _, f := range allFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > maxActivityIndexBytes {
			t.Fatalf("file %s size %d exceeds %d bytes", f, len(data), maxActivityIndexBytes)
		}
		var stored []string
		if err := json.Unmarshal(data, &stored); err != nil {
			t.Fatalf("file %s invalid JSON: %v", f, err)
		}
		if len(stored) > maxActivityHashes {
			t.Fatalf("file %s has %d hashes, exceeds %d", f, len(stored), maxActivityHashes)
		}
	}

	// 重新啟動後讀取完整歷史無遺漏
	s.Close()
	restarted, err := NewService(s.client, s.walletDir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()

	hashes, err := restarted.activityHashes()
	if err != nil || len(hashes) != total {
		t.Fatalf("expected %d hashes after restart, got %d (err: %v)", total, len(hashes), err)
	}
	for i, h := range hashes {
		if h != ids[i] {
			t.Fatalf("order mismatch at index %d: want %s got %s", i, ids[i], h)
		}
	}
}

// 封存寫入失敗時保留原資料與掃描進度，重試及重啟後仍能讀到完整歷史。
func TestActivityArchivePhaseAFailurePreservesState(t *testing.T) {
	dir := t.TempDir()
	var walletAddr string
	head := &types.Header{Number: big.NewInt(500), Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1)}
	blockHash := "0x" + strings.Repeat("a", 64)
	client := mockRPC(t, func(method string, params json.RawMessage) any {
		switch method {
		case "eth_chainId":
			return "0xaa36a7"
		case "eth_getBlockByNumber":
			var args []any
			_ = json.Unmarshal(params, &args)
			if len(args) > 1 && args[1] == true {
				return map[string]any{
					"hash": head.Hash().Hex(),
					"transactions": []any{
						map[string]any{
							"hash": blockHash,
							"from": walletAddr,
							"to":   walletAddr,
						},
					},
				}
			}
			return head
		case "eth_getBlockReceipts":
			return errors.New("the method eth_getBlockReceipts does not exist/is not available")
		case "eth_getTransactionReceipt":
			return map[string]any{
				"status":            "0x1",
				"transactionHash":   blockHash,
				"blockHash":         head.Hash().Hex(),
				"blockNumber":       "0x1f4",
				"gasUsed":           "0x5208",
				"cumulativeGasUsed": "0x5208",
				"logsBloom":         "0x" + strings.Repeat("00", 256),
				"logs":              []any{},
			}
		}
		return nil
	})

	svc, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if _, err := svc.Create("test-pass-12345"); err != nil {
		t.Fatal(err)
	}
	walletAddr, err = svc.keystore.Address()
	if err != nil {
		t.Fatal(err)
	}

	// 預先建立既有封存檔（200 筆）以驗證讀取既有封存成功
	existingArchiveIDs := make([]string, 200)
	for i := range existingArchiveIDs {
		existingArchiveIDs[i] = fmt.Sprintf("0x%064x", i+1)
	}
	archiveData, err := json.Marshal(existingArchiveIDs)
	if err != nil {
		t.Fatal(err)
	}
	existingArchivePath := filepath.Join(dir, "activity-archive-00000000000000000001.json")
	if err := atomicWriteFile(existingArchivePath, archiveData, 0600); err != nil {
		t.Fatal(err)
	}

	// 門檻只計算活躍檔；既有封存不占這 1,000 筆額度。
	activeIDs := make([]string, maxActivityHashes)
	for i := range activeIDs {
		activeIDs[i] = fmt.Sprintf("0x%064x", 201+i)
	}
	activeData, err := json.Marshal(activeIDs)
	if err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(dir, "activity.json")
	if err := atomicWriteFile(activePath, activeData, 0600); err != nil {
		t.Fatal(err)
	}

	// 讀取當前既有封存與活躍檔內容作為比對基準
	beforeArchive, err := os.ReadFile(existingArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeActive, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}

	// 設定同步進度起始於區塊 500
	start := uint64(500)
	if _, err := svc.ConfigureScan(true, &start); err != nil {
		t.Fatal(err)
	}
	progBefore, err := svc.ScanProgress()
	if err != nil {
		t.Fatal(err)
	}
	if progBefore.Next != 500 {
		t.Fatalf("unexpected initial cursor: %d", progBefore.Next)
	}

	// 等到真正寫新封存時才放入同名目錄，讓 atomicWriteFile 的 rename 失敗。
	// 這不依賴執行身分，且 scan.json 仍可正常寫入。
	var blockedPath string
	var writeErr error
	archiveWrites := 0
	svc.writeActivityArchive = func(path string, data []byte, perm os.FileMode) error {
		archiveWrites++
		blockedPath = path
		if path == existingArchivePath {
			t.Fatal("attempted to overwrite existing archive")
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		writeErr = atomicWriteFile(path, data, perm)
		return writeErr
	}

	scanErr := svc.ScanOnce(context.Background())
	var renameErr *os.LinkError
	if archiveWrites != 1 || !errors.As(writeErr, &renameErr) || renameErr.Op != "rename" || renameErr.New != blockedPath {
		t.Fatalf("expected one archive rename failure: writes=%d path=%q err=%v", archiveWrites, blockedPath, writeErr)
	}
	if !errors.Is(scanErr, writeErr) {
		t.Fatalf("ScanOnce did not preserve archive write error: got %v, want %v", scanErr, writeErr)
	}

	// 驗證 a & b: 游標維持原高度 500，未向前跳躍
	progAfterFailure, err := svc.ScanProgress()
	if err != nil {
		t.Fatal(err)
	}
	if progAfterFailure.Next != 500 {
		t.Fatalf("cursor prematurely skipped unconfirmed transaction: want 500, got %d", progAfterFailure.Next)
	}
	if progAfterFailure.Error != writeErr.Error() {
		t.Fatalf("scan error was not saved: %q", progAfterFailure.Error)
	}

	// 驗證 c: 既有封存檔與活躍檔維持原樣，完全無損
	afterArchive, err := os.ReadFile(existingArchivePath)
	if err != nil || !bytes.Equal(beforeArchive, afterArchive) {
		t.Fatal("existing archive file was modified despite write failure")
	}
	afterActive, err := os.ReadFile(activePath)
	if err != nil || !bytes.Equal(beforeActive, afterActive) {
		t.Fatal("active file was modified despite archive write failure")
	}

	// 移除故障後，舊資料應完整，新交易尚未保存。
	svc.writeActivityArchive = nil
	if err := os.Remove(blockedPath); err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{}, existingArchiveIDs...), activeIDs...)
	stored, err := svc.activityHashes()
	if err != nil || !slices.Equal(stored, want) {
		t.Fatalf("failed write changed stored history: hashes=%d err=%v", len(stored), err)
	}

	// 重啟後重掃同一區塊，確認故障恢復不依賴舊服務的記憶體狀態。
	svc.Close()
	svc, err = NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce recovery failed: %v", err)
	}
	progRecovered, err := svc.ScanProgress()
	if err != nil {
		t.Fatal(err)
	}
	if progRecovered.Next != 501 || progRecovered.Error != "" {
		t.Fatalf("scan did not recover: %+v", progRecovered)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "activity-archive-*.json"))
	if err != nil || len(paths) != 2 {
		t.Fatalf("expected existing and new archive: %v %v", paths, err)
	}
	active, err := readActivityFile(activePath)
	if err != nil || len(active) != activityActiveKeepCount {
		t.Fatalf("active file was not compacted: hashes=%d err=%v", len(active), err)
	}

	// 關閉並重啟服務，驗證資料完整性
	svc.Close()
	reopened, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	allHashes, err := reopened.activityHashes()
	want = append(want, blockHash)
	if err != nil || !slices.Equal(allHashes, want) {
		t.Fatalf("history lost or reordered after restart: got %d hashes, want %d (err: %v)", len(allHashes), len(want), err)
	}
}

// TestActivityArchivePhaseBInterruptedCompaction 驗證階段 b：封存成功但活躍檔更新中斷時，有序去重、倒序分頁不跳頁、CSV 無重複行。
func TestActivityArchivePhaseBInterruptedCompaction(t *testing.T) {
	s, _ := guardedFixture(t)
	total := 1000
	ids := make([]string, total)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	// 寫入 1000 筆至活躍檔
	activeData, _ := json.Marshal(ids)
	activePath := filepath.Join(s.walletDir, "activity.json")
	if err := atomicWriteFile(activePath, activeData, 0600); err != nil {
		t.Fatal(err)
	}

	// 模擬崩潰情境：封存檔已寫入前 800 筆，但活躍檔未及縮減（仍有全部 1000 筆）
	archiveData, _ := json.Marshal(ids[:800])
	archivePath := filepath.Join(s.walletDir, "activity-archive-00000000000000000001.json")
	if err := atomicWriteFile(archivePath, archiveData, 0600); err != nil {
		t.Fatal(err)
	}

	// 驗證 activityHashes 有序去重
	loaded, err := s.activityHashes()
	if err != nil || len(loaded) != total {
		t.Fatalf("expected %d deduplicated hashes, got %d (err: %v)", total, len(loaded), err)
	}
	for i, h := range loaded {
		if h != ids[i] {
			t.Fatalf("order mismatch at %d: want %s got %s", i, ids[i], h)
		}
	}

	// 驗證分頁倒序索引穩定不跳頁
	ctx := context.Background()
	resp1, err := s.Activity(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if resp1.TotalTransactions != total || resp1.Pages != 50 || len(resp1.Transactions) != 20 {
		t.Fatalf("page 1 invalid: %+v", resp1)
	}
	// 第一頁第一筆應為最新交易 (ids[999])
	if resp1.Transactions[0].Hash != ids[999] {
		t.Fatalf("page 1 first tx mismatch: want %s got %s", ids[999], resp1.Transactions[0].Hash)
	}

	// 走訪所有分頁，確認不跳頁且交易總數與順序完全正確
	seenTransactions := map[string]int{}
	for page := 1; page <= resp1.Pages; page += 1 {
		resp, err := s.Activity(ctx, page)
		if err != nil {
			t.Fatalf("page %d failed: %v", page, err)
		}
		for _, tx := range resp.Transactions {
			seenTransactions[tx.Hash] += 1
		}
	}
	if len(seenTransactions) != total {
		t.Fatalf("expected %d unique transactions across pages, got %d", total, len(seenTransactions))
	}
	for h, count := range seenTransactions {
		if count != 1 {
			t.Fatalf("transaction %s appeared %d times across pages", h, count)
		}
	}
}

// TestActivityArchivePhaseCInterruptedCursorAdvances 驗證階段 c：活躍檔縮減成功但 scan 游標更新中斷時，重掃同一區塊回傳 (0, nil) 並順利推進游標。
func TestActivityArchivePhaseCInterruptedCursorAdvances(t *testing.T) {
	head := &types.Header{Number: big.NewInt(500), Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1)}
	blockHash := fmt.Sprintf("0x%064x", 1001)
	var walletAddr string
	client := mockRPC(t, func(method string, params json.RawMessage) any {
		switch method {
		case "eth_chainId":
			return "0xaa36a7"
		case "eth_getBlockByNumber":
			var args []any
			_ = json.Unmarshal(params, &args)
			if len(args) > 1 && args[1] == true {
				return map[string]any{
					"hash": head.Hash().Hex(),
					"transactions": []any{
						map[string]any{
							"hash": blockHash,
							"from": walletAddr,
							"to":   walletAddr,
						},
					},
				}
			}
			return head
		case "eth_getBlockReceipts":
			return errors.New("the method eth_getBlockReceipts does not exist/is not available")
		case "eth_getTransactionReceipt":
			return map[string]any{
				"status":            "0x1",
				"transactionHash":   blockHash,
				"blockHash":         head.Hash().Hex(),
				"blockNumber":       "0x1f4",
				"gasUsed":           "0x5208",
				"cumulativeGasUsed": "0x5208",
				"logsBloom":         "0x" + strings.Repeat("00", 256),
				"logs":              []any{},
			}
		default:
			t.Logf("RPC method: %s %s", method, string(params))
		}
		return nil
	})

	dir := t.TempDir()
	svc, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create("test-pass-12345"); err != nil {
		t.Fatal(err)
	}
	walletAddr, err = svc.keystore.Address()
	if err != nil {
		t.Fatal(err)
	}

	// 先填滿 1000 筆索引
	ids := make([]string, 1000)
	for i := range ids {
		ids[i] = fmt.Sprintf("0x%064x", i+1)
	}
	if n, err := svc.addActivityHashes(ids); err != nil || n != 1000 {
		t.Fatalf("fill: %d %v", n, err)
	}

	// 模擬第 1001 筆由掃描新區塊引發封存
	if n, err := svc.addActivityHashes([]string{blockHash}); err != nil || n != 1 {
		t.Fatalf("add: %d %v", n, err)
	}

	// 模擬崩潰：活躍檔已縮減，但 scan.json 游標未更新（仍停在原區塊）
	// 重掃同一區塊，再次傳入 blockHash
	n, err := svc.addActivityHashes([]string{blockHash})
	if err != nil {
		t.Fatalf("re-scan addActivityHashes failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("re-scan expected 0 new hashes, got %d", n)
	}

	// 設定 scan.json 於區塊 500
	start := uint64(500)
	if _, err := svc.ConfigureScan(true, &start); err != nil {
		t.Fatal(err)
	}

	// 執行 ScanOnce，因已包含該交易，能無害越過並推進游標
	if err := svc.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}
	progress, err := svc.ScanProgress()
	if err != nil {
		t.Fatal(err)
	}
	if progress.Next != 501 {
		t.Fatalf("cursor did not advance, next is %d", progress.Next)
	}
}
