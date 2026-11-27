# Clavis CI/CD 与 agent 护栏

日期 2026-11-27 · T-clavis-cicd-design-0 · 待实施

**决策**　macOS arm64 的测试、race 和跨平台编译是日常合并门禁。Windows/Linux 原生运行测试单列，先建立可追踪的失败基线，再把新增回归升为阻断。每次发版以同一提交、同一包的证据为准。普通 PR 的提交、排障和合并交给 agent，护栏变更与发布批准由维护者负责。

## 基线与发布边界

- 调度实测的首个 CI run 发生在 2026-11-25 02:04，随后合计 **9 次 push、9 次失败、0 次成功**。本地 macOS `go test ./...` 全绿，9 次标记 verified 的提交对应的远端 CI 均红。以该记录作为设计基线，实施时从新 run 开始登记。
- `ci.yml` 的 macOS race 覆盖 core、根包、CLI、secrets/storage/sync/daemon，漏掉了修过竞态的 `uiapp` 和 `internal/watcher`。现有 `bash -n scripts/*.sh` 会把展开后的其余文件作为脚本参数，只解析第一份。工作流里的裸 `git diff --check` 也只看工作区，需要改为检查提交范围。 [现有 CI](../../.github/workflows/ci.yml)
- 调度记录显示 hooks 都是 `.sample`、仓库 0 tag、0 PR，三台机器已有认证的 `gh`。使用现有 CLI 和 GitHub Actions，首批无需另建服务。干净 worktree 的 `7646da4` 已通过本机跨平台编译和 vet，可用作命令参考。 [最终门禁记录](../verification/final-gate-2026-11-27.md)
- 1.0 承诺 macOS 自用稳定版。Windows ready、P5 身份中心继续排除在交付范围外。A1–A16 仍待真机签收，真实文本和 Vision provider 也需要独立的运行证据。此前 ad-hoc 包能证明本机安装链路。发版签名口径以维护者明确批准的渠道为准，若仍要求范围文档中的 Developer ID，缺身份就保持发布阻断。 [1.0 范围](scope-1.0.md) · [上线评审](launch-readiness-2026-11-25.md)

## CI 分档

Go 版本由 `go.mod` 统一提供。macOS job 明确使用 arm64 runner，记录实际 `go version`、`GOOS/GOARCH`、runner 镜像和已检出 SHA。PR job 使用临时配置与数据目录，缓存依赖和编译产物，测试用 `-count=1` 禁用结果缓存。下表耗时为初始目标，实施时记实际 p50/p95。

| 档位 | 触发与预算 | 检查 | 决策 |
| --- | --- | --- | --- |
| 本地快速 | 每次提交及推送前，目标 90 秒 | 暂存区检查、任务号、变更文件语法、相关包测试 | 快速拦截，服务端重复检查关键项 |
| PR 必需 | 每次 PR 更新及 `main` push，PR p95 目标 10 分钟 | 下表四项 | 任一缺失、取消、超时、失败均待处理 |
| 兼容性 | 每次 PR 运行现有 Windows/Linux 原生矩阵，夜间从 `main` 复跑 | 记录所有用例结果和首个失败指纹，新增失败追到 PR | 起步为报告，基线确立后新增失败阻断。已知平台失败仍保持红灯 |
| 夜间压力 | 每晚 `main`，目标 30 分钟，按 job 设超时 | macOS `go test -race -count=1 ./...`，对 `./uiapp ./internal/watcher` 再跑 `-race -shuffle=on -count=3` 并保留 seed。Windows/Linux 保留原生测试 | 建关联 issue，定位首坏 SHA。确认 1.0 回归后暂停发版并修复或 revert |
| 发布候选 | 维护者选定已合并 SHA，独立 job | 全量机械门禁、候选打包和安装验证、人工证据 | 仅对该 SHA 和产物摘要放行 |

| GitHub 必需检查名称 | 必须执行的内容 |
| --- | --- |
| `clavis / policy` | 核对任务号及 PR 提交范围、验收项集合和声明的路径范围。PR 用 `git diff --check "$BASE_SHA...$PR_HEAD_SHA"`，push 用事件前后 SHA。对每个受版本管理的 shell 文件分别执行 `bash -n`，对 `.ps1` 用 PowerShell parser 检查语法。校验 workflow YAML、`mygo.json` 标识和四项 macOS usage description。 |
| `clavis / macos-test` | `go test -count=1 ./...`、native `go build ./...` 和 `go vet ./...`。运行隔离目录的 smoke/self-check、sync plan、版本注入断言。 |
| `clavis / macos-race` | `go test -race -count=1 ./uiapp ./internal/watcher ./internal/secrets/... ./internal/storage/... ./internal/sync/... ./internal/daemon/... ./pkg/core/... ./cmd/clavis/... .`。保留完整输出，单列 UI/watcher 时长。 |
| `clavis / cross-build-vet` | `CGO_ENABLED=0`，对 Windows amd64/arm64、Linux amd64 逐目标执行 `go build ./...` 和 `go vet ./...`。保留 Windows bridge/capture 测试二进制编译。任一矩阵项失败则汇总失败。 |

必需检查每个 PR 都发出，初版文档 PR 也跑全套。汇总 job 用 `always()` 收取 `needs.*.result`，明确验证每个预期 job 都成功。工作流级别避免路径过滤。PR 的测试跑合并候选，报告同时记 PR head、base 和 runner 实际检出的 SHA。同一 PR 的新推送取消旧 run，main run 按提交保留结论。检查名跨 workflow 唯一，分支保护在平台支持时绑定预期 App。GitHub 会接受某些 skipped/neutral 状态，因此由汇总 job 保证实际执行条件和结果。 [GitHub 必需检查说明](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks) · [GitHub 分支保护](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)

## 失败定性和跨平台追红

每条失败记录最少带 `task_id / head_sha / base_sha / workflow_run_id / attempt / runner / package / test / exit / fingerprint / owner`，附首个失败日志链接、可复制的最窄复现命令、修复分支或 issue。指纹保留断言类型和关键错误，归一化临时路径、时间戳、端口和随机 ID，保留 shuffle seed。失败日志禁止输出 token、Keychain 内容和用户数据。

| 类别 | 证据及处理 |
| --- | --- |
| 代码回归 | 同 runner、同命令的 base 绿而候选红，或错误指纹新增。owner 先修或 revert，重新推送并等新 SHA。 |
| 既存平台失败 | 同 runner 上已记录的 base 与候选出现同一包、用例和失败指纹，且匹配未过期 issue。原 job 保持失败，单独展示基线命中。未知失败不能落进该类。 |
| flaky | 同 SHA、同 runner 配置、测试条件一致时出现失败和成功。保留两次 attempt 与 seed，单次复跑用于定位，不能抹去必需检查的首次失败。 |
| 基础设施 | runner 启动、工具链下载、服务不可用等，且日志表明测试未执行。允许同 SHA 重跑失败 job 一次，两次同类失败转 `BLOCKED` 并挂 run。 |
| 缺证据或未执行 | 没有 run、job skipped/cancelled、检查跑错 SHA、产物缺失、断言计数不全。先恢复证据。等待超时记为 `ci_pending`，附链接继续由任务监控回收。 |

起步拆为两单并行追踪。Linux 先复核 `pkg/core/actions` 5 项（参数透传、脱敏、信任目录、denylist、dry-run），再查 `internal/storage` 8+ 项（SQLite 重启恢复、跨连接删除原子性、tombstone、替换 sidecar 后的 Close/chmod）。Windows 先修 `TestValidateMCP` 的 Unix 路径 fixture，再逐项处理 TOML 模板合并、词典优先级、digest、library overlay。先执行对应 runner 的完整包测试，保存 `go test -json`，再缩到 `-run '^具体用例$'`。路径、fixture 和真实实现故障分别定性，存储原子性或凭证脱敏若涉及共享代码，同步在 macOS 验证，确认影响 1.0 数据安全即阻断。

基线记录逐用例和平台录入，带首次复现链接、维护者、期限（初始 7 天）和修复条件。禁止按整个 OS、整个包或所有失败做通配，禁止用 `continue-on-error` 让结果看起来成功。两平台各拿到一个可复现 base run 后，启用独立的 `clavis / compat-regression` 汇总为必需检查。该检查只放行已核对的基线命中，对新增失败、未执行或报告缺失置红。每条旧失败修好后从基线移除，清零连续 5 次再将对应原生 suite 升为直接必需检查。实施阶段若仅报告的非必需项出现新失败，agent 暂停普通任务的自动合并，先把证据交给 owner。

`preview_split` 和 watcher 并发隔离列入第一批压力复现，保存 `-race -shuffle=<seed>` 命令与原失败。优先修 goroutine 生命周期、等待条件与共享夹具。禁止以 sleep、去掉 `-race`、串行整个测试套件或多次重跑择绿规避。只有非发布关键用例可由维护者限时隔离，原测试仍在夜间运行。race、权限恢复、数据迁移、Vault 和签名校验不得隔离。

## 分支、PR、本地 hooks

- 一个任务号对应一个短分支 `clv/<task-id>-<slug>` 和一个 worktree。以最新 `origin/main` 开工。第一次可审查的 commit 后立即 `git push -u origin HEAD` 并开 Draft PR。之后每个可复查节点推送，最长每 60 分钟复核远端。禁止将待提交工作交给 `main` 或其他任务的 worktree。
- commit trailer 采用 `Task: T-clavis-…`，PR 标题含同一任务号，squash commit 标题也含任务号。服务端扫描 PR 引入的每个普通提交，自动合并底分支产生的 merge commit 单列处理。PR 描述存原始验收项、允许路径、实现与失败证据、待验项目和风险等级。
- `main` 强制 PR、最新 base 上的必需检查、线性历史和 squash，限制 force push、删默认分支与绕过保护。普通 PR 由任务 agent 在证据齐全后合并。CI/workflow、权限/凭证、发布/数据迁移、验收例外由维护者审批当前 diff。启用期用平台支持的规则保护护栏文件，关键文件 require code owner review 和陈旧审批失效。若账户不支持规则或缺少具备该权限的独立维护者，记录能力缺口并改由维护者合并这些 PR。禁止 agent 自批或用管理员直推。首个试点 PR 产生同名绿检查后再启用 required。 [GitHub 分支保护](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
- 拟由 `make hooks` 安装受版本管理的 `.githooks/`，设置仓库级 `core.hooksPath`。若存在自定义配置，保留并报告冲突，避免覆盖其他工具。三台机器各跑一次 hooks 自检。钩子以小脚本调用同一校验入口，避免本地规则分叉。
  - `pre-commit` 检查暂存区 diff、允许路径、改动 Go 文件 gofmt、已暂存脚本语法。按显式路径 `git add`，预检不改文件。
  - `commit-msg` 检查普通任务提交的 trailer，拒绝缺失、重复或不匹配任务号。
  - `pre-push` 从标准输入读取全部待推 ref，拒绝任务帐号直推 `main` 和发布 tag，检查提交任务号和验收清单版本，运行变更包的快测。推送完成后再由 agent 核对远端。
  - hooks 可以被省略或绕过，因此 `clavis / policy` 要在 CI 对本地检查的关键信息复验。用测试 PR 演练故意漏任务号，以及文档任务额外暂存未允许的路径，两者都应失败。

## Agent 从任务到合并

验收原件由调度侧发布成带版本的任务清单，包含 `task_id`、基准 SHA、`allowed_paths[]`、逐项 `acceptance[]`。任务开始时保存原件的规范化 SHA-256 和序号。解析器对每次 `--accept` 追加值，采用 `action="append"`。新增测试传三次参数并断言顺序和值，缺值、未知项和重复 ID 直接报错。提交验证要和**原件**的 ID 集合、数量与摘要比较，避免用截断后的数组自证完整。原件若更新，需要调度方发新版本，重新绑定证据。

每项验收按稳定 ID 记录 `command / exit_code / evidence / tested_sha / tested_tree / environment / status`，其中环境可区分 fixture、真机、真实服务。测试任务附一次针对产品行为的负对照，例如破坏备份一个字节或禁用 WAL checkpoint 后出现预期失败，随后恢复并复跑。构建或安装任务绑定产物 SHA-256、版本、安装目录和实际运行输出。人工项由维护者记录确认人、设备和结果。证据缺失时状态为 `pending` 或 `blocked`。

普通任务的操作顺序如下。

1. 更新 `origin/main`，确认任务分支、允许路径和暂存内容，跑 `make test-fast` 或对应包命令。按单文件暂存并提交。push 后 fetch 并核对 `git rev-list --left-right --count "HEAD...@{upstream}"` 为 `0 0`，再核对远端 SHA。已生成 commit 未推送时保留 `local_only`，并在 60 分钟巡检中报警。
2. 创建或更新 Draft PR。候选 ready 后采集 `gh pr view "$PR" --json headRefOid,baseRefOid,mergeStateStatus,statusCheckRollup`、`gh pr checks "$PR" --required --watch`。再读取全部 checks 和 run 的事件、SHA、attempt、结论与日志链接。用 `gh run view "$RUN_ID" --log-failed` 追首个失败。有失败、缺预期检查、旧 SHA 或非必需回归就回到修复。状态接口不可达则记录 `BLOCKED`，同时保留已经推送的证据。等待器初始上限 20 分钟，超时记录 pending 并交回巡检跟踪，避免无主。
3. 合并前重新读取 head、base、检查和审批，确认测试证据对应候选。启动 squash 用 `gh pr merge "$PR" --squash --match-head-commit "$HEAD_SHA" --subject "$TASK_SUBJECT" --body-file "$SQUASH_BODY"`，正文最后写同一个 `Task:` trailer。首版不启用后台 auto-merge，避免复核后的 head 更新悄悄替换候选。 [GitHub CLI 检查](https://cli.github.com/manual/gh_pr_checks) · [GitHub CLI 合并](https://cli.github.com/manual/gh_pr_merge)
4. 获取 squash 后的 `mergeCommit.oid`，等待该 SHA 对应的 `main` push CI，记录 run URL 和结果。`merged` 表示进入 main，只有所需检查、任务证据和 post-merge CI 都满足才标 `verified`。main 红灯时锁住发布及后续普通任务合并，优先修复或通过 PR revert。PR 确认合并且证据留存后清理任务分支，其他 worktree 的修改照常保留。

PR job token 只读权限，checkout 禁止保留写凭证。普通 agent 使用受限任务身份，访问分支和 PR，无法改保护设置、写成功状态、推发布 tag 或取得发布凭证。外部 PR 使用 `pull_request` 和隔离的无密钥 runner。发布 job 的凭证及真实 provider 测试与 PR 代码隔离，日志只收安全诊断和分类结果。护栏审批同时覆盖 workflow、被调用的校验脚本、基线例外、任务清单校验器和所有权配置。保护配置未落地前，普通任务可自动跟踪 CI，合并由维护者执行。

巡检汇总保留四项指标。未推送任务数及最老积压时长、已合并但无 CI 成功证据数、首跑失败而复跑成功的用例数、过期兼容基线条目数。每条异常给任务号、最新 SHA、run 或 PR 链接和 owner，避免同一次失败反复发通知。

## 发布自动化

1. **候选构建**　维护者指定一个已走 PR、`main` CI 成功的完整 SHA。只读构建 job 从干净 checkout 跑最终门禁（全量 Go 测试、完整 race、跨平台编译/vet、脚本和 manifest）。由 `scripts/version.sh` 解析版本并注入 Go 和 bundle metadata。正式候选用维护者预先指定的版本（例如 `1.0.0-rc.1`）显式传给 `CLAVIS_VERSION`，记录该值与源码 SHA，最终 tag 必须与之对应。禁止从其他分支、脏树或旧归档转发。
2. **包验证**　在 macOS arm64 runner 调用 `scripts/package.sh`。在临时目录从 DMG 安装，再对该副本核验 arm64、`codesign --verify --deep --strict`、Info.plist、`--version`、`--smoke-check`、`--self-check`。核对编译注入版本、plist 和候选声明完全一致。逐项验证 bundle 文件清单以及 DMG/ZIP/CLI 的 SHA-256，记录签名模式和完整日志。候选 artifact 保留 14 天，构建 job 无发布凭证。ad-hoc 只进入自用候选流程。
3. **真机验收**　维护者用同一包摘要完成 A1–A16，记录未授权启动、重新打开设置并恢复使用、干净目录安装，保护现用 `/Applications/Clavis.app`。结合迁移/WAL/损坏备份检测、真实文本和 Vision provider smoke 的证据，签收候选 SHA 与包摘要。代码或签名后任何字节改变就重新验证。
4. **提升和回退**　维护者授权后创建指向候选 SHA 的不可移动 annotated `vX.Y.Z[-rc.N]` tag，受保护发布流程重复校验 tag、main 祖先关系、artifact SHA、版本和验收记录。优先走私有 CI artifact 或维护者内部分发，不自动创建公开 Release。发布 job 将产物与源 SHA、run ID、签名类型和校验清单一起记录。网络中断后先查远端 tag 和 artifact 摘要再重试；同 tag 不覆盖，问题用新版本修复。保留上一可用包和升级前备份，数据库版本若已升级，先验证恢复副本可读再按迁移规则恢复。
5. **权限落地**　维护者是唯一发版批准者。账户支持受保护 environment 时才把发布凭证放在其中；若当前仓库方案不支持 required reviewer，保持维护者专用手动提升，自动化止于候选与只读验证。无 Developer ID 时可生成 ad-hoc 自用候选，正式 1.0 是否认可该签名由维护者签收记录。 [GitHub environments](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments)

## 七类失效对应的验收

| 已复现的失效 | 实施后的验收动作与预期 |
| --- | --- |
| 未推送积压三次（59 文件、39 提交、25 文件） | 制造一个仅本地的提交或待暂存文件。巡检显示本地领先数或任务路径未提交，关闭任务失败。push 后保存远端 SHA 证据。 |
| commit 缺任务号四次 | 缺 trailer 的 commit 被 hook 拒绝。同一提交通过跳过 hook 上 PR 也要被 `policy` 拒绝。最终 squash 保留任务号。 |
| argparse 静默截断三次 | 传三条 `--accept`，校验三条按顺序保留。删一条验收记录，CI 与原件比对时拒绝。 |
| 普通测试掩盖 uiapp/watcher 的 race | 在试点分支放入可复现竞态，普通测试绿也无法越过独立 race 检查。race 命令从日志可见两个目标包。 |
| 第 6 步 WAL/备份证据不足，第 7 步产物近乎误判 | 让 fixture 备份摘要失配、关闭 WAL checkpoint，或替换候选包，分别让对应验收失败。签名、版本与安装 smoke 针对同一份候选。 |
| 9 次 verified 对应 9 次 CI 红 | 用一次真实红 run 演练。分支可已推送，任务状态不能升为 verified，必须附失败 run。 |
| preview_split、watcher 并发隔离反复 flaky | 保留首失败和重试 run/seed，模拟复跑绿，任务依然进入 flaky 分类并由 owner 修复。 |

## 仓库入口与落地顺序

拟由根 `AGENTS.md` 提供 agent 的第一屏，给出 scope、常用命令、任务号与推送政策、证据状态和停止条件。README 的开发段链接 `docs/CI.md`，后者收录 CI 档位、失败排查和发布手册。拟新增 Makefile 提供 `make help`、`make hooks`、`make test-fast`、`make ci-pr`、`make ci-race`、`make release-verify`，底层由本地与 CI 共用同一命令实现。拟新增 PR 模板、保护路径所有者、平台基线清单。使用新 worktree 验证 agent 从 `AGENTS.md` 出发三分钟内能找到本地命令、失败 run 和发版责任人。

| 顺序 | 责任 | 退出条件 |
| --- | --- | --- |
| 1 | CI owner，先恢复可信反馈 | PR 与 main 的最小必需检查有同名绿 run。独立 race 含 uiapp/watcher，脚本逐个解析。Windows/Linux 原生故障各有带日志的 triage 单。 |
| 2 | 维护者配置保护，agent 实施 hooks 和任务证据 | 用试点 PR 演练直推拒绝、旧 SHA 拒绝、缺任务号拒绝、三条验收不丢、失败 run 不能 verified。三台机器各完成一次 hook/PR 自检。 |
| 3 | 平台 owner 推进兼容基线和压力检查 | 新增兼容失败阻断，现存条目有 owner 和期限。preview_split/watcher 可按种子复现或由修复 PR 关闭。 |
| 4 | 发布维护者验收候选 | 同一 SHA 的机械门禁、产物摘要、安装自检及真机记录齐全，演练错 tag 和错摘要均拒绝提升。 |

本轮交付为本设计文档。上述检查名、hooks、分支规则、验收记录和发布流程均为后续实施项。
