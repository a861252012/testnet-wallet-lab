# 2026-09-21 安全掃描修正

基準：`47ce72b87d7396ef10f4db0b12d483d693d88a86`。本文件記錄本機修改及隔離驗證，未 commit、push、發布、部署或關閉雲端 finding。雲端報告包含歷史提交與中英文重複項目，以下按實際問題合併。

## 處理結果

| 問題 | 結果 | 邊界及限制 |
| --- | --- | --- |
| 匿名訪客占滿 20 個帳戶名額（兩份報告） | `no_change`，維護者接受風險 | 保留公開建立錢包；不改成管理者建立或邀請碼，不刪除現有帳戶。 |
| 無效匿名請求耗盡簽章／備份額度 | 已修正報告的無效請求路徑；合法格式濫用風險由維護者接受 | CSRF、嚴格 JSON、空密碼、空送出識別碼拒絕發生在扣次數之前。有效格式請求仍共用每分鐘 10 次額度，並保留密碼驗證及原本的總流量／併發限制；未宣稱完全防止 DoS。 |
| VM 信任可變 GHCR tag／可偽造 revision label（兩份報告） | 本機修正與替身驗證完成；線上端到端驗收未完成 | CI 在前置檢查和 exact-image smoke 成功後，用短效 GitHub OIDC 憑證簽署 digest。部署器在停服前，驗證 digest、issuer、repository、workflow、main ref、push event 與 commit，任何失敗都保留舊服務。實際 CI 簽章、registry 及 VM 操作尚未執行。 |
| 單一 RPC 可替換智慧帳戶入金地址 | `fixed`，前提為使用獨立確認供應商 | 新增必要的 `--confirm-rpc`；兩邊均須為 Sepolia、回傳正確的非零 ABI 地址且相同，才輸出 sender 或進入簽署。不同 host 不保證不同經營者；兩家串通或共同遭入侵不在這項防護內，也不是本地 CREATE2 推導／共識證明。 |
| 活動索引無上限 | `fixed` | 恢復原有 1,000 筆上限，讀檔最多 128 KiB，新增雜湊也驗證格式。超限或錯誤整批拒絕，不截斷、不覆寫原紀錄。既有超限檔案會明確報錯，需管理者先備份再處理；不會自動清理。 |
| Bundler 回應無大小上限 | `no_change`，基準版本已修正 | 現有 `Client.call` 使用 2 MiB + 1 的有限讀取；RPC envelope、ID、result/error 也有檢查。本次重跑原有邊界測試通過。 |
| Browser fixture 失敗被後續 E2E 成功吞掉 | `no_change`，依目前 workflow 為誤報 | 步驟明確指定 `shell: bash`；GitHub 使用 `bash --noprofile --norc -eo pipefail`。隔離替身讓第一個 npm 命令失敗，步驟確實非零退出，E2E 未執行。 |

總覽曾顯示的 High 1 與發現清單不一致，未取得可追查的對應報告；本次沒有宣稱修復未知的高風險項目。`ai-translator` 的報告不在本專案修正範圍。

## 攻擊路徑與最小修正

### 共用限流

原本 `SharedDemo` 先扣除次數，才進入 `localWalletFilter` 與 JSON decoder。缺少 CSRF 的十次空 POST 雖被拒絕，仍使之後正常備份收到 429。

現在 middleware 只把計數器放入 request context，由 EVM／Solana／TRON 簽署、備份及建立帳戶 handler 在基本請求驗證後呼叫同一個計數器。保留原有全站配額，避免改成可輪換的匿名 ID 而意外放寬暴力猜密碼上限。密碼是否正確仍由各錢包 service 檢查。

涉及 `internal/web/shared.go`、`wallet.go`、`solana.go`、`tron.go`、`keys.go`。測試替換原本僅對空 handler 計數的測試，改以真實 middleware、HTTP handler、暫存錢包與備份執行；涵蓋跨鏈、帳戶／網路前綴、非法 JSON、未知欄位、尾隨 JSON、缺少欄位，以及十次正常操作後的 429。

這只修正無效請求提早扣除額度，仍允許有效格式的匿名請求消耗額度。維護者明確選擇保留訪客密碼操作，接受這個限制；不得把該 finding 整體標成「匿名 DoS 已消除」。

### 部署來源

`poll.sh` 的 tag 現在只定位候選映像。實際部署仍以 `deploy.sh` 為共同驗證邊界，即使直接傳入 digest 也不能繞過。固定 Cosign verifier 位於 `/usr/local/bin/cosign`；TUF 快取位於部署目錄，符合既有 systemd 寫入限制。現有 label、main、新版啟動與回退檢查全部保留。

涉及 `.github/workflows/verify.yml`、`scripts/deploy/deploy.sh`、`poll.sh`、`install.sh`、`tests/deployment/test_deploy.py` 與 `docs/deployment.md`。Cosign installer action 固定 commit，工具固定 v3.1.3；只在 publish job 加入 `id-token: write`。沒有在此次工作中改變 GitHub 設定或實際授予新的外部存取。

舊 VM 的 root-owned 腳本不會被應用映像自動替換。需另外安裝經驗證的 Cosign、新部署腳本，再以已簽章版本驗收；這是未執行的發布工作。簽章測試以程序替身模擬驗證結果，不是實際 Sigstore／GHCR／VM 驗收。

### 入金地址與索引

`cmd/accept-erc4337/main.go` 將未信任的 RPC 地址與獨立 RPC 結果比較，拒絕零地址、非零 ABI padding、長度錯誤、不同鏈、不同地址及無法確認的回應；失敗不輸出任何可入金地址。`docs/erc4337-acceptance.md` 更新必要參數與信任前提。

`internal/wallet/activity.go` 在共用讀／寫邊界限制索引，涵蓋匯入、手動同步、背景 scanner 及活動讀取。超額追加整批失敗，既有檔案保持原樣；scanner 原有「寫入成功才推進游標」行為維持。

## 驗證

Go 檢查在僅含版本控制檔案與本次測試的暫存副本執行。容器使用 `--network none`、唯讀原始碼與唯讀模組快取、`GOPROXY=off`，另建可丟棄編譯快取；沒有掛載 `.env`、本機 wallet、未追蹤鏈上證據或共用資料。

1. 語法、建置與格式：Go 套件編譯、shell `bash -n`、actionlint v1.7.7、`git diff --check`。
2. 漏洞觸發與替代輸入：修正前的暫存副本確實出現無效請求 429、索引超限仍接受、部署忽略驗證器失敗；修正後同一組回歸案例通過。RPC 使用兩個本機 mock，涵蓋單邊替換、錯鏈、零地址、錯誤 padding／長度，失敗回傳零值及錯誤；正常一致地址保持可用。
3. 合法操作與相鄰回歸：正常跨鏈備份、原有錯誤密碼拒絕、原有十次額度、索引去重／重啟、正常部署／失敗回退與現有 UserOperation／receipt 驗證均納入測試。

命令與結果：

```text
go test -race -count=1 ./internal/web ./internal/wallet ./internal/wallet/erc4337 ./cmd/accept-erc4337  PASS
go test -race -count=1 ./...                                                                       PASS
go vet ./...                                                                                     PASS
gofmt -l cmd internal contracts tests                                                              PASS（無輸出）
go fix -diff ./...                                                                                 PASS（無差異）
python3 -m unittest discover -s tests/deployment -p 'test_*.py'                                      PASS（15 個）
actionlint .github/workflows/verify.yml                                                            PASS
bash -n scripts/deploy/deploy.sh scripts/deploy/poll.sh scripts/deploy/install.sh                     PASS
git diff --check                                                                                   PASS
```

另用 GitHub Bash 啟動參數與假的 npm 命令驗證 fixture 失敗停止後續步驟，結果 PASS。未改動瀏覽器資產、Solidity 合約或鏈上流程；本次不執行 public-chain 測試、公開站探測、簽署或送款。Go 的瀏覽器 E2E 在無 Node/Playwright 的隔離容器內略過，Go 模擬 EVM E2E 有執行。

獨立代理因執行環境的模型名稱不受支援而無法啟動，改由主代理執行第二輪唯讀檢查：追蹤所有計數器呼叫者、跨鏈／帳戶路由、索引讀寫者、部署直接入口與回退，以及 RPC 驗證前後的輸出／簽章順序。

## 官方依據

- [Cosign 簽章驗證](https://docs.sigstore.dev/cosign/verifying/verify/) 與 [v3.1.3 CLI 選項](https://github.com/sigstore/cosign/blob/v3.1.3/doc/cosign_verify.md)：使用 exact digest、憑證身分及 GitHub workflow claims。
- [GitHub Actions shell 規則](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idstepsshell)：明確指定 Bash 的失敗中止與 pipefail 行為。
