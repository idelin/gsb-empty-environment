# 个体贡献裁剪确定性

隐私计数服务按用户限制可贡献记录数，但乱序和重传使选择不稳定。写供Go采集宿主调用的裁剪包，每用户每窗口保留按带密钥摘要排序最小的K条记录，跨分组另设总上限；重复事件ID幂等，冲突拒绝。合并分片后必须与完整集合裁剪一致，用穷举分区和撤回事件验证结果及被裁剪原因。

## 裁剪规则

- 排名键：`HMAC-SHA256(key, 规范化记录字节)`，覆盖事件 ID、用户、窗口、分组与载荷；摘要相同时以事件 ID 兜底，保证全序。
- 局部上限 K：每个 `(用户, 窗口, 分组)` 保留摘要最小的 K 条，落选者原因记为 `local_cap`。
- 全局上限：每个 `(用户, 窗口)` 跨所有分组再设总上限，局部幸存者竞争失败记为 `global_cap`。
- 幂等：相同事件 ID、相同内容的重复投递为空操作；内容不一致返回 `ErrConflict`，已接受记录不受影响。
- 撤回：`Withdraw` 生成墓碑，撤回事件不占名额；先于事件到达的撤回作为孤儿墓碑保留，分片合并时自动生效，迟到重传无法复活已撤回事件。
- 输出确定性：`Kept` 按 `(摘要, 事件 ID)` 排序，`Dropped`/`Withdrawn` 按事件 ID 排序，并附带每条记录被裁剪的原因。

## 分片合并

每个选择器保留全部已接受事件（而非提前裁剪），`Merge` 取并集并按同样的幂等/冲突规则对账，同时合并撤回墓碑。因此

```
Merge(shards...).Trim() == 在一个选择器上摄入完整集合后 Trim()
```

合并要求各分片使用相同的 HMAC 密钥与上限，否则返回 `ErrInvalidConfig`；跨分片同 ID 内容冲突返回 `ErrConflict`。

## 用法

```go
s, err := netsec.NewSelector(hmacKey, localK, globalCap)
if err != nil { /* ... */ }

if err := s.Ingest(record); errors.Is(err, netsec.ErrConflict) {
    // 同事件 ID 出现内容冲突，拒绝并告警
}
s.Withdraw(record.EventID) // 可选：撤回

result := s.Trim()
// result.Kept      保留记录（摘要序）
// result.Dropped   被裁剪记录及 ReasonLocalCap / ReasonGlobalCap
// result.Withdrawn 撤回墓碑

merged, err := netsec.Merge(shardA, shardB)
```

一次性无状态场景可直接调用 `netsec.TrimRecords(key, records, localK, globalCap)`。

## 验证

- `TestTrimCapsAndReasons`：独立计算 HMAC 顺序，断言两级上限与每条裁剪原因，多轮随机乱序摄入结果一致。
- `TestMergeExhaustivePartitions`：对 8 个事件的全部集合划分（Bell 数 B8 = 4140 种分区）穷举，叠加重传、撤回与分片内乱序，逐分区断言合并裁剪与完整集合参考结果完全一致。
- `TestMergeMultiScopeRandomized`：多用户/窗口/分组下 40 轮随机分片、重传与撤回的等价性。
- 另有冲突拒绝、孤儿墓碑、先撤回后到达、并发竞态（`-race`）等测试。

运行：

```sh
go test -race ./...
go test -cover ./...
```
