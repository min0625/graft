[English](https://github.com/min0625/graft/blob/main/README.md) | **繁體中文**

# graft

> 將外部 git 儲存庫接枝到你的專案中 — 就像 npm，但適用於任何儲存庫。

`graft` 是一個語言無關的 git 儲存庫依賴管理工具。它讓你宣告、鎖定版本，並安裝來自其他 git 儲存庫的依賴 — 無論它們包含 shell 腳本、protobuf 定義、CI 範本或其他任何東西 — 並提供熟悉的套件管理工具體驗。

```bash
$ graft add github.com/your-org/shared-scripts@v1.2.0
✓ installed shared-scripts v1.2.0 (a3f8c21)
✓ added shared-scripts v1.2.0 (a3f8c21)

# 剛 clone 完或在 CI 中：
$ graft apply
✓ installed shared-scripts v1.2.0 (a3f8c21)
```

---

## 為什麼選擇 graft？

| | git submodule | git subtree | Gitman | vdm | **graft** |
|---|---|---|---|---|---|
| 直覺的 CLI | ✗ | ✗ | ✓ | ✓ | ✓ |
| 鎖定檔 | 部分 | ✗ | ✓ | ✗ | ✓ |
| 單一執行檔 | ✓ | ✓ | ✗ (pip) | ✓ | ✓ |
| 不混入外部歷史 | ✓ | ✗ | ✓ | ✓ | ✓ |
| 平行安裝 | ✗ | ✗ | ✗ | ✗ | ✓ |
| 內容雜湊驗證 | ✗ | ✗ | ✗ | ✗ | ✓ |

---

## 系統需求

- `$PATH` 中需有 `git`
- macOS、Linux 或 Windows

## 安裝

### Homebrew（macOS / Linux）

```bash
brew install min0625/tap/graft
```

### 自動安裝腳本

**macOS / Linux**

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh)"
```

自動偵測 OS 與架構（Linux/macOS、x86_64/arm64），安裝到 `~/.local/bin`。可用 `GRAFT_INSTALL_DIR` 覆寫安裝位置，或用 `GRAFT_VERSION=v1.0.0` 釘選版本。

**Windows（PowerShell）**

```powershell
irm https://raw.githubusercontent.com/min0625/graft/main/script/install.ps1 | iex
```

安裝到 `$HOME\.local\bin`。可用 `$env:GRAFT_INSTALL_DIR` 覆寫，或用 `$env:GRAFT_VERSION = 'v1.0.0'` 釘選版本。

### go install

```bash
go install github.com/min0625/graft/cmd/graft@latest
```

### 手動下載

Linux、macOS、Windows（amd64/arm64）的預編譯執行檔在 [GitHub Releases](https://github.com/min0625/graft/releases)。

> graft 仍在 v1 之前的開發階段。Homebrew tap、安裝腳本與預編譯執行檔將隨第一個 tagged release 提供；在那之前請使用 `go install` 安裝。

---

## 快速開始

```bash
# 1. 在你的儲存庫中初始化 graft——引數指定依賴的安裝目錄
graft init vendor

# 2. 新增一個依賴——一步完成解析、鎖定與安裝
graft add github.com/your-org/shared-scripts@v1.2.0

# 3. 剛 clone 完（或在 CI 中）時，從鎖定檔重新安裝所有依賴
graft apply
```

這會建立兩個檔案：

- `graft.toml` — 你的依賴清單（提交此檔案）
- `graft.lock` — 帶有固定 SHA 和內容雜湊的鎖定檔（提交此檔案）

---

## 命令

### `graft init <vendor>`

在當前目錄中初始化 graft。必要引數指定依賴安裝的根目錄——沒有預設值，因此這個選擇永遠是明確的。建立 `graft.toml`；若已存在則會失敗——永遠不會覆寫。

```bash
graft init vendor    # 或：graft init deps、graft init third_party⋯
```

> 提示：在 Go 或 PHP 專案中，`vendor/` 已屬於工具鏈（`go mod vendor`、Composer）——請改用其他名稱，例如 `deps`。

---

### `graft add <repo>[@ref]`

新增依賴，或更新現有依賴。更新 `graft.toml`、重新產生 `graft.lock`，並同步 vendor 目錄。

```bash
graft add github.com/your-org/shared-scripts@v1.2.0    # 鎖定 tag 指向的 commit
graft add github.com/your-org/shared-scripts@main       # 鎖定分支目前的 commit
graft add github.com/your-org/shared-scripts@a3f8c21d   # 鎖定 SHA
graft add github.com/your-org/shared-scripts             # 鎖定最新 tag 指向的 commit
graft add shared-scripts@v1.3.0                          # 以名稱更新現有依賴
```

無論你傳入什麼 ref——tag、分支、SHA，或省略（解析最新的 semver tag）——graft 都會向遠端解析並以 go.mod 的方式記錄：`graft.toml` 得到人類可讀的 `version`（tag，或在沒有 tag 時使用形如 `v0.0.0-20260418091327-a3f8c21d4e8f` 的 pseudo-version），而確切的 commit SHA 與內容雜湊則寫入 `graft.lock`。安裝永遠使用鎖定的 commit，因此之後分支移動或 tag 被重新指向都無法改變安裝結果。想取得新的 commit，再執行一次 `graft add` 即可。

對於已存在於 `graft.toml` 的依賴，可以直接使用其名稱取代完整的 repo URL。若依賴已鎖定在相同的 commit，此命令為無操作。

`graft add` 會以重新同步*整個*鎖定檔與 vendor 樹收尾（等同 `graft lock` + `graft apply`），因此你對 `graft.toml` 中其他依賴的手動編輯也會在同一次執行中被一併處理。

選項：

```
--dest <dir>       在本地放置依賴的位置（預設值：<vendor>/<name>）
--path <dir>       要安裝的遠端儲存庫子目錄（預設值：儲存庫根目錄）
--name <name>      依賴名稱（預設值：repo 的最後一段路徑）
```

帶有選項的範例：

```bash
graft add github.com/your-org/devtools@v2.0.0 \
  --dest tools/shared \
  --name devtools-scripts
```

同一個 repo 可以出現多次——例如取 monorepo 的兩個子目錄——只要每個條目各有自己的 `--name`。條目彼此獨立：各自鎖定版本，之後以名稱逐一更新。

```bash
graft add github.com/your-org/monorepo@v1.4.0 --path packages/proto --name monorepo-proto
graft add github.com/your-org/monorepo@v1.4.0 --path packages/scripts --name monorepo-scripts
graft add monorepo-proto@v1.5.0    # 只更新這個條目；另一個維持原本的鎖定
```

未帶 `--name` 時，graft 以 repo 比對既有條目。若 repo 比對到多個條目，或由 repo 推導的預設名稱已被*不同的* repo 占用，`graft add` 會回報錯誤並給出提示，永遠不會靜默重指向任何條目。

---

### `graft apply`

將 vendor 目錄同步至 `graft.lock` 定義的狀態：補上缺少的依賴、移除多餘的依賴、升級或降級版本不符的依賴。永遠不會修改鎖定檔。

這是在 CI 中使用的命令。

```bash
graft apply
```

如果 `graft.lock` 遺失或與 `graft.toml` 不同步，graft 將以非零代碼退出並告訴你應該執行什麼。

使用 `--link`（或 `GRAFT_LINK_MODE=symlink`）時，dest 會變成指向共用 content store 的目錄 symlink，而非複本——詳見[快取與去重](#快取與去重)。

---

### `graft lock`

從 `graft.toml` 重新同步 `graft.lock`，但不安裝任何東西。

```bash
graft lock
```

當你手動編輯 `graft.toml`（例如把 `version` 改成較新的 tag）並想在執行 `graft apply` 之前更新鎖定檔時很有用。`repo` 與 `version` 都未變更的條目會保留已鎖定的 commit——這些條目不需要網路存取。新條目以及 `repo` 或 `version` 變更的條目會被重新解析並下載（以計算鎖定檔的內容雜湊）；僅變更 `path` 時會重新下載已鎖定的 commit 來重算內容雜湊，不會重新解析版本。不會安裝任何東西到 vendor。

---

### `graft remove <name>`

從 `graft.toml` 和 `graft.lock` 中移除依賴，並刪除其本地檔案。

```bash
graft remove shared-scripts
```

---

### `graft status`

顯示 `graft.toml`、`graft.lock` 與 vendor 目錄的同步狀態。完全唯讀——不修改任何檔案，也不進行網路請求。

```bash
$ graft status
✓ shared-scripts  a3f8c21 (v1.2.0)  ok
✗ proto-defs      b7e1209 (v0.8.1)  modified
```

全部同步時以結束碼 0 退出，否則為 1——很適合在 CI 中防止 vendor 檔案被手動修改。link 模式的 dest 以低成本的連結目標比對驗證；`--deep` 會額外重新雜湊所引用的 content store 條目。

---

### `graft cache`

檢視與管理使用者層級的全域快取。這些命令不會碰專案檔案，也不需要 `graft.toml`。詳見[快取與去重](#快取與去重)。

```bash
graft cache dir      # 輸出快取位置
graft cache verify   # 重新雜湊 store 條目，刪除損壞的（有損壞則 exit 4）
graft cache clean    # 移除未被引用的條目與過期的裸庫（--all：全部刪除）
```

---

## 組態

`graft.toml` 是清單檔案。提交它到你的儲存庫。

```toml
vendor = "vendor"   # 存放依賴的位置（必填；由 `graft init <vendor>` 設定）

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
path    = "proto"          # 選用：只安裝儲存庫的這個子目錄
dest    = "vendor/proto"   # 選用：自訂安裝位置（預設值：<vendor>/<name>）
```

注意事項：

- `repo` 可省略 scheme——像 `github.com/org/repo` 這樣不帶 scheme 的路徑會以 HTTPS 擷取（仿 go.mod 風格）。需要 SSH 時請明確寫出 `git@github.com:org/repo.git`。
- `version` 仿 go.mod 風格：有 tag 時為 git tag，否則為內嵌 commit 的 pseudo-version（`v0.0.0-<timestamp>-<sha12>`）。可以手動把它改成較新的 tag，再執行 `graft lock`。
- 解析後的 commit SHA 與內容雜湊只存在於 `graft.lock`，安裝永遠只依據它們——因此分支移動或 tag 被重新指向都無法默默改變你的依賴。
- 命令可以在任何子目錄執行：graft 會從當前目錄向上尋找最近的 `graft.toml`（不會越過 git 儲存庫根目錄），並以該目錄作為專案根目錄。
- 不支援 Git LFS：若依賴的檔案樹使用 LFS（`.gitattributes` 中有 `filter=lfs`），graft 會以清楚的錯誤訊息失敗，而不是默默 vendor 進 pointer 檔。

### `graft.lock`

由 graft 自動生成。提交它到你的儲存庫。請勿手動編輯。

```toml
# This file is auto-generated by graft. Do not edit manually.
# Run `graft lock` to regenerate.

lock_version = 1

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"
dest    = "vendor/shared-scripts"
commit  = "a3f8c21d4e8f1b2c3d4e5f6a7b8c9d0e1f2a3b4c"
time    = 2026-04-18T09:13:27Z
hash    = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
path    = "proto"
dest    = "vendor/proto"
commit  = "b7e1209fa3c8d2e1f0a9b8c7d6e5f4a3b2c1d0e9"
time    = 2026-02-02T18:40:11Z
hash    = "sha256:a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
```

`time` 是鎖定 commit 的 committer 時間戳（UTC）——純資訊性欄位，方便一眼看出依賴有多舊。

---

## CI 用法

**GitHub Actions**

```yaml
steps:
  - uses: actions/checkout@v4

  - uses: actions/setup-go@v5
    with:
      go-version: stable

  - name: 安裝 graft
    run: go install github.com/min0625/graft/cmd/graft@latest

  - name: 快取 graft 下載
    uses: actions/cache@v4
    with:
      path: ~/.cache/graft
      key: graft-${{ hashFiles('graft.lock') }}

  - name: 套用依賴
    run: graft apply
```

**GitLab CI**

```yaml
before_script:
  - go install github.com/min0625/graft/cmd/graft@latest
  - graft apply
```

---

## .gitignore

將 vendor 目錄加入 `.gitignore`：

```
vendor/
```

或者略過 `.gitignore` 設定，直接將 vendor 依賴提交到版控——方便在無網路環境下保持可重現性。兩種工作流程都受支援：若 `vendor/` 目錄已提交且內容與鎖定檔相符，`graft apply` 不會進行任何操作，而 `graft status` 可在 CI 中抓出對它的手動修改。

---

## 快取與去重

graft 維護一個使用者層級的全域快取（位置：`graft cache dir`；可用 `GRAFT_CACHE_DIR` 覆寫）：

- **裸儲存庫快取** — 擷取是增量的，下載過的 commit 永遠不會重複下載，重新安裝也可離線完成。
- **Content store** — 每個安裝樹只儲存一份，以鎖定檔的內容雜湊為鍵。`graft lock` 接著 `graft apply` 時每個依賴只下載一次；多個專案共用的相同內容，每台機器也只擷取、儲存一份。

預設情況下 vendor 目錄是實體複本（檔案系統支援時使用 copy-on-write reflink）。使用 `graft apply --link`（或 `GRAFT_LINK_MODE=symlink`）時，每個 dest 改為一個指向 store 的目錄 symlink——Windows 上為 junction，不需要管理員權限——任意數量的專案共用同一份磁碟複本。link 模式要求 `vendor/` 必須加入 gitignore，且是每台機器自己的選擇；永遠不會記錄在 `graft.toml` 或 `graft.lock` 中。

```bash
graft cache dir      # 輸出快取位置
graft cache verify   # 重新雜湊 store 條目，刪除損壞的
graft cache clean    # 移除未被引用的條目（--all：全部刪除）
```

快取純粹是效能層——隨時刪除都是安全的。

---

## 並行執行

會修改狀態的命令（`add`、`remove`、`apply`、`lock`）會取得每專案一把的 advisory lock，因此第二個 graft 程序——例如共用工作區的兩個 CI 任務——會等待第一個完成，而不是弄壞 vendor 目錄。這與 cargo、uv 的行為相同。鎖檔位於全域快取中，永遠不會出現在你的儲存庫裡。`graft status` 是唯讀的，永遠不會阻塞。

---

## 與替代方案的比較

**vs git submodule**
Submodules 在每次 clone 後都需要額外的命令（`git submodule update --init --recursive`），狀態管理令人困惑，並且缺乏適當的鎖定檔。graft 只要一個命令：`graft apply`。

**vs git subtree**
Subtree 會將依賴的完整提交歷史合併進你的儲存庫，且沒有清單檔——沒有任何一個檔案能告訴你依賴了什麼、版本是多少。graft 不混入外部歷史，並將每個依賴記錄在 `graft.toml` / `graft.lock` 中。

**vs Gitman**
Gitman 需要 Python 3.10+。graft 是一個單一二進位檔，安裝時無需任何額外套件管理器。兩者都支援鎖定檔，但 graft 增加了內容雜湊驗證和平行安裝。與 Gitman 一樣，graft 不會遞迴解析傳遞依賴 — 你需要明確宣告所有所需的依賴。這讓工具保持簡單且透明。

**vs vdm**
vdm 沒有鎖定檔 — 如果你固定到一個分支，不同天你會得到不同的程式碼。graft 始終記錄確切的 commit SHA 和內容雜湊。與 vdm 一樣，graft 只管理你明確宣告的頂層依賴。

---

## 授權條款

Apache License 2.0 — 詳見 [LICENSE](https://github.com/min0625/graft/blob/main/LICENSE)。
