# 渠道重试与多 Key 轮询优化设计方案 (Target State Design)

## 1. 背景与现状问题

在现有的 `new-api` 架构中，当请求外部 AI 渠道返回 429 (Rate Limit / Quota Exceeded) 等错误时，系统会触发重试 (`RetryTimes`)。
但当前的降级与重试算法存在以下缺陷：

1. **重试次数（RetryIndex）与渠道优先级（Priority）硬绑定**：
   在 `model/channel_cache.go` 的 `GetRandomSatisfiedChannel` 中，系统首先将匹配的模型对应渠道的所有 Priority 进行**去重**（存入 `uniquePriorities`），然后直接使用 `targetPriority = sortedUniquePriorities[retry]`。
   这导致：
   - 只要发生 429 导致 `retry` 增加，下一次请求**必定跨越到下一个更低的优先级**。
   - 同一个 Priority 即使有多个渠道节点，或者单一渠道包含多个 Key（`IsMultiKey=true`），也无法在同优先级内耗尽所有可能性再降级。
2. **429 错误未能有效隔离当前失败节点**：
   - 当遇到 401（Key 无效）时，系统会封禁单 Key 或渠道。
   - 当遇到 429（配额超限）时，由于是“瞬时错误”，系统不执行禁用（AutoBan 不生效）。但因为下一次重试直接跳了 Priority，导致原本可以轮询的多 Key 完全失效。

**用户诉求**：
在触发重试时，**必须优先在当前优先级（Priority）内穷尽所有的渠道和 Key 组合**。只有当同一优先级下的所有可用候选者（按权重/多Key）都发生 429 失败后，才真正“跃阶”降级到下一个 Priority。

## 2. 目标 (Target State)

改造后，系统应呈现如下行为：
- **穷尽同级资源**：如果 Priority 8 中有 3 个 Key（无论是在 1 个多 Key 渠道里，还是拆分成 3 个单 Key 渠道），当出现 429 时，剩余的重试次数应优先消耗在这剩下的 2 个未尝试的 Key/渠道上。
- **平滑降级**：只有当 Priority 8 的所有候选者都被本次请求证实不可用（均在本次黑名单中），才去获取 Priority 7 的渠道。
- **保护全局状态**：不使用持久化的 AutoBan 来处理 429，只在当前 `gin.Context` 级别维护一个“本请求生命周期内的黑名单”（排斥名单）。
- **完全兼容现有计费**：不修改任何计费逻辑（保持 `preConsume` / `Settle` 安全），不改变 GORM 数据库结构。

## 3. 设计方案

改造主要分为两个上下文隔离层：

### 3.1 请求上下文级的黑名单记录 (`controller/relay.go`)

在处理 429 等重试逻辑时，我们需要精确记录“本次请求已经失败过的具体渠道和 Key”，防止在同级别轮询时重复抽中。

- **现有机制**：`c.GetStringSlice("use_channel")` 仅记录了 `Channel ID`，无法区分单渠道下的多 Key。
- **优化设计**：
  在 `controller/relay.go` 的 `for` 重试循环中，当遇到需要重试的错误时（例如 429）：
  - 构造一个精确的失败标识符，例如 `fmt.Sprintf("%d_%d", channel.Id, channel.MultiKeyIndex)`（如果是非多 Key，Index 默认为 0）。
  - 将该标识符追加到 `gin.Context` 中的一个专门的键中（例如 `constant.ContextKeyFailedChannelKeys`）。
  - （注意，不需要修改 `use_channel` 的现有日志行为，只需新增一个专用于路由过滤的结构）。

### 3.2 路由选择算法的解绑 (`model/channel_cache.go`)

重构 `GetRandomSatisfiedChannel` 的算法，打破 `retry` 与 `uniquePriorities` 下标的 1:1 硬绑定。

**原逻辑**：
```go
targetPriority := sortedUniquePriorities[retry] // 直接按 retry 取对应的 Priority
// 然后在 channels 中找 Priority == targetPriority 的
```

**新逻辑思路**：
1. **收集与过滤**：遍历 `group2model2channels` 拿到的原始渠道列表 `channels`，同时读取 `gin.Context` 中传递的失败黑名单（通过扩展 `RetryParam` 传入）。
2. **动态候选池构建**：
   - 按照渠道的 Priority 对可用渠道进行分组。
   - 过滤掉当前渠道当前 `KeyIndex` 已经被记录在黑名单中的候选。
3. **基于权重的选择 (不依赖外层的 `retry` 下标)**：
   - 取出过滤后**剩余的所有渠道中，Priority 最高的这一组**作为 `targetChannels`。
   - 对这个最高 Priority 的 `targetChannels` 执行现有的按 `weight` (权重) 随机抽取的逻辑。
4. **同级耗尽则自然降级**：
   - 由于黑名单是累加的，如果某个 Priority 下的渠道组合被试光了，那下一次进函数时，过滤后剩下的最高 Priority 自然就变成了更低一级的 Priority。这就完美实现了同级耗尽后再降级的需求。

### 4. 影响范围与安全评估

1. **数据库 (SQL)**：无变化，零影响。
2. **计费系统 (Billing)**：
   - 计费发生在外层 `controller/relay.go` 选定渠道之后 (`PrepareTieredBillingForSelectedGroup`)。
   - `model/channel_cache.go` 仅负责“返回下一个该用的渠道”，对计费无任何入侵，计费系统继续按照选中的渠道执行预扣费，符合 `new-api` 的安全准则。
3. **依赖关系**：
   - `GetRandomSatisfiedChannel` 的方法签名需要微调（或使用现成的 `RetryParam` 传递 Context 信息）。`RetryParam.Ctx` 中已有足够的信息读取过滤黑名单。
4. **性能影响**：
   - 数组过滤和 Map 查找均在纳秒级别，对高并发路由性能几乎无损。

## 5. 实施边界与步骤限制

- **不重构外部包**：只修改 `model/channel_cache.go` (选择算法)、`service/channel_select.go` (封装) 和 `controller/relay.go` (失败写入)。
- **隔离性**：新增的上下文黑名单仅在 `for` 重试循环内有效，请求结束即销毁。
- **实施准则**：严格遵守 Golang 风格，无过深嵌套，并在提交代码前跑通相关测试用例。