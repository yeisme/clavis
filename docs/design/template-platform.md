# 设计：模板平台（模板管理与使用）

Status: `inferred` 设计决策 + `observed` 事实基线。基线 HEAD `21ac2db`。所有远端事实来自 s101 只读检查，未调用 provider、未复制正文到本仓。

## 0. 结论

**不造模板市场，做 promptrepo 目录的消费端 + 本地覆盖层。**

三句话说明为什么：

1. 发布侧已经存在且比 Clavis 能做到的更完整：`yeisme-agent/shared/promptrepo`（Go SDK，v0.3.0）+ `backend-server/template-registry`（HTTP/CLI/CAS/合同/recipe/RBAC）+ `data/yeisme-prompt-templates`（88 个 solution 的权威 catalog）。Clavis 再造一套 = 两套 digest 语义、两套合同、必然漂移。
2. Clavis 的位置是**键盘入口**，不是编排器：模板在这里的价值是「一键填槽上屏」和「喂给当前 AI profile」，不是跑多步 agent 工作流（那是 herdr 的活）。
3. 唯一值得自建的是**覆盖层**：远端模板正文只读，本地只改触发词、应用绑定、默认值。这样上游升级无需三方合并。

范围红线（写进验收，防止蔓延）：不做模板市场、不做发布、不做远端执行、不做跨 owner 数据库读取、不做通用 agent 编排。

## 1. 事实基线

### 1.1 yeisme-agent 侧（`observed`）

| 组件 | 位置 | 关键事实 |
| --- | --- | --- |
| SDK | `shared/promptrepo`（`github.com/yeisme/promptrepo`, go 1.24.0） | `ParseTemplateAddress` / `FormatTemplateAddress`；`Inspect` / `Validate` / `Render` / `Preview` / `ContractResolver`；`Preview` 只做本地内存渲染，`provider_calls=false`、`state_writes=false` |
| 服务 | `backend-server/template-registry` | GORM+SQLite、SHA-256 CAS、S3/Postgres 可切换、`ui-template install` 有 staging + single-writer promotion + last-known-good |
| 目录 | `data/yeisme-prompt-templates/catalog.json` | `promptrepo.catalog.v0.1`，88 solutions / 113 discovery 条目 / 928 KB 正文（95 个 `main` role + 领域 role） |
| 合同 | 同仓 `solutions/*/*/contracts/*.json` | `promptrepo.template-contract.v0.1`：inputs（type/min/max/min_length/max_length/regex/enum）、license、permissions、template_digest |
| recipe | 同仓 `recipes/*.json` | `promptrepo.recipe.v0.1`：steps[DAG] + bindings{kind:step_output} + depends_on + output_requirement |
| 需求真源 | `openspec/specs/prompt-template-*`（8 份） | 见 §4 的 fail-closed 规则，逐条对应 |
| 角色模板 | `agent/personal-assistant/scripts/herdr-templates/*.md`（5 个） | YAML front-matter（`description/command/ready/submit/stop`）+ persona 正文，同一「frontmatter + markdown」形状 |
| 本地库 | `.template-registry/`（`objects/`、`operations.jsonl`、`sessions.db`） | 内容寻址对象 + 追加式操作日志 |

目录统计（实测，决定默认导入范围）：

| 维度 | 分布 |
| --- | --- |
| category | image 35、video 17、writing 9、3d 5、engineering 4、agent 3、design 3、audio 2、general 2、office 2、research 2、其余各 1 |
| rights | internal 82、MIT 3、free-evaluation 1、external-attributed 1、unknown 1 |
| maturity | exploratory 84、beta 3、first-support 1 |
| locale | en 87、zh-CN 77（中文为人工审阅层，不进编译 digest） |
| input type | string 707、enum 232、integer 8（另定义 boolean/number） |
| media（`discovery[].media`，solution 级聚合） | text 46 条次、image 39、video 12、3d 5、audio 5、code 2、spoken_dialogue 1、multimodal 1；**纯 `media=[text]` 的 solution 只有 21 条** |

**默认导入判据（2026-11-19 用真实目录修正，原方案作废）**

原先写「默认只导入文本类 + `maturity≥beta`」，实测该组合只剩 **1 条**（且是唯一违反 locale 规范的 `graph/longform.generic.v2`：只有 zh-CN、5 个领域 role、无 `main`）——等于不建库。修正为：

1. **过滤门用 `media`**，不用 `maturity`：只默认导入 solution 级 `media=[text]` 的 **21 条**；含 image/video/3d/audio 的（67 条）默认不导入，交给 eikona/scaena。
2. **`maturity` 只做 UI 徽章**（exploratory 84 / beta 3 / first-support 1），不做准入。84/88 是 exploratory，拿它当门等于全排除。
3. **`rights` 全是 `internal`** 说明这是自用资产库——「实验性」在这里不等于「不能用」；外发时按 §5 的 rights 表处理。
4. **`discovery[]` 是搜索索引**（88 个 solution 按 role×locale 展开成 113 条），字段只有 `{package_id,solution_id,role,locale,title,summary,media}`，**不含 path/digest**。搜索走 discovery、导入走 solutions，不要把 discovery 当列表渲染（否则列表会出现 113 条重复项）。

### 1.2 Clavis 侧（`observed`）

| 能力 | 证据 | 缺口 |
| --- | --- | --- |
| 模板模型 | `pkg/core/config/prompts_config.go:38-49` `PromptTemplate{ID,Trigger,Title,Category,Description,Template,OptimizerPreset,InjectGlossary,AppBundleIDs,Slots}` | 无 `ref/version/digest/locale/role/rights/permissions/contract` |
| 占位符 | `pkg/core/prompt/parser.go` 单遍正则 `{{name}}`、`{{name=默认}}`、`{{name:a\|b}}`；内置 `selection/clipboard/app/url/window_title/date/time/datetime/glossary/cursor` | 无「已声明输入」概念，无法 fail-closed |
| 槽位 | `PromptSlot.Kind ∈ {text,select}`（`prompts_config.go:234`） | 无 number / boolean / regex / min-max 校验 |
| 存储 | `prompts.toml`（全字段）+ `prompts/*.md`（front-matter 仅 `id/trigger/title/category/description/optimizer_preset/inject_glossary`，md 覆盖同 ID 的 TOML 项，`store.go:563`） | md 无法承载 slots/ref/digest；无 CAS、无 provenance |
| 远端适配 | `pkg/core/prompt/adapter.go` `AdaptThirdParty` 只吃 `templateRef/version/digest/slots` 元数据，**不接受正文** | 无法导入正文与合同 |
| 使用 | Actions tab 前缀源 `/` → 模板列表（`pkg/core/engine/capsule.go:1788`）→ 填槽 composer（`uiapp/prompt_composer.go:46`）→ 提交上屏或复制 | 无搜索（标题/摘要/标签/别名）、无多语言、无权限门 |
| 网络 | 除 AI provider 外无出网代码 | 无 catalog 刷新通道 |

### 1.3 差集（本设计要补的，只有这些）

1. 目录快照读取 + digest 校验 + CAS。
2. 合同 → 槽位的严格映射与校验器。
3. 渲染的 fail-closed 语义（未声明占位符、缺失必填、类型错）。
4. 本地覆盖层（触发/绑定/默认值）与上游升级。
5. 管理界面 + 诊断。
6. recipe 的最小落地（≤3 步、逐步确认）。

## 2. 对象模型

三层，正文与覆盖分离：

```text
catalog snapshot（不可变，digest 钉住）
  └─ imported template（ref + version + digest + 正文 + contract，只读）
       └─ local overlay（trigger / title / category / app_bundle_ids / slot defaults，可写）
            └─ rendered instance（一次性，不落盘、不记日志）
```

分层规则（硬）：

- **正文永不被本地改**。要改正文 = 复制成新 ID（`local:<original-id>`）并断链 provenance，UI 明示「已本地化，不再接收上游更新」。
- **覆盖层只允许改 6 个字段**：`trigger`、`title`、`category`、`app_bundle_ids`、`slot.default`、`enabled`。其余字段只读。
- **升级不需要三方合并**：只比较 `digest`。变了就换正文 + 保留覆盖层；合同变了就重算槽位（覆盖层里已删除的槽位默认值丢弃并记诊断）。

磁盘布局（`os.UserConfigDir()/clavis/templates/`）：

```text
templates/
├── catalog.snapshot.json          # 目录原文 + 本地复算的 catalog digest
├── objects/sha256/<hex>           # CAS：模板正文、合同 sidecar，按 digest 寻址
├── imports/<solution>.<role>.<locale>.json   # ref/version/digest/contractDigest/importedAt/sourceKind
└── overlays/<solution>.<role>.<locale>.toml  # 仅上述 6 个字段
```

`sourceKind ∈ {bundled, file, git, registry}`。现有 `prompts.toml` / `prompts/*.md` 保持不动，视为 `sourceKind=local`；合并视图按渲染期统一，冲突时本地优先并告警（不静默覆盖用户手写的东西）。

## 3. 合同 → 槽位映射

| contract input | Clavis slot | 校验器 |
| --- | --- | --- |
| `string` | `kind=text` | `min_length/max_length/regex` |
| `string` + `enum` | `kind=select`，options=enum | 值必须在 enum 内 |
| `integer` | `kind=number`（新增，整数） | `min/max`，且拒绝小数 |
| `number` | `kind=number` | `min/max` |
| `boolean` | `kind=select`，options=`["true","false"]` | 不新增控件，省 UI 成本 |

判定规则（全部 fail-closed，直接对应 `prompt-template-authoring-quality-gate`）：

1. 占位符未在合同声明 → 拒绝渲染，诊断 `undeclared_placeholder:<name>`。
2. 必填未给 → 拒绝渲染，`missing_required:<name>`。
3. 类型/约束不符 → 拒绝渲染，`input_invalid:<name>:<code>`。
4. 可选、无默认、未给 → 渲染空串，状态 `missing`（不得伪造值，也不得对空值应用 regex）。
5. 合同缺失（老 catalog / 本地手写）→ 允许导入但打 `degraded:no_contract`，槽位从正文推断，UI 显示「无合同，仅尽力校验」。
6. 正文同时出现内置占位符与同名合同输入 → 合同优先，内置让位，记诊断。
7. `locale` 只搜 `en` 编译正文；`zh-CN` 标题/摘要用于显示与搜索，但**不渲染、不进 digest**（对齐 locale-unification spec）。

新增 `kind=number` 会碰到 `prompts_config.go:234` 的校验白名单与设置页编辑器，属于共享层改动（两平台各跑一次验证，P11）。

## 4. 信任与校验

- **只做 digest，不做签名**。promptrepo 的模型是「快照钉住 + SHA-256 校验」，没有内容签名链。本设计不夸大：能防的是传输损坏与目录漂移，防不了恶意上游。
- 校验点：catalog 整体 digest、`solution.digest`、`template.digest`、`contract.template_digest`、`contract.digest`（逐层复算 `sha256:` 前缀 + 64 hex 小写）。
- 任一校验失败 = 该项不可用（列表显示红色条纹 + 原因），不降级成「照样能插入」。
- 安装/更新走 staging → 复算 → rename：先写 `templates/.staging/<id>/`，全部通过后原子 rename 到最终目录，失败保留 last-known-good。这是 `template-registry ui-template install` 的同一语义，复刻而不是另发明。
- 目录刷新失败保留上一份快照，并在设置页标注「快照时间 / 已过期 N 天」。

### 4.1 四级 digest 的实际算法（2026-11-19 已验证可复刻）

Clavis 不引入 `github.com/yeisme/promptrepo` 依赖（未公开分发，且会破坏纯 stdlib 纪律），因此必须自己实现。以下算法已用**真实目录 + SDK 双向对照**验证：SDK 算出的值与文件自声明值一致，且不依赖 SDK 的手写序列化也能复现同一结果。

统一形态：**清字段 → 排序 → Go `encoding/json.Marshal`（无缩进、HTML 转义 `<` `>` `&`）→ sha256 → `sha256:<64hex 小写>`**。

| 级别 | 清空/排序规则 | 字段顺序 | 验证 |
| --- | --- | --- | --- |
| catalog | `Digest=""`；`Discovery=nil`（omitempty 故整字段省略）；`GeneratedAt` 零值序列化为 `"0001-01-01T00:00:00Z"`；`Solutions` 按 `package_id + "/" + id + "@" + version` 升序 | `schema_version, repository, generated_at, digest, solutions` | 真实 catalog 复算 = 自声明 `sha256:dc15b9e5…` ✓ |
| solution | `Digest=""`；`RepositoryID=""`；`tags/capabilities/templates` 为 omitempty | `package_id, id, version, digest, category, tags, capabilities, rights, maturity, locales, templates` | 抽样 12/12 匹配 ✓ |
| template | 无规范化，直接 sha256(正文原始字节) | — | `main.en.md` 3979 字节复算匹配 ✓ |
| contract | `Digest=""`；`Permissions` 排序后写入；`Inputs` 按 `Name` 排序；`permissions/inputs` 均 omitempty | `schema_version, package_id, solution_id, version, role, locale, template_path, template_digest, digest, license, permissions, inputs`；input 内部字段 `name, type, required, default, examples, enum, regex, min, max, min_length, max_length, labels, descriptions, sensitivity` | 4/4 合同样本匹配 ✓ |

**绑定关系（必须校验）**：`contract.template_digest` == `catalog` 中该 `role+locale` 的 `template.digest` == `sha256(正文原始字节)`；已实测一致。注意 `solution.digest` 是**独立的一层**，既不等于 `template.digest` 也不等于 `contract.digest`，三者不可混用。

踩坑记录：Go 的 `json.Marshal` 按 **struct 字段声明序**输出，不是字母序——用「排序后再序列化」的直觉去做会算出完全不同的 digest（这也是我第一次尝试三种规范化全部失败的原因）。

## 5. 权限与隐私

| 字段 | 值 | Clavis 行为 |
| --- | --- | --- |
| `permissions` 含 `preview` | 允许本地渲染预览 | 正常 |
| `permissions` 含 `execute_requires_review` | 执行必须人工确认 | 禁止自动送进 AI/自动化；每次调用弹一次「发送给 <profile>」确认，且该确认不记住 |
| `permissions` 缺失该值 | 对执行类能力 fail-closed（scaena 侧同样要求保留该策略） | 只允许复制/上屏，不允许发送 |
| `rights=internal` | 内部资产（82/88） | 允许上屏与 AI 调用；导出到不是自己机器的目标（复制到剪贴板以外的文件）时记一条审计 |
| `rights=MIT / external-attributed` | 可外发 | 上屏/AI 正常，保留归属行（若正文含 `attribution` 字段则一并插入） |
| `rights=unknown` | 未知 | 与 internal 同权限 + 显示「权利未确认」 |

**渲染正文是一等敏感物**（`prompt-template-safe-preview`）：不得写入 stdout/stderr/JSON/审计/诊断/快照/fixture，只存在于进程内返回值与用户显式触发的上屏/复制。审计事件只记 `ref + version + digest + role + locale + profile + provider + model + 耗时 + token 数 + 结果类别`。

## 6. 使用面

一个渲染内核，四条出口：

| 出口 | 形态 | 说明 |
| --- | --- | --- |
| 上屏 | Actions tab `/` 前缀 → 填槽 → 提交到前台应用 | 已有链路，换数据源 + 加搜索 |
| 触发词 | `;` 片段式直接触发（`/cr`） | 导入时自动建议 trigger，冲突检测（同前缀已占则退化为 `//<slug>`） |
| AI | 渲染结果作为 user message 送入当前 profile，流式 + 多轮 | 复用 `pkg/core/ai`，注入 glossary/selection 走同一 context policy |
| recipe | 串行 2–3 步，每步一次确认 | 只支持 `depends_on` 线性链；`bindings.kind=step_output` 允许把上一步结果喂下一步 |

**recipe 的边界（明确不做）**：不做并行分支、不做条件跳转、不做循环、不自动写文件。超过 3 步或含 `output_requirement` 要求改代码的，直接给 herdr 派发建议，不在 Clavis 内执行。理由：Clavis 无沙箱、无审批队列，编排能力放这里只会变成无审计的自动化。

搜索：`title/summary/aliases/tags/capability` × 多 locale 命中，排序 = 当前应用绑定 > 最近使用 > 名称匹配 > 其余。复用现有 frecency 表（`state.FrecencyScores`），不新建权重体系。

## 7. 管理面

设置中心新增一页「模板库」（现 5 tab：General/AI/IMEKeymap/Permissions/Configs，作为第 6 tab 或 IMEKeymap 下的子页，取前者）：

```text
[来源]  内置目录（bundled v?) · 本地目录 · git/file 路径        [刷新] [查看快照时间]
[筛选]  分类 ▾  标签 ▾  权限 ▾  语言 ▾  状态 ▾（可更新/校验失败/已本地化）
[列表]  标题 · 版本 · rights 徽章 · maturity 徽章 · 槽位数 · 状态
[详情]  合同表单（只读回显）· ref · digest 前 12 位 · 权限 · [导入] [更新] [移除] [复制 ref]
[诊断]  每项一行失败原因（code + 人类句子），可复制
```

更新流：刷新目录 → diff（新增 / 版本变 / 撤销 / 权限收紧）→ 默认只勾选「权限未收紧」的项 → 应用（staging→rename）→ 逐项报告。

MERGE 规则：上游撤销（消失）的模板不自动删除，标 `orphaned`，保留 30 天再提示清理。

## 8. 与 herdr / 多设备的结合

herdr 不是模板运行时，是**发布与派发通道**：

| 环节 | 谁做 | 怎么做 |
| --- | --- | --- |
| 目录生成 | template-registry `catalog build` | 在 s101 构建，产物 `catalog.json` 进 `data/yeisme-prompt-templates`（git 真源） |
| 目录分发 | 人工/定时 | Clavis 读 `file://` 本地克隆或内置 bundle；不需要常驻 HTTP（promptrepo 自身也不要求） |
| 任务派发 | herdr lane | 后端 pi / 文档 codex；验收用 `git_sha` + `cmd` 机器断言 |
| 角色模板 | herdr `*-templates/*.md` | 同 front-matter 形状，作为 `sourceKind=agent_role` 一并导入，供「把这段 prompt 派给某角色」使用 |
| 多设备 | sync 的 `templates` 数据类 | 只同步 imports + overlays（小、无隐私），不同步正文（正文按 digest 从目录取，天然一致） |

多设备的关键取舍：**pin 可能不同**。设备 A 停在 v1.0.0、设备 B 升到 v1.1.0 时，不自动覆盖，只在管理页显示「设备差异：B 已更新 3 项」，由用户决定。这是 sync 的 `PersonalBaseline`（共享基线）+ `DeviceOverlay`（设备覆盖）模型的直接套用，无需新语义。

## 9. 批次与验收

| 批 | 内容 | 估时 | 机器验收 |
| --- | --- | --- | --- |
| T1 | catalog 读取 + 逐层 digest 复核 + CAS 写盘（纯 stdlib，无新依赖） | 6h | fixture 目录（离线，禁联网）跑 `go test ./pkg/core/template/...`；坏 digest → 必须报错且不落盘 |
| T2 | 合并视图（local + imported）+ 冲突告警 + 诊断模型 | 5h | 同 ID 本地覆盖时清单只有一条且 `source=local`；孤儿项标 `orphaned` |
| T3 | 合同 → 槽位映射 + `kind=number` + 校验器（min/max/regex/enum/length） | 8h | 7 条 fail-closed 规则逐条单测；232 个 enum 输入的类型映射快照测试 |
| T4 | 渲染 fail-closed + composer 复用 + 权限门 | 6h | 未声明占位符必拒；`execute_requires_review` 未确认时不得调用 AI（用 fake provider 断言 0 次调用） |
| T5 | 设置页「模板库」+ 导入/更新/移除 + 诊断面板 | 12h | `ui.NewTester` 帧断言：列表/筛选/更新对话框可渲染；更新后 digest 变化可见 |
| T6 | recipe 线性执行（≤3 步、逐步确认）+ 审计（只记元数据） | 10h | 3 步链用 fake provider 跑通；第 4 步必须被拒；审计 JSON 不含正文（正则断言无模板片段） |
| T7 | CLI（`clavis template list/import/validate --json`）+ 文档 + 体积预算 | 6h | JSON envelope 形状与 template-registry 的 `ok/command/error` 对齐；bundle 体积 ≤2 MB |

合计约 53h。T1–T4 是纯 Go 可离线可单测，T5 之后要真机 UI，T6 是唯一有执行风险的批次（建议单独 gate）。

## 10. 风险与回退

| 风险 | 缓解 / 回退 |
| --- | --- |
| 自研解析与 SDK 语义漂移（Preview 的 fail-closed 规则是隐式知识） | 把 7 条规则写成表驱动测试，fixture 直接取官方 catalog 片段；不一致即测试红，不靠人读文档 |
| 目录含 82 条 `internal` + 84 条 `exploratory`，全量导入会污染键盘入口 | 过滤门用 `media`：只默认导入 solution 级 `media=[text]` 的 21 条（实测数字），图片/视频/3D/音频类 67 条默认不导入交给 eikona/scaena；`maturity` 只做徽章不做准入（详见 §1.1 的默认导入判据） |
| 正文进日志（隐私红线） | 渲染结果只在返回值与显式上屏路径出现；审计白名单字段 + 单测正则断言 |
| `kind=number` 改共享层（两平台） | 先只加校验器不改 TOML 形状（number 用 text 存），等两平台验证后再落字段 |
| 覆盖层与本地手写模板 ID 撞车 | 导入 ID 加前缀 `pr:`；UI 冲突时报错不猜测 |
| 目录刷新把用户 pin 的版本悄悄升掉 | 刷新只更新快照，升级必须显式勾选；`pinned=true` 的项永不自动升 |

## 11. 明确不做

- 不做模板上传/发布（发布留在 template-registry + git）。
- 不做远端执行、不做远端脚本、不做 git hook/LFS。
- 不做跨 owner 数据库读取。
- 不做通用 agent 编排（recipe >3 步 → herdr）。
- 不做模板市场 UI（评分/下载榜/贡献者）。
- 不在 Clavis 内实现 S3/Git 拉取（第一版）：目录由外部命令落到本地目录，Clavis 只读盘。
