[English](README.md) | **繁體中文**

[![codecov](https://codecov.io/gh/min0625/graft/branch/main/graph/badge.svg)](https://codecov.io/gh/min0625/graft)

# 🌱 Graft

> 將外部 git 儲存庫接枝到你的專案中 — 就像 npm，但適用於任何儲存庫。

`graft` 是一個語言無關的 git 儲存庫依賴管理工具。它讓你宣告、鎖定版本，並安裝來自其他 git 儲存庫的依賴 — 無論它們包含 shell 腳本、protobuf 定義、CI 範本或其他任何東西 — 並提供熟悉的套件管理工具體驗。

```bash
$ graft add github.com/your-org/shared-scripts@v1.2.0
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

### 自動安裝腳本

**macOS / Linux**

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh)"
```

自動偵測 OS 與架構（Linux/macOS、x86_64/arm64），安裝到 `~/.local/bin`。可用 `GRAFT_INSTALL_DIR` 覆寫安裝位置，或用 `GRAFT_VERSION=v0.0.1` 釘選版本（可用 [releases 頁面](https://github.com/min0625/graft/releases)上的任一 tag）。

**Windows（PowerShell）**

```powershell
irm https://raw.githubusercontent.com/min0625/graft/main/script/install.ps1 | iex
```

安裝到 `$HOME\.local\bin`。可用 `$env:GRAFT_INSTALL_DIR` 覆寫，或用 `$env:GRAFT_VERSION = 'v0.0.1'` 釘選版本。

### 手動下載

Linux、macOS、Windows（amd64/arm64）的預編譯執行檔在 [GitHub Releases](https://github.com/min0625/graft/releases)。

### Shell 自動補全

執行一次對應指令並在 shell 設定檔中 source 即可：

```bash
# Bash（~/.bashrc 或 ~/.bash_profile）
source <(graft completion bash)

# Zsh（~/.zshrc，需先啟用 compinit）
source <(graft completion zsh)

# Fish（~/.config/fish/config.fish）
graft completion fish | source

# PowerShell（$PROFILE）
graft completion powershell | Out-String | Invoke-Expression
```

或將腳本持久化以加快啟動速度：

```bash
# Bash
graft completion bash > /etc/bash_completion.d/graft

# Zsh
graft completion zsh > "${fpath[1]}/_graft"
```

---

## 快速開始

```bash
# 1. 在你的儲存庫中初始化 graft——引數指定依賴的安裝目錄
graft init deps

# 2. 新增一個依賴——一步完成解析、鎖定與安裝
graft add github.com/your-org/shared-scripts@v1.2.0

# 3. 剛 clone 完（或在 CI 中）時，從鎖定檔重新安裝所有依賴
graft apply
```

這會建立兩個檔案：

- `graft.toml` — 你的依賴清單（提交此檔案）
- `graft.lock` — 帶有固定 SHA 和內容雜湊的鎖定檔（提交此檔案）

……並把每個依賴安裝在 `<dir>/<name>`：

```
your-project/
├── graft.toml
├── graft.lock
└── deps/                  # 你在 `graft init` 指定的安裝目錄
    ├── shared-scripts/    # 整個 repo root
    └── proto-defs/        # 若有設 `subdir`，只放該子樹的內容
```

---

## 命令

### `graft init [dir]`

在當前目錄中初始化 graft。可選引數設定依賴安裝的根目錄；省略時預設為 `deps`。建立 `graft.toml`；若已存在則會失敗——永遠不會覆寫。

```bash
graft init              # 建立 dir = "deps"
graft init third_party  # 明確指定名稱
```

> 提示：在 Go 或 PHP 專案中，`vendor/` 已屬於工具鏈（`go mod vendor`、Composer）——預設的 `deps` 可避免這個衝突。

---

### `graft add <repo>[@ref]`

新增依賴，或更新現有依賴。更新 `graft.toml`、重新產生 `graft.lock`，並同步 vendor 目錄。

```bash
graft add github.com/your-org/shared-scripts@v1.2.0    # 鎖定 tag 指向的 commit
graft add github.com/your-org/shared-scripts@main       # 鎖定分支目前的 commit
graft add github.com/your-org/shared-scripts@a3f8c21d   # 鎖定 SHA
graft add github.com/your-org/shared-scripts             # 鎖定最新 tag 指向的 commit
graft add github.com/your-org/shared-scripts@v1.3.0     # 以 repo URL 更新現有依賴
```

你傳入的 ref 會如何記錄成 `graft.toml` 的 `version`：

| 你傳入 | `version` 變成 |
|---|---|
| tag（`@v1.2.0`） | tag 名稱，原樣記錄 |
| 分支或 SHA（`@main`、`@a3f8c21d`） | 形如 `v0.0.0-20260418091327-a3f8c21d4e8f` 的 pseudo-version |
| 省略 | 最新的 semver tag（若沒有 tag，則為遠端 `HEAD` 的 pseudo-version） |

無論哪一種，確切的 commit SHA 與內容雜湊都寫入 `graft.lock`，而安裝**永遠只依據它們**——之後分支移動或 tag 被重新指向都無法改變安裝結果。

若依賴已鎖定在相同的 commit，此命令為無操作。`graft add` 會以重新同步*整個*鎖定檔與 vendor 樹收尾（等同 `graft lock` + `graft apply`），因此你對 `graft.toml` 中其他依賴的手動編輯也會在同一次執行中被一併處理。當這次重新同步改動到你指定以外的任何依賴時，`graft add` 會印出 `also synced other dependencies:` 區塊將它們列出，讓連帶變更不會藏在你所要求那個依賴的訊息行背後：

```bash
$ graft add github.com/your-org/shared-scripts@v1.3.0
✓ updated shared-scripts to v1.3.0 (c4d9e02)
also synced other dependencies:
✓ installed proto-defs v0.9.0 (f1a2b3c)   # 你在 graft.toml 手動編輯過的版本
```

選項：

```
--name <name>      依賴名稱與 vendor 下的安裝路徑（例如 tools 或 tool-a/util）
--subdir <dir>     要安裝的遠端儲存庫子目錄（預設值：儲存庫根目錄）
--symlinks <mode>  symlink 策略：reject（預設）或 skip（會在 graft.toml 寫入 symlinks）
```

`--name` 的值同時決定依賴名稱與安裝路徑：`--name tools` 安裝至 `<dir>/tools`，`--name tool-a/util` 安裝至 `<dir>/tool-a/util`。若要改名，先 `graft remove` 再以新的 `--name` 重新 `graft add`。

同一個 repo 可以出現多次——例如取 monorepo 的兩個子目錄——只要每個條目各有自己的 `--name`。

```bash
graft add github.com/your-org/monorepo@v1.4.0 --subdir packages/proto --name monorepo-proto
graft add github.com/your-org/monorepo@v1.4.0 --subdir packages/scripts --name monorepo-scripts
graft add github.com/your-org/monorepo@v1.5.0 --name monorepo-proto    # 只更新這個條目
```

未帶 `--name` 時，graft 以 repo 比對既有條目。若 repo 比對到多個條目，或由 repo 推導的預設名稱已被*不同的* repo 占用，`graft add` 會回報錯誤並給出提示，永遠不會靜默重指向任何條目。

---

### 更新依賴

沒有獨立的 `update` 命令——`graft add` 同時負責新增與更新。要更新現有依賴：

```bash
graft add github.com/your-org/shared-scripts@v1.3.0   # 更新到較新的 tag
graft add github.com/your-org/shared-scripts@main      # 把釘選的分支重新解析到最新 commit
graft add github.com/your-org/shared-scripts           # 更新到最新的 semver tag
```

每次更新都會在 `graft.toml` 顯示為一行 `version` 變更。tag 升級也可以手動改 `graft.toml` 的 `version` 再跑 `graft lock`；pseudo-version 無法手算，那種情況請重跑 `graft add`。當多個條目共用同一個 repo 時，加上 `--name` 指定要更新哪一個。

---

### `graft apply`

將 vendor 目錄同步至 `graft.lock` 定義的狀態：補上缺少的依賴、移除多餘的依賴、升級或降級版本不符的依賴。永遠不會修改鎖定檔。

這是在 CI 中使用的命令。

```bash
graft apply
```

如果 `graft.lock` 遺失或與 `graft.toml` 不同步，graft 將以非零代碼退出並告訴你應該執行什麼。

使用 `GRAFT_LINK_MODE=symlink` 時，dest 會變成指向共用 content store 的目錄 symlink，而非複本——詳見[快取與去重](#快取與去重)。

---

### `graft lock`

從 `graft.toml` 重新同步 `graft.lock`，但不安裝任何東西。

```bash
graft lock
```

當你手動編輯 `graft.toml`（例如把 `version` 改成較新的 tag）並想在執行 `graft apply` 之前更新鎖定檔時很有用。`repo` 與 `version` 都未變更的條目會保留已鎖定的 commit——這些條目不需要網路存取。新條目以及 `repo` 或 `version` 變更的條目會被重新解析並下載（以計算鎖定檔的內容雜湊）；僅變更 `subdir` 或 `symlinks` 時會重新下載已鎖定的 commit 來重算內容雜湊，不會重新解析版本。不會安裝任何東西到 vendor。

#### `--check` — CI 守門

```bash
graft lock --check
```

驗證 `graft.lock` 是否已是 `graft.toml` 的最新解析結果，**不寫任何檔案**。一致時以結束碼 0 退出；不一致時以結束碼 2 退出並列出待更新的依賴名稱。用於在 CI 中擋下「忘了跑 `graft lock` 就提交」的情況。

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

最後一欄的狀態為下列其一：

| 狀態 | 意義 |
|---|---|
| `ok` | 已安裝且與鎖定檔相符 |
| `missing` | 鎖定檔中有，但 vendor 目錄中缺少 |
| `modified` | vendor 內容與鎖定的雜湊不符（例如手動改過檔案） |
| `extra` | vendor 目錄中有，但鎖定檔中沒有 |
| `out of sync` | `graft.toml` 與 `graft.lock` 不一致（執行 `graft lock`） |

全部同步時以結束碼 0 退出；純 vendor 偏移（missing/modified/extra）為 1；`graft.toml` 與 `graft.lock` 不一致時為 2（與 `graft lock --check`、`graft apply` 相同的鎖定檔同步失敗碼；兩者同時發生時取較大值）——很適合在 CI 中防止 vendor 檔案被手動修改。沒有任何依賴時印出 `✓ no dependencies`。link 模式的 dest 以低成本的連結目標比對驗證（store 為不可變；如需重新雜湊 store 條目，請使用 `graft cache verify`）。以另一種模式具現化的 dest——copy 模式下的 symlink、link 模式下的實體樹——回報 `modified`：那正是 `graft apply` 會重寫的偏移。

---

### `graft cache`

檢視與管理使用者層級的全域快取。這些命令不會碰專案檔案，也不需要 `graft.toml`。詳見[快取與去重](#快取與去重)。

```bash
graft cache dir      # 輸出快取位置
graft cache verify   # 重新雜湊 store 條目，刪除損壞的（有損壞則 exit 4）
graft cache prune    # 移除未使用的條目與過期的裸庫（可安全地定期執行）
graft cache clean    # 移除整個快取
```

---

## 組態

`graft.toml` 是清單檔案。提交它到你的儲存庫。

```toml
dir = "deps"        # 存放依賴的位置（由 `graft init` 設定）

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
subdir   = "proto"   # 選用：只安裝儲存庫的這個子目錄
symlinks = "skip"    # 選用："reject"（預設）或 "skip" 略過 symlink 而非以結束碼 2 失敗
```

注意事項：

- `repo` 可省略 scheme——像 `github.com/org/repo` 這樣不帶 scheme 的路徑會以 HTTPS 擷取（仿 go.mod 風格）。也接受明確的 `https://` 或 SSH URL（`git@github.com:org/repo.git`）。由於 graft 呼叫外部 `git`，所有 git 憑證機制——credential helper、`~/.netrc`、SSH agent、`url.insteadOf` 重寫——都自動生效。graft 會關閉 credential helper 自身的互動式提示，因此沒有快取憑證的 HTTPS 儲存庫會直接快速失敗，而不是彈出登入視窗；SSH 遠端仍可能透過 `ssh` 本身跳出提示（例如 host key 確認），graft 並不會抑制這類提示。
- `version` 仿 go.mod 風格：有 tag 時為 git tag，否則為內嵌 commit 的 pseudo-version（`v0.0.0-<timestamp>-<sha12>`）。對 tag 可手動改成較新的 tag，再執行 `graft lock`。Pseudo-version 是衍生值，無法手算，要改請重跑 `graft add`。
- 解析後的 commit SHA 與內容雜湊只存在於 `graft.lock`，安裝永遠只依據它們——因此分支移動或 tag 被重新指向都無法默默改變你的依賴。
- 命令可以在任何子目錄執行：graft 會從當前目錄向上尋找最近的 `graft.toml`（不會越過 git 儲存庫根目錄），並以該目錄作為專案根目錄。若你的 shell 目前 `cd` 在某個需要重新安裝的依賴目錄下，`apply` 會以清楚的錯誤訊息失敗，要求你先 `cd` 出來，而不是丟出作業系統層級的「檔案正被另一個處理程序使用」錯誤。
- 不支援 Git LFS：若依賴的檔案樹使用 LFS（`.gitattributes` 中有 `filter=lfs`），graft 會以清楚的錯誤訊息失敗，而不是默默 vendor 進 pointer 檔。
- Symlink 預設以結束碼 2 拒絕（錯誤訊息會指名該 symlink 的路徑）。若上游 repo 含有無關緊要的 symlink（文件連結、相容性別名），可對該依賴設定 `symlinks = "skip"`——graft 會略過所有 symlink 並印出警告；vendor 目錄仍不含任何 symlink。若要一次加入這類 repo，可用 `graft add --symlinks=skip`（會自動寫入該設定）。
- 不支援依賴內的 git submodule：檔案樹含 submodule（gitlink）條目時以結束碼 2 失敗並指名該條目的路徑——一般簽出會默默漏掉 submodule 的內容。請將該 submodule 的儲存庫另外宣告為一個 graft 依賴。

### `graft.lock`

由 graft 自動生成。提交它到你的儲存庫。請勿手動編輯。

```toml
# This file is auto-generated by graft. Do not edit manually.
# Run `graft lock` to regenerate.

lock_version = 1
dir = "deps"

[[deps]]
name    = "shared-scripts"
repo    = "github.com/your-org/shared-scripts"
version = "v1.2.0"
commit  = "a3f8c21d4e8f1b2c3d4e5f6a7b8c9d0e1f2a3b4c"
time    = 2026-04-18T09:13:27Z
hash    = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

[[deps]]
name    = "proto-defs"
repo    = "github.com/your-org/proto-defs"
version = "v0.8.1"
subdir  = "proto"
commit  = "b7e1209fa3c8d2e1f0a9b8c7d6e5f4a3b2c1d0e9"
time    = 2026-02-02T18:40:11Z
hash    = "sha256:a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3"
```

`time` 是鎖定 commit 的 committer 時間戳（UTC）——純資訊性欄位，方便一眼看出依賴有多舊。

---

## 私有儲存庫

graft 呼叫系統的 `git`，因此認證方式與你機器上的 `git clone` 完全一致——沒有 graft 專屬的 token 或設定。只要 `git clone <repo>` 能成功，`graft add <repo>` 就能成功。

- **SSH：** 使用 SSH URL 搭配既有的 agent/金鑰：`graft add git@github.com:your-org/private-repo.git@v1.2.0`。
- **HTTPS：** 已設定的 credential helper、`~/.netrc`、或 `url.insteadOf` 重寫都會生效。例如要讓所有不帶 scheme 的 `github.com/...` 依賴改走 SSH：

  ```bash
  git config --global url."git@github.com:".insteadOf "https://github.com/"
  ```

- **CI：** 用與 `git clone` 相同的方式提供憑證——deploy key / SSH agent，或在 checkout 步驟放 token（例如 GitHub Actions 的 `actions/checkout` token，或 `git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/"`）。

---

## CI 用法

**GitHub Actions**

```yaml
steps:
  - uses: actions/checkout@v4

  - name: 安裝 graft
    run: /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh)"

  - name: 快取 graft 下載
    uses: actions/cache@v4
    with:
      path: ~/.cache/graft
      key: graft-${{ hashFiles('graft.lock') }}

  - name: 檢查鎖定檔是否為最新
    run: graft lock --check

  - name: 套用依賴
    run: graft apply
```

**GitLab CI**

```yaml
before_script:
  - /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/min0625/graft/main/script/install.sh)"
  - graft lock --check
  - graft apply
```

---

## .gitignore

將安裝目錄（你在 `graft init` 設定的 `dir`，預設為 `deps`）加入 `.gitignore`：

```
deps/
```

或者略過 `.gitignore` 設定，直接將依賴提交到版控——方便在無網路環境下保持可重現性。兩種工作流程都受支援：若安裝目錄已提交且內容與鎖定檔相符，`graft apply` 不會進行任何操作，而 `graft status` 可在 CI 中抓出對它的手動修改。

---

## 快取與去重

graft 維護一個使用者層級的全域快取（位置：`graft cache dir`；可用 `GRAFT_CACHE_DIR` 覆寫）：

- **裸儲存庫快取** — 擷取是增量的，下載過的 commit 永遠不會重複下載，重新安裝也可離線完成。
- **Content store** — 每個安裝樹只儲存一份，以鎖定檔的內容雜湊為鍵。`graft lock` 接著 `graft apply` 時每個依賴只下載一次；多個專案共用的相同內容，每台機器也只擷取、儲存一份。

預設情況下 vendor 目錄是實體複本（檔案系統支援時使用 copy-on-write reflink）。`GRAFT_LINK_MODE` 環境變數選擇 dest 如何具現化——模式名稱（`copy`、`symlink`）對齊 uv——且所有會具現化的命令（`apply`、`add`、`remove`）一視同仁地遵循它。使用 `GRAFT_LINK_MODE=symlink` 時，每個 dest 改為一個指向 store 的目錄 symlink——Windows 上為 junction，不需要管理員權限——任意數量的專案共用同一份磁碟複本。symlink 模式要求安裝目錄必須加入 gitignore，且是每台機器自己的選擇；永遠不會記錄在 `graft.toml` 或 `graft.lock` 中。若只想單次覆寫，為單一命令設定即可（`GRAFT_LINK_MODE=symlink graft apply`）。

```bash
graft cache dir      # 輸出快取位置
graft cache verify   # 重新雜湊 store 條目，刪除損壞的
graft cache prune    # 移除未使用的條目與過期的裸庫（可安全地定期執行）
graft cache clean    # 移除整個快取
```

快取純粹是效能層——隨時刪除都是安全的。

---

## 並行執行

會修改狀態的命令（`add`、`remove`、`apply`、`lock`）會取得每專案一把的 advisory lock，因此第二個 graft 程序——例如共用工作區的兩個 CI 任務——會等待第一個完成，而不是弄壞 vendor 目錄。這與 cargo、uv 的行為相同。鎖檔位於全域快取中，永遠不會出現在你的儲存庫裡。`graft status` 是唯讀的，永遠不會阻塞。

---

## 環境變數

| 變數 | 預設值 | 說明 |
|---|---|---|
| `GRAFT_CACHE_DIR` | 作業系統使用者快取目錄（如 `~/.cache/graft`） | 覆寫全域快取位置。可隨時安全刪除；graft 會視需要重新擷取。 |
| `GRAFT_LINK_MODE` | `copy` | 依賴具現化到 vendor 的方式：`copy`（預設，支援時使用 reflink）或 `symlink`（指向 content store 的目錄 symlink / Windows junction）。這是每台機器的選擇，永遠不會記錄在 `graft.toml` 或 `graft.lock` 中。 |

兩個變數對所有使用到對應功能的命令均有效。若要單次覆寫：`GRAFT_LINK_MODE=symlink graft apply`。

---

## 常見問題

**怎麼更新依賴？** 再跑一次 `graft add`——見[更新依賴](#更新依賴)。沒有獨立的 `update` 命令。

**graft 會解析傳遞依賴嗎？** 不會。graft 只管理你明確宣告的頂層依賴——依賴自己的 `graft.toml`（若有）會被忽略。這讓解析保持簡單透明；你需要的依賴請全部自己宣告。

**上游刪掉或重指 tag 會怎樣？** 已安裝的依賴不受影響——`graft apply` 是依 `graft.lock` 裡的 commit SHA 安裝，而非 tag，因此即使 tag 移動或消失仍能運作。只有當你針對該依賴重跑 `graft add`/`graft lock` 時才會碰到遠端。

**可以 vendor 同一個 monorepo 的多個子目錄嗎？** 可以——把 repo 加入多次，每次給各自的 `--name` 與 `--subdir`。見 [`graft add`](#graft-addreporef)。

**怎麼列出我的依賴 / 確認它們完好？** `graft status` 會印出每個依賴的釘選 commit 與同步狀態（`ok` / `missing` / `modified` / `extra` / `out of sync`），唯讀且離線。可當作 CI 守門，確保安裝目錄沒被手動改過。

**要不要把安裝目錄提交進版控？** 兩種都可以。`.gitignore` 掉走一般套件管理流程（`graft apply` 會重建它），或提交它以支援離線/可重現建置——見 [.gitignore](#gitignore)。

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

## 設計文件

完整的設計與行為規範請見 [`docs/design.zh-TW.md`](docs/design.zh-TW.md)（權威版）與其英文翻譯 [`docs/design.md`](docs/design.md)——檔案格式、命令語義、結束碼、架構、安全考慮與測試策略。

---

## 授權條款

Apache License 2.0 — 詳見 [LICENSE](LICENSE)。
