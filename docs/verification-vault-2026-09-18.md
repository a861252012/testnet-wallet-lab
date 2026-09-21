# ETHVault 本機驗證紀錄 — 2026-09-18

本文件保留 9 月 18 日的歷史結果。後續 CI／斷言補強與新來源指紋，見 [2026-09-19 複核及驗證](verification-vault-2026-09-19.md)；下方 569 個 pass 不能當成後續修改已重跑全套的證據。

本輪實際執行時間為 2026-09-18 **21:23–21:30（Asia/Taipei）**，即 **13:23–13:30 UTC**。範圍為當時工作樹的本機驗證；當時尚未驗收這份修改的公開部署與公共 Sepolia 存提。

## 版本與結果摘要

基底 commit：`66ae38bb4721d15b1993cf6343670883061c305b`。驗證包含當時未提交的修改，來源以下方指紋識別，不能只用基底 commit 代表受測版本。

| 階段 | 程式／建置／測試檔案 SHA-256 | 結果 |
|---|---|---|
| 初次執行 | `a76791f28d4f523d445987d5e98dbe165f9009972d097badf0d4aef89e3f348a` | Go、合約、瀏覽器通過；部署 fixture 因 PATH 空白引用錯誤失敗 |
| 修正部署 fixture 後 | `3a75df0852e8f14454e909f47f39c9092c35ac7029be1e529483270974cf1195` | 部署 13 項、workflow lint、錯誤環境拒絕、本機映像 smoke 通過 |

兩次 manifest 的差異僅有 `tests/deployment/test_deploy.py`、`tests/deployment/test_poll.py`；Go、JavaScript、Solidity、UI 及 CI 檔案未變。因此沿用初次成功結果，只針對受影響的部署 fixture 及剩餘項目驗證。各次執行前後來源一致，映像驗證完成後亦重新核對最終指紋。

指紋涵蓋 176 個 tracked／non-ignored 程式、設定、建置與測試檔案：取 `git ls-files --cached --others --exclude-standard`，排除 `docs/`、`README*`、`test-results/` 及 `.md`；逐檔 SHA-256 後，以檔名排序的 JSON（`sort_keys=True`、`separators=(',', ':')`）計算整體 SHA-256。這個值不是 Git tree／commit SHA；文件與證據另以 diff 審閱。

這份文件保留當時的結果摘要與來源指紋；原始本機 log、執行索引與封存檔不隨 repository 保存。重新驗證可使用下方指令與專案 CI，但新結果只適用於重跑時的版本，不能替代當時的執行結果。

## 實測環境

| 工具 | 本輪使用版本 |
|---|---|
| Go | `go1.26.1 darwin/arm64` |
| Node.js / npm | `v24.2.0` / `11.3.0`；CI browser job 使用 Node 22，本輪未在 Node 22 重跑 |
| Playwright | `1.58.2`，本機已安裝 Chromium |
| Solidity | `0.8.37+commit.f401782d.Emscripten.clang`，optimizer 200 runs，EVM Cancun |
| Docker | `29.4.0`，Linux ARM64 engine |

npm 的全域 `allow-scripts` 設定在當時產生非致命警告。兩個 npm 專案均以 lockfile 執行 `npm ci --ignore-scripts`；合約驗收先執行 `check`，沒有先覆寫 artifact。

## 已完成的驗證

| 指令／項目 | 結果與範圍 |
|---|---|
| `npm run check --prefix contracts` | Exit 0，重新編譯並核對提交的 Solidity artifacts |
| `npm run test:e2e --prefix tests/browser` | Exit 0，Chromium → Go → simulated EVM；存入 0.05、取回 0.02、剩餘 0.03 ETH |
| `npm test --prefix tests/browser` | Exit 0，HTTP fixtures；三種語言、亮／暗色、375/768/1024/1440 px、原有多鏈與錯誤流程 |
| `RUN_BROWSER_E2E=1 go test -race -count=1 -json ./...` | Exit 0，**569 個測試／子測試 pass，5 個 opt-in 外部鏈案例 skip，0 個 fail**；browser 案例有執行 |
| `go vet ./...` | Exit 0 |
| `gofmt -l cmd internal contracts tests` | Exit 0、無輸出 |
| `go fix -diff ./...` | Exit 0、無差異 |
| `python3 -m unittest discover -s tests/deployment -p 'test_*.py'` | 初次失敗；引用修正後 **13 項通過**，Exit 0 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 .github/workflows/verify.yml` | Exit 0；檢查 workflow 語法，並非 GitHub Actions 遠端執行 |
| E2E 錯誤環境四例 | 缺 URL、缺合約、非 loopback URL、缺 simulated 標記，均以預期原因 Exit 1，驗收結果為通過 |
| `git diff --check HEAD` | Exit 0 |
| 本機 `Dockerfile.deploy` build + `tests/deployment/smoke.py` | Exit 0，Linux ARM64、隔離網路、一次性測試 volume；驗證 non-root/read-only、登入／CSRF、密碼保護、容器替換保存錢包及舊 session 拒絕 |

HTTP 整合案例 `TestE2EVaultFullLifecycle` 使用 **0.5 / 0.2 / 0.3 ETH**，與瀏覽器案例的金額不同，兩者都在本機 simulated EVM 上執行。瀏覽器送出後，Go runner 另外依各自 hash 讀取 EVM 成功收據、驗證 `Deposited`／`Withdrawn` 的合約／帳戶／金額，並直接核對 `balanceOf=0.03 ETH`。

本機映像：`testnet-wallet-lab:review-20260918-3a75df08`，`APP_VERSION=local-review-3a75df0852e8`。Image ID：`sha256:a81bb355a143329478b1baf337579245daa435c7c4163b8a7bb065cee6d53588`。此為本機 image ID，並非已發布的 GHCR digest。

## 初次失敗與修正

部署／輪詢替身直接把本機 PATH 拼進 shell，遇到含空白的應用程式路徑後，shell 把後半段誤當 export 參數，導致 6 failures、5 errors。兩份 fixture 改用 `shlex.quote` 處理 base、PATH 與替身程式路徑；暫存目錄名稱刻意含空白，以持續覆蓋此情境。修正後同一組 13 項測試全部通過，正式部署腳本未修改。

初次執行失敗，修正後才通過；兩次結果應分開解讀。

## 當時跳過與未執行的項目

以下五項依既有 opt-in 規則跳過，不算公共鏈驗收成功：

```text
internal/chain  TestSepoliaActivityReadOnly
internal/chain  TestAdditionalNetworksReadOnly
internal/wallet TestSepoliaExchangeReadOnly
internal/wallet TestSolanaDevnetSendAcceptance
internal/wallet TestTronShastaReadOnly
```

本輪尚未執行這份修改的遠端 GitHub Actions、Node 22 瀏覽器流程、Linux AMD64 smoke、`govulncheck`、新版公開站 `test:live`、公共 Sepolia ETHVault 部署／原始碼驗證／存入／取回。這些結果不得由本機通過推論。`EXPECTED_VAULT_ADDRESS` 已新增且完成靜態複核，但指定真實地址的 live 驗收仍須等合約啟用後執行。

程式審查發現成功紀錄可能互相替代、金額 substring 比對可能誤判等問題；修正後執行上述測試。靜態審查不計為測試通過次數。
