package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

func TestScannerPersistsCursorOnlyAfterSuccessfulBlock(t *testing.T) {
	head := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1)}
	fail := true
	client := mockRPC(t, func(method string, params json.RawMessage) any {
		switch method {
		case "eth_chainId":
			return "0xaa36a7"
		case "eth_getBlockByNumber":
			var args []any
			json.Unmarshal(params, &args)
			if len(args) > 1 && args[1] == true {
				if fail {
					return errors.New("unavailable")
				}
				return map[string]any{"hash": head.Hash(), "transactions": []any{}}
			}
			return head
		case "eth_getBlockReceipts":
			return []any{}
		}
		return nil
	})
	dir := t.TempDir()
	svc, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create("test-password-123"); err != nil {
		t.Fatal(err)
	}
	start := uint64(100)
	if _, err := svc.ConfigureScan(true, &start); err != nil {
		t.Fatal(err)
	}
	if err := svc.ScanOnce(context.Background()); err == nil {
		t.Fatal("failed RPC was ignored")
	}
	state, _ := svc.ScanProgress()
	if state.Next != 100 || state.Error == "" {
		t.Fatal("failure advanced cursor")
	}
	fail = false
	if err := svc.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ = svc.ScanProgress()
	if state.Next != 101 || state.Error != "" {
		t.Fatal("success did not advance cursor")
	}
	svc.Close()
	restarted, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	state, err = restarted.ScanProgress()
	if err != nil || state.Next != 101 || !state.Enabled {
		t.Fatal("lost persisted cursor")
	}
}

func TestScannerConfigurationIsResponsiveDuringBlockRPC(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	header := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1)}
	client := mockRPC(t, func(method string, params json.RawMessage) any {
		switch method {
		case "eth_chainId":
			return "0xaa36a7"
		case "eth_getBlockByNumber":
			var args []json.RawMessage
			if err := json.Unmarshal(params, &args); err != nil {
				return err
			}
			var full bool
			if len(args) > 1 {
				_ = json.Unmarshal(args[1], &full)
			}
			if full {
				close(entered)
				<-release
				return map[string]any{"hash": header.Hash(), "transactions": []any{}}
			}
			return header
		case "eth_getBlockReceipts":
			return []any{}
		}
		return nil
	})
	svc, err := NewService(client, t.TempDir(), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if _, err := svc.Create("test-password-123"); err != nil {
		t.Fatal(err)
	}
	start := uint64(100)
	if _, err := svc.ConfigureScan(true, &start); err != nil {
		t.Fatal(err)
	}
	scanDone := make(chan error, 1)
	go func() { scanDone <- svc.ScanOnce(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not enter block RPC")
	}
	configDone := make(chan error, 1)
	go func() {
		_, err := svc.ConfigureScan(false, nil)
		configDone <- err
	}()
	select {
	case err := <-configDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("configuration was blocked by the scan RPC")
	}
	unblock()
	select {
	case err := <-scanDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not finish")
	}
	progress, err := svc.ScanProgress()
	if err != nil || progress.Enabled || progress.Next != 100 || progress.Error != "" {
		t.Fatalf("old scan overwrote new configuration: %+v, %v", progress, err)
	}
}

func TestScanStorageBoundaryPreservesNullTokens(t *testing.T) {
	dir := t.TempDir()
	client := mockRPC(t, func(method string, _ json.RawMessage) any {
		if method == "eth_chainId" {
			return "0xaa36a7"
		}
		return nil
	})
	svc, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	path := filepath.Join(dir, "scan.json")
	data := []byte(`{"enabled":false,"start":0,"next":0,"finalized":0,"tokens":null,"updatedAt":"0001-01-01T00:00:00Z"}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := svc.ScanProgress()
	if err != nil || state.Tokens != nil {
		t.Fatalf("null token semantics changed: %+v %v", state, err)
	}
	if _, err := svc.ConfigureScan(false, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(saved, []byte(`"tokens":null`)) {
		t.Fatalf("null tokens changed on round trip: %s", saved)
	}
}

// TestScannerBackoffRetriesAndRecovers 驗證掃描失敗時退避重試、指數遞增、成功後自動恢復，以及設定變更時立即重設。
func TestScannerBackoffRetriesAndRecovers(t *testing.T) {
	dir := t.TempDir()
	client := mockRPC(t, func(method string, _ json.RawMessage) any {
		if method == "eth_chainId" {
			return "0xaa36a7"
		}
		return nil
	})
	svc, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	now := time.Now()
	// 初始狀態無退避
	gen, skip := svc.checkScanBackoff(now)
	if skip {
		t.Fatal("expected no initial backoff")
	}

	// 第一次失敗：退避 4 秒
	testErr := errors.New("rpc failure")
	svc.recordScanError(gen, testErr, now)
	if _, skip := svc.checkScanBackoff(now.Add(2 * time.Second)); !skip {
		t.Fatal("expected skip during backoff period")
	}
	if _, skip := svc.checkScanBackoff(now.Add(5 * time.Second)); skip {
		t.Fatal("expected no skip after backoff elapsed")
	}

	// 第二次連續失敗：退避翻倍至 8 秒
	svc.recordScanError(gen, testErr, now.Add(5*time.Second))
	if _, skip := svc.checkScanBackoff(now.Add(10 * time.Second)); !skip {
		t.Fatal("expected skip during doubled backoff period")
	}

	// 多次連續失敗：最高上限 60 秒
	for i := 0; i < 10; i += 1 {
		svc.recordScanError(gen, testErr, now)
	}
	if svc.scanBackoffDuration > 60*time.Second {
		t.Fatalf("backoff exceeded 60s maximum: %v", svc.scanBackoffDuration)
	}

	// 錯誤排除後回傳 nil：自動恢復，清除退避
	svc.recordScanError(gen, nil, now)
	if _, skip := svc.checkScanBackoff(now); skip || svc.scanBackoffDuration != 0 {
		t.Fatal("expected backoff cleared on success")
	}

	// 再次失敗後，透過 ConfigureScan 應立即重設退避
	svc.recordScanError(gen, testErr, now)
	if _, skip := svc.checkScanBackoff(now.Add(1 * time.Second)); !skip {
		t.Fatal("expected active backoff")
	}
	if _, err := svc.ConfigureScan(true, nil); err != nil {
		t.Fatal(err)
	}
	if _, skip := svc.checkScanBackoff(now.Add(1 * time.Second)); skip {
		t.Fatal("ConfigureScan did not reset backoff")
	}
}

// TestScannerConfigureScanClearsBackoffRace 驗證舊掃描失敗返回時，若期間使用者已透過 ConfigureScan 重設，舊錯誤不應將退避加回。
func TestScannerConfigureScanClearsBackoffRace(t *testing.T) {
	dir := t.TempDir()
	client := mockRPC(t, func(method string, _ json.RawMessage) any {
		if method == "eth_chainId" {
			return "0xaa36a7"
		}
		return nil
	})
	svc, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	now := time.Now()
	gen, skip := svc.checkScanBackoff(now)
	if skip {
		t.Fatal("expected no initial backoff")
	}

	// 模擬掃描執行期間，使用者呼叫 ConfigureScan 重設退避並遞增世代
	start := uint64(200)
	if _, err := svc.ConfigureScan(true, &start); err != nil {
		t.Fatal(err)
	}

	// 模擬舊掃描在 ConfigureScan 之後返回錯誤，嘗試登記退避
	staleErr := errors.New("stale scan failure")
	svc.recordScanError(gen, staleErr, now)

	// 驗證舊世代錯誤被忽略，退避未被加回
	if _, skip := svc.checkScanBackoff(now.Add(1 * time.Second)); skip {
		t.Fatal("stale scan error re-introduced backoff after ConfigureScan")
	}

	// 驗證當前世代之錯誤仍可正確套用退避
	currGen, _ := svc.checkScanBackoff(now)
	svc.recordScanError(currGen, errors.New("current generation error"), now)
	if _, skip := svc.checkScanBackoff(now.Add(1 * time.Second)); !skip {
		t.Fatal("current generation error should apply backoff")
	}
}

// TestScannerBackoffDoesNotBlockHistory 驗證背景排程 RunMaintenance 在掃描 RPC 連續失敗進入退避期間，
// 不會反覆對鏈端發起 ScanFinalizedBlock，且 History 背景對帳依然定期觸發並查詢交易狀態，不受掃描退避阻礙。
func TestScannerBackoffDoesNotBlockHistory(t *testing.T) {
	dir := t.TempDir()
	var scanRPCCalls atomic.Int64
	var historyRPCCalls atomic.Int64
	head := &types.Header{Number: big.NewInt(100), Difficulty: big.NewInt(0), GasLimit: 30000000, BaseFee: big.NewInt(1)}

	client := mockRPC(t, func(method string, params json.RawMessage) any {
		switch method {
		case "eth_chainId":
			return "0xaa36a7"
		case "eth_blockNumber":
			return "0x100"
		case "eth_getBlockByNumber":
			var args []any
			_ = json.Unmarshal(params, &args)
			// 若請求 full transactions (args[1] == true)，代表 ScanFinalizedBlock 掃描區塊主體
			if len(args) > 1 && args[1] == true {
				scanRPCCalls.Add(1)
				return errors.New("rpc node scan unavailable")
			}
			return head
		case "eth_getTransactionReceipt":
			historyRPCCalls.Add(1)
			return nil
		case "eth_getTransactionByHash":
			return nil
		}
		return nil
	})

	svc, err := NewService(client, dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	if _, err := svc.Create("test-password-123"); err != nil {
		t.Fatal(err)
	}

	// 注入待查核交易至日誌，使 History 在背景觸發時向節點發起 RPC 查核
	rec := signedJournalRecord(t, "maintenance-history-check")
	if _, err := svc.journal.AppendAtomic(rec); err != nil {
		t.Fatal(err)
	}

	// 設定測試排程間隔：輪詢間隔 20ms、歷史查詢間隔 50ms、初始退避 80ms
	svc.maintenanceTickInterval = 20 * time.Millisecond
	svc.maintenanceHistoryInterval = 50 * time.Millisecond
	svc.scanBackoffInitial = 80 * time.Millisecond

	start := uint64(100)
	if _, err := svc.ConfigureScan(true, &start); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunMaintenance(ctx)
	}()

	// 運行足夠時間以觀察退避效果與多次歷史查核（達成條件即提早結束，超時上限 1.5 秒防範慢速環境）
	deadline := time.After(1500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			goto finished
		default:
			if historyRPCCalls.Load() >= 3 && scanRPCCalls.Load() >= 2 {
				time.Sleep(60 * time.Millisecond)
				goto finished
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

finished:
	cancel()
	<-done

	scanCalls := scanRPCCalls.Load()
	historyCalls := historyRPCCalls.Load()

	// 斷言 a: 在退避期間，背景排程不會反覆對鏈端發起 ScanFinalizedBlock
	// 在約 350ms 內，若無退避則 20ms ticker 會發起 ~17 次掃描；受 80ms + 160ms 退避限制，請求次數應 <= 3 次
	if scanCalls == 0 {
		t.Fatal("expected at least 1 scan attempt")
	}
	if scanCalls > 3 {
		t.Fatalf("scan RPC was called %d times; expected backoff to restrict requests", scanCalls)
	}

	// 斷言 b: 同時間每 50ms（測試間隔）的 History 依然正常觸發並查詢交易狀態，不受掃描退避阻礙
	if historyCalls < 2 {
		t.Fatalf("History RPC was called only %d times; expected >= 2 independent triggers", historyCalls)
	}
}
