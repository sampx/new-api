# SAM-FORK-MOD.md — sampx/new-api Fork 定制与部署规范

本文档是 `sampx/new-api` 相对官方上游 `QuantumNous/new-api` 的 **Sam 定制变更清单与分支/部署规则**。

任何涉及本 Fork 的代码修改、Bug 修复、上游同步、构建或部署工作，都应先阅读本文档。

---

## 1. 分支职责与部署机制

自 2026-10-31 起，Fork 采用以下固定分支策略：

| 分支 | 用途 | 允许内容 |
| :--- | :--- | :--- |
| `main` | Fork 基线分支，跟踪 `origin/main` | 不承载 Sam 私有定制提交 |
| `sam-dev` | Sam 的长期定制、修复、构建与部署分支 | 所有 Sam 专属改动、Bug 修复、部署脚本与适配 |
| `upstream/main` | 官方 `QuantumNous/new-api` 参考分支 | 只用于 fetch、比较和未来升级评估 |

### 强制规则

1. **所有 Sam 自定义开发都在 `sam-dev` 进行。**
   - 新增功能、兼容修复、路由/重试改造、部署相关变更都提交到 `sam-dev`。
   - 不再把 Sam 定制提交直接堆在 `main` 上。

2. **所有自行构建与生产部署都以 `sam-dev` 为唯一代码来源。**
   - 本地构建前应确认当前分支为 `sam-dev`。
   - TX 等生产环境部署的 Docker 镜像必须由 `sam-dev` 构建。
   - `main` 只作为 Fork 基线，不作为 Sam 定制生产版本的构建来源。

3. **今天（2026-10-31）不执行上游升级或合并。**
   - 允许 `git fetch upstream` 用于检查官方进展。
   - 未经单独的升级任务，不把 `upstream/main` merge/rebase/cherry-pick 到 `sam-dev`。
   - 当前优先在 `sam-dev` 修复已知问题并保持现有部署稳定。

4. **未来上游升级采用“新基线 + 逐项审计 Sam 定制”的受控流程。**
   - 先选择合适的官方 release/commit 作为新基线；
   - 再逐项判断本文档中的 Sam 定制是“已被上游替代 / 仍需保留 / 需要重写”；
   - 不直接把大量 `upstream/main` 提交一次性硬合并到当前生产分支；
   - 完成回归验证后再切换新的生产基线。

5. **每个新增 Sam 定制提交都要同步更新本文档。**
   - 记录提交哈希、背景、主要行为、涉及模块及是否仍需长期保留。
   - 仅用于维护本清单本身的 docs-only 记录提交不单独编号，避免清单产生自引用递归；其所记录的业务/治理提交仍必须逐项列出。

### 当前分支状态（2026-10-31）

- `main`：`c27d1ef65`，与 `origin/main` 对齐。
- `sam-dev`：截至本次业务修复提交 `d18e0d500`，包含当前全部 8 个需在未来上游升级时审计的 Sam 定制/治理提交，并跟踪 `origin/sam-dev`；其后的纯清单维护提交不单独作为定制编号。
- 官方 `upstream/main` 已明显领先当前 Fork 基线，但本阶段只做参考，不合并。

---

## 2. 当前 Sam 定制提交总览

截至业务修复提交 `d18e0d500`，`sam-dev` 相对 `origin/main` 包含以下 8 个需要在未来上游升级时识别的定制/治理提交：

| 编号 | 变更主题 | 提交哈希 | 涉及模块 | 主要功能 |
| :--- | :--- | :--- | :--- | :--- |
| **01** | 渠道重试算法优化与优先级平滑降级 | `ec607f317` | `relay`, `model`, `service`, `web` | 同优先级内穷尽渠道/多 Key 后再降级；支持 Token 自动分组 |
| **02** | TX 自动化生产部署脚本 | `d9baeebba` | 根目录脚本 | 构建 Docker 镜像并同步部署至 TX 服务器 |
| **03** | Auto-groups 防御性容错解析 | `4765a4d7e` | `model` | 非 JSON 历史值容错，避免解析异常 |
| **04** | Header Override 支持 `||` 回退与动态 UUID | `4c21df678` | `relay/channel` | 多级客户端 Header 回退、`{uuid}` 生成、渠道测试兼容 |
| **05** | 渠道切换时重置运行时 Header Override | `48252b208` | `relay/channel` | 避免重试/切换渠道后沿用前一个渠道的运行时 Header 覆盖状态 |
| **06** | `sam-dev` 分支与生产部署治理规则 | `3cf6eeb85` | `docs` | 明确 `main` / `sam-dev` / `upstream/main` 职责、生产构建来源与受控上游升级流程 |
| **07** | Retry exhaustion 错误保真 | `247c8a27a` | `controller`, `service`, `docs` | 保留 `RetryTimes` 与既有选路机制；候选耗尽时向客户端返回最后真实 upstream error，而不是 generic `get_channel_failed` |
| **08** | 终态 retry 错误汇总与日志追踪 | `d18e0d500` | `controller`, `docs` | 最终重试失败时汇总各渠道 upstream 错误，客户端消息硬限制 400 Unicode 字符；成功响应不携带历史错误，服务端记录清晰 retry trace |

---

## 3. 详细变更记录

### 01. 渠道重试算法优化与多 Key 轮询降级 (`ec607f317`)

- **相关设计文档**：[`docs/retry-algorithm-design.md`](retry-algorithm-design.md)
- **背景与问题**：
  - 原版 New API 在请求外部渠道遇到 429（限流/配额超限）触发重试时，`RetryIndex` 与渠道优先级 `Priority` 硬绑定。
  - 发生失败时容易直接跨到更低优先级，无法先尝试同级其他渠道或同一渠道的其他 Key。
- **主要实现**：
  1. 重构渠道选择与 retry 流程，同优先级候选优先耗尽后再降级；
  2. 单次请求内记录失败的 channel/key，避免同一失败节点被立即重复选择；
  3. 支持 Token 自动分组（Auto-Groups）及相关结算/切换；
  4. 配套前端与回归测试。
- **维护注意**：重试状态码由运行时配置决定。客户端参数错误等 4xx 不应无意义重试并掩盖真实上游错误。

---

### 02. TX 服务器自动化部署脚本 (`d9baeebba`)

- **涉及文件**：[`deploy_to_tx.sh`](../deploy_to_tx.sh)
- **主要功能**：
  - 构建本地 Docker 镜像并打标签；
  - 将镜像导出并通过 SSH/SCP 传输至 TX 生产服务器；
  - 自动加载镜像并重启生产容器。
- **分支约束**：自 2026-10-31 起，该部署流程必须从 `sam-dev` 执行，不能从 `main` 构建 Sam 的生产镜像。

---

### 03. Auto-groups 非 JSON 格式防御性解析 (`4765a4d7e`)

- **涉及文件**：
  - `model/token.go`
  - `model/token_auto_groups_cache_test.go`
- **主要功能**：
  - 兼容数据库中的历史非 JSON `auto-groups` 值；
  - 增加防御性 fallback parsing；
  - 补充回归测试，避免旧数据导致解析异常。

---

### 04. Header Override 支持 `||` 短路回退与动态 UUID (`4c21df678`)

- **涉及文件**：
  - `relay/channel/api_request.go`
  - `relay/channel/api_request_test.go`
- **背景与需求**：
  - 部分上游要求存在会话类 Header（例如 `x-opencode-session`）；
  - 不同下游客户端发送的会话 Header 名称不同，需要统一回退机制。
- **主要实现**：
  1. 支持 `||` 级联短路回退：
     ```json
     "x-opencode-session": "{client_header:x-opencode-session} || {client_header:x-session-affinity} || {client_header:x-session-id} || {uuid}"
     ```
  2. 支持 `{client_header:NAME}`、`{uuid}` / `{random_uuid}` / `crypto.randomUUID()`、`{api_key}` 等占位符；
  3. 在渠道测试没有客户端 Header 的情况下，可自动回退到动态 UUID，而不是直接失败。

---

### 05. 渠道切换时重置运行时 Header Override (`48252b208`)

- **背景与问题**：
  - 单次请求在 retry/fallback 期间可能切换 channel；
  - Header Override 中存在运行时展开后的状态，如果切换 channel 时未清理，可能把前一渠道的 override 结果带入下一渠道。
- **主要实现**：
  - 在 channel 切换时重置 runtime header override；
  - 确保每个新 channel 都基于自身配置重新计算 Header 覆盖；
  - 避免多渠道重试链出现跨渠道 Header 污染。

---

### 06. `sam-dev` 分支与生产部署治理规则 (`3cf6eeb85`)

- **背景与目的**：
  - 将官方 Fork 基线与 Sam 长期定制开发明确分离；
  - 避免 Sam 私有修复继续直接堆叠在 `main`；
  - 为未来受控升级 `upstream/main` 保留可审计边界。
- **主要规则**：
  1. `main` 仅作为跟踪 `origin/main` 的干净 Fork 基线；
  2. `sam-dev` 是 Sam 定制开发、构建与生产部署的唯一长期分支；
  3. 上游升级必须作为独立任务执行，不在普通 Bug 修复中顺手 merge/rebase/cherry-pick。

---

### 07. Retry exhaustion 错误保真 (`247c8a27a`)

- **相关设计文档**：[`docs/retry-algorithm-design.md`](retry-algorithm-design.md)
- **背景与问题**：
  - 某次 upstream 请求已经返回真实错误并命中自动重试规则后，当前请求会把失败的 channel/key 加入排除集并继续选路；
  - 如果候选在 `RetryTimes` 预算耗尽前先被排空，下一次选路会返回 generic `get_channel_failed`，从而覆盖此前最后一个真实 upstream error；
  - `RetryTimes` 本身是有效配置，必须继续作为普通 Relay 的最大重试预算，不能被绕过。
- **主要实现**：
  1. 保持 `RetryTimes`、自动重试状态码、优先级、权重、多 Key 与 auto-group 的原有重试/选路机制不变；
  2. selector 仅向 controller 暴露“本次选择已确认候选耗尽”的状态，不改变候选排序或选择结果；
  3. 若已有真实 upstream error 且随后确认候选耗尽，则最终客户端错误恢复为 `relayInfo.LastError`；
  4. 初始就无可用渠道、尚未请求 upstream 时，继续返回原有渠道选择失败错误；非 exhaustion 的新选路错误也不会被旧 upstream error 覆盖。
- **维护注意**：
  - 例如 `RetryTimes=8` 时，初次请求之后最多再重试 8 次；候选先耗尽则提前停止，预算先耗尽也停止；
  - 不要为了错误保真修改现有 channel/key 选择顺序或自动重试状态码策略。

---

### 08. 终态 retry 错误汇总与日志追踪 (`d18e0d500`)

- **相关设计文档**：[`docs/retry-algorithm-design.md`](retry-algorithm-design.md)
- **背景与需求**：
  - 仅返回最后一个 upstream error 仍然不足以判断一次客户端请求到底尝试过哪些渠道、各渠道分别为什么失败；
  - 但把完整 provider 错误链原样返回会撑爆客户端错误展示，因此必须限制客户端错误长度；
  - 成功重试不应把前序失败历史泄露到正常响应中，而服务端日志仍应保留完整重试轨迹。
- **主要实现**：
  1. 保持 `RetryTimes`、自动重试状态码、优先级、权重、多 Key 与 auto-group 的现有重试/选路行为不变；
  2. 每次渠道 relay 失败后记录 channel id/name、key index、HTTP status 与脱敏错误文本，仅用于当前请求的终态展示与日志；
  3. 若请求确实发生过 retry 且最终仍以重试链中的 upstream error 结束，则客户端返回按尝试顺序汇总的简短错误摘要；
  4. 客户端摘要连同 request id 最多 400 个 Unicode 字符：空间不足时先去掉渠道名称，再压缩各渠道错误文本，最终仍受 400 字符硬上限；
  5. 如果后续渠道成功，客户端只返回正常成功响应，不包含历史错误；如果后续终态变成新的本地/选路错误，也不让旧 upstream 历史覆盖该错误；
  6. 服务端增加 `relay retry trace`，记录 attempt、channel、key index、status、error 与 retry/stop/success 动作；客户端长度限制不影响日志完整性。
- **维护注意**：
  - 客户端错误摘要只用于最终失败的 retry 链，不要扩展成成功响应 metadata；
  - channel id 与 status 是摘要的优先保留字段，错误文本必须先脱敏再进入客户端；
  - 不要为了汇总错误历史改变现有 retry 决策或 selector 顺序。

---

## 4. 后续 Bug 修复记录规则

从本决策起，后续在 `sam-dev` 上修复的兼容问题也必须继续记录在本文件，例如：

- OpenAI-compatible 上游对 `developer` / `system` role 的兼容；
- `reasoning_effort` 等参数的 Provider/Model 差异；
- retry/fallback 过程中的错误保真；
- DeepSeek Harness 等客户端与第三方 OpenAI-compatible Provider 之间的协议适配。

每项落地后应追加新的编号、提交哈希与行为说明，避免未来升级上游时无法判断哪些定制仍需保留。
