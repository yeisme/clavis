# 未合并分支清单（2026-11-22 盘点）

> **Status: Superseded / Updated as of 2026-11-25** — 本文件为 2026-11-22 历史分支盘点；权威路线见 [`docs/ROADMAP.md`](../ROADMAP.md)（基线 HEAD `79d325b`）。原列三项冲突分支中：IMK 清理已于 `CLV-P2-4` 直接在 `main` 上删除 `cmd/clavis-imk/` 与 `internal/bridge/imk_ipc.go`（决策 D2），剪贴板热路径已由 Clipboard Jank v4 及 `CLV-P1-4`（`79d325b`）取代，模板库 T5 统一纳入 `CLV-P3-3`。下文保留作历史审计记录。

盘点方法：`git cherry main <branch>` 判 patch 是否已在 main（`-` = 已在，`+` = 真未合并），
再用 `git merge-tree --write-tree` 做无侵入冲突检测。

## 结论

29 个分支未合并进 main，但只有 **5 个**含真正未合并的提交；其余 24 个的提交
内容已在 main（历史 worktree 残留的分支引用），属陈旧引用。

## 唯一可干净合并的（已合）

| 提交 | 分支 | 内容 |
|---|---|---|
| `ac23319d` | fix-workflow-settings-safety | CLI workflow source 绑定对齐 UI（main.go +54 / main_test.go +81） |

2026-11-22 已 `--no-ff` 合入 main，全量测试绿。

## 有冲突、未合并（3 个，需人工决定）

这些都停在 11-17~11-19，距 main 有 3–5 天跨度，期间 uiapp / pkg/core 改动很大。

| 提交 | 日期 | 内容 | 冲突数 |
|---|---|---|---|
| `54457ebc` | 11-17 | 剪贴板激活域效应（uiapp/capsule_view.go + controller.go +90 行） | 4 |
| `a3e02c70` | 11-19 | 模板离线统一库视图 + 严格覆盖层（pkg/core/template/selection.go + view.go 146 行） | 6 |
| `e6521b0b` | 11-19 | 移除原生 IMK 配置与假安装器（config/main_config.go / validator.go） | 2 |

**处理建议**（按我的判断排序）：

1. **`e6521b0b` 应该重做而不是合并**：IMK 路线早已实测否决（见 `ime-conflict-avoidance.md`），
   这个提交清掉配置是正确的方向，但当前 main 的配置结构已经变了，重新写一遍比解冲突快。
2. **`a3e02c70` 需要先确认还需不需要**：模板库 T4/T5 任务后来走过别的路径，
   先看 `T-clavis-tpl-t5` 的现状再决定。
3. **`54457ebc` 优先级最低**：剪贴板激活域效应这 5 天里已被多次改动覆盖，
   先确认它要解决的问题现在还在不在。

## 为什么没有直接合

冲突不是「解一下就完」的量级：3–5 天的跨度里 uiapp 被 P0/P1/P2 三个视觉批次重写过，
pkg/core/template 也换过实现。盲合会引入回归，且没有任何测试能证明合进来的旧逻辑
与现在的语义一致。宁可记录在案，逐个人工判断。
