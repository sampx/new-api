# SAM-FORK-MOD.md — New API 定制分支修改记录

本文档用于记录 `sampx/new-api`（基于上游 `QuantumNous/new-api`）的所有定制修改，涵盖已完成的历史变更及当前实施中的特性。

---

## 变更概览

| 编号 | 变更主题 | 提交哈希 / 状态 | 涉及模块 | 主要功能 |
| :--- | :--- | :--- | :--- | :--- |
| **01** | 渠道重试算法优化与优先级平滑降级 | `ec607f317` (2026-08-03) | `relay`, `model`, `service`, `web` | 解绑重试与 Priority 映射，同级内穷尽渠道与多 Key，平滑降级；支持 Token 自动分组 |
| **02** | 自动化生产部署脚本 | `d9baeebba` (2026-08-04) | 根目录脚本 | 提供一键构建 Docker 镜像并同步部署至腾讯云服务器的自动化脚本 |
| **03** | Auto-groups 防御性容错解析 | `4765a4d7e` (2026-08-04) | `model` | 修复非 JSON 字符串解析 panic，提供防御性回退与回归测试 |
| **04** | 请求头覆盖支持 `\|\|` 短路回退与动态 UUID | 待实施 (2026-09-26) | `relay/channel` | 支持多级客户端 Header 回退与 `{uuid}` 兜底，适配 OpenCode Go 等不同客户端调用 |

---

## 详细变更记录

### 01. 渠道重试算法优化与多 Key 轮询降级 (ec607f317)

* **相关设计文档**：[`docs/retry-algorithm-design.md`](retry-algorithm-design.md)
* **背景与问题**：
  * 原版 New API 在请求外部渠道遇到 429（限流/配额超限）触发重试时，`RetryIndex` 与渠道优先级 `Priority` 硬绑定（`targetPriority = sortedUniquePriorities[retry]`）。
  * 导致出现 429 时必定直接跨越到下一个更低的优先级，无法在同级内尝试其他可用节点，且单渠道的多 Key 轮询（`IsMultiKey=true`）也会被直接跳过。
* **主要实现**：
  1. **路由算法重构**（`model/channel_cache.go`, `service/channel_select.go`）：
     * 解绑 `retry` 与 `Priority` 的 1:1 映射。
     * 改为在单次请求上下文内维护失败黑名单（`constant.ContextKeyFailedChannelKeys`，格式为 `ChannelID_KeyIndex`）。
     * 过滤已失败节点后，始终在剩余最高 Priority 的候选池中按权重随机选择，只有当同优先级所有节点均失败后才降级到更低优先级。
  2. **请求上下文级黑名单隔离**（`controller/relay.go`）：
     * 针对 429 等瞬时错误只在单次请求内隔离，不触发全局持久化禁用（AutoBan）。
  3. **Auto-Groups 自动分组支持**：
     * 新增 Token 维度的自动分组排序与切换机制（`model/token.go`, `service/group.go`）。
     * 完善多 Key、阶梯结算（Tiered Settle）及完整单元测试。
  4. **前端交互与多语言**：
     * API Key 抽屉、分组 ComboBox 组件升级，补充中/英/日/法/俄等多语言配置。

---

### 02. TX 服务器自动化部署脚本 (d9baeebba)

* **涉及文件**：[`deploy_to_tx.sh`](../deploy_to_tx.sh)
* **主要功能**：
  * 构建本地 Docker 镜像并打标签。
  * 将镜像导出并通过 SSH/SCP 传输到腾讯云生产服务器。
  * 自动执行镜像加载与容器重启，简化生产发布流程。

---

### 03. Auto-groups 非 JSON 格式防御性解析 (4765a4d7e)

* **涉及文件**：
  * `model/token.go`
  * `model/token_auto_groups_cache_test.go`
* **主要功能**：
  * 修复数据库中历史旧数据若存在非 JSON 格式的 `auto-groups` 字符串时导致的解析异常。
  * 增加防御性回退解析（Fallback Parsing），保证系统的稳健性，并补齐了测试用例。

---

### 04. 请求头覆盖支持 `||` 短路回退与动态 UUID 生成 (2026-09-26 实施项)

* **涉及文件**：
  * `relay/channel/api_request.go`
  * `relay/channel/api_request_test.go`
* **背景与需求**：
  * 对接 **OpenCode Go** 等上游渠道时，上游服务强制校验 `x-opencode-session` 请求头，若缺失则返回 `400 Bad Request (MissingSessionID)`。
  * 下游客户端类型多样：
    * OpenCode 原生客户端发送 `x-opencode-session`；
    * Ellamaka 客户端在自定义 Provider 下发送 `x-session-affinity`；
    * 其它第三方客户端可能发送 `x-session-id`、`x-request-id` 或不发送任何会话标识。
  * 原版 `applyHeaderOverridePlaceholders` 仅支持单一的 `{client_header:NAME}` 且无法回退，一旦客户端未携带该 Header，值被替换为空字符串直接触发上游报错。
* **设计与实现**：
  1. **支持 `||` 级联短路回退语法 (Coalesce)**：
     * 按从左到右的顺序求值，命中第一个非空值即返回，全部未命中则不注入。
     * 支持级联表达式：
       ```json
       "x-opencode-session": "{client_header:x-opencode-session} || {client_header:x-session-affinity} || {client_header:x-session-id} || {uuid}"
       ```
     * 兼容包裹语法：
       ```json
       "x-opencode-session": "{client_header:x-opencode-session || client_header:x-session-affinity || crypto.randomUUID()}"
       ```
  2. **内置生成器与语法标识**：
     * `{client_header:NAME}`：从当前客户端请求头中提取。
     * `{uuid}` / `{random_uuid}` / `crypto.randomUUID()`：动态生成标准的 `uuid.New().String()` 作为兜底。
     * `{api_key}`：渠道 API Key 注入。
     * 静态字面量：提取去除引号后的非空字符串作为默认值。
  3. **渠道测试（Channel Test）兼容**：
     * 原逻辑在 `info.IsChannelTest` 时只要存在 `client_header` 就跳过该 Header，导致后台测试渠道时必报缺少 Session ID。
     * 引入回退逻辑后，测试阶段由于无客户端上下文，`client_header` 自动顺位回退到 `{uuid}`，使后台测试功能顺利通过。
