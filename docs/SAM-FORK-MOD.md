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

### 当前分支状态（2026-10-31）

- `main`：`c27d1ef65`，与 `origin/main` 对齐。
- `sam-dev`：`48252b208`，包含当前全部 5 个 Sam 定制提交，并跟踪 `origin/sam-dev`。
- 官方 `upstream/main` 已明显领先当前 Fork 基线，但本阶段只做参考，不合并。

---

## 2. 当前 Sam 定制提交总览

当前 `sam-dev` 相对 `origin/main` 包含以下 5 个定制提交：

| 编号 | 变更主题 | 提交哈希 | 涉及模块 | 主要功能 |
| :--- | :--- | :--- | :--- | :--- |
| **01** | 渠道重试算法优化与优先级平滑降级 | `ec607f317` | `relay`, `model`, `service`, `web` | 同优先级内穷尽渠道/多 Key 后再降级；支持 Token 自动分组 |
| **02** | TX 自动化生产部署脚本 | `d9baeebba` | 根目录脚本 | 构建 Docker 镜像并同步部署至 TX 服务器 |
| **03** | Auto-groups 防御性容错解析 | `4765a4d7e` | `model` | 非 JSON 历史值容错，避免解析异常 |
| **04** | Header Override 支持 `||` 回退与动态 UUID | `4c21df678` | `relay/channel` | 多级客户端 Header 回退、`{uuid}` 生成、渠道测试兼容 |
| **05** | 渠道切换时重置运行时 Header Override | `48252b208` | `relay/channel` | 避免重试/切换渠道后沿用前一个渠道的运行时 Header 覆盖状态 |

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

## 4. 后续 Bug 修复记录规则

从本决策起，后续在 `sam-dev` 上修复的兼容问题也必须继续记录在本文件，例如：

- OpenAI-compatible 上游对 `developer` / `system` role 的兼容；
- `reasoning_effort` 等参数的 Provider/Model 差异；
- retry/fallback 过程中的错误保真；
- DeepSeek Harness 等客户端与第三方 OpenAI-compatible Provider 之间的协议适配。

每项落地后应追加新的编号、提交哈希与行为说明，避免未来升级上游时无法判断哪些定制仍需保留。
