# 有限窗口模式匹配器（A → W内 B，中间无 C）

监控日志按**到达顺序**摄入，告警按**事件时间**判定。模式定义：

- 事件 `A` 与其后的事件 `B` 满足 `0 <= tB - tA <= W`（窗口两端均包含，同 tick 的 A/B 可匹配）；
- 区间 `[tA, tB]` 内不得存在任何 `C`（含端点：与 A 或 B 同时间的 C 同样使匹配无效）；
- 同类型事件时间相同时按事件 ID 排序（内部索引按 `(timestamp, id)` 全序排列，保证确定性）；
- 输出**所有**满足条件的 `(AID, BID)` 对，每对的告警 ID 稳定：`alert:{AID}->{BID}`。

## 事件模型与修订语义

每个事件有稳定 `id` 与单调递增的 `revision`（`src/types.ts`）。`PatternMatcher.apply()`（`src/matcher.ts`）按如下规则处理一条修订：

| 情况 | 结果 | 状态 |
| --- | --- | --- |
| 更高修订 | `applied` | 应用（可改类型、时间或撤销） |
| 同修订同内容 | `duplicate` | 幂等无操作 |
| 同修订不同内容 | `conflict` | 拒绝并记录到 `conflicts`，状态不变 |
| 更低修订 | `stale` | 忽略 |
| `timestamp < watermark - allowedLateness` | `late` | 拒绝，状态不变 |
| 引用已剪枝事件 | `stale`/`late` | 拒绝（见下文墓碑规则） |

## 输出变更流

每条变更为 `{seq, kind: 'upsert'|'retract', alertId, aId, bId}`，`seq` 严格递增（含 checkpoint 恢复之后）。

- 迟到的 `C` 或 `A`/`B` 被撤销/移出窗口 → 对相关告警发 `retract`；
- 阻塞条件消失（`C` 被撤销或改类型）→ 恢复时发 `upsert`；
- 同一 `(AID, BID)` 的告警 ID 在 retract/upsert 周期中保持稳定；
- 下游用 `seq` 去重（或利用集合语义）即可让变更流重复消费不重复计数（见测试 9）。

## 水位线、剪枝与归档

`advanceWatermark(ts)` 单调推进（非递增调用被忽略）。记
`floor = watermark - allowedLateness`，`pruneBefore = floor - W`：

- `timestamp < floor` 的修订被拒绝（边界包含：`timestamp == floor` 接受）；
- `timestamp < pruneBefore` 的存活事件从内存剪枝——它们不可能再参与任何新对
  （作为 A：未来 B 至少落在 `floor`，超出 `tA + W`；作为 C：未来对的区间不早于
  `floor - W`）；
- `tB < pruneBefore` 的开放对被**归档**（从内存移除，经 `onArchive` 回调外置）。
  此时两个端点均已被剪枝、区间内任何 C 的修订也必然因迟到被拒，因此封闭告警
  不会再被任何可接受的修订影响；
- 剪枝时留下紧凑墓碑 `id -> 最高修订号`：旧修订重投判 `stale`，更高修订判
  `late`（已封闭状态不允许复活），这保证了归档的正确性与内存有界。

## 内存界限

随开放窗口增长的部分：

- 存活事件索引：`O(horizon 内事件数)`，`horizon = W + allowedLateness`（加水位线
  尚未推进的在途数据）；
- 开放告警：`O(horizon 内 A 数 × B 数)` 的最坏上界，实际受 C 密度约束；
- 单次变更的重算只触及 `[t-W, t]` 范围，成本与全局事件数无关。

与窗口无关的部分：墓碑表为 `O(不同事件 id 数)`；若 id 空间有界或去重外置，
则内存完全由开放窗口决定。已归档告警不占用内存。

## Checkpoint / 恢复

`snapshot()` 序列化全部算子状态（记录、开放对、水位线、seq、墓碑），
`PatternMatcher.restore(json)` 重建后继续消费，输出与未中断运行一致
（下游消费端需各自 checkpoint 其视图，见测试 8）。

## 参考实现与一致性

`src/reference.ts` 提供一次性参考计算：解析每个 id 的最高修订（同修订不同内容
视为冲突抛错），按 `(timestamp, id)` 排序后枚举所有有效对。随机乱序、同 tick
冲突、水位线推进+迟到拒绝等场景下，流式最终有效告警集合与参考计算完全一致
（测试 6、10）。

## 运行

需要 Node ≥ 23.6（原生 TypeScript type-stripping；Node 22.6+ 可用
`--experimental-strip-types`）。无第三方依赖，测试数据全部本地合成。

```sh
npm test
```
