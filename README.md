# winmerge — 窗口结果汇合器

把分片上报的局部结果按**兼容切片**汇合成整窗结果。纯 Python 3.11+，
零依赖，状态只落本地文件（哈希链事件日志 + 原子写下游状态），不借
外部消息平台掩盖一致性问题。

## 输入协议（`winmerge/protocol.py`）

所有入站事件都是带 `type` 字段的 JSON 对象：

| type      | 字段 | 语义 |
|-----------|------|------|
| `partial` | `window, shard, epoch, local_version, watermark, rule_version, value` | 分片局部结果，带分片 epoch / 局部版本 / 输入水位 / 规则版本四元坐标 |
| `revoke`  | `window, shard, epoch, local_version, reason` | 撤销已上报切片 |
| `handoff` | `manifest_id, epoch, splits{旧分片:[新子分片]}` | 扩缩容交接清单 |
| `ack`     | `result_id` | 下游确认 |
| `tick`    | `now` | 推进时钟，驱动窗口超时 |

## 汇合规则

- 窗口仅在**当前活跃分片集**被同一 `rule_version` 的切片全覆盖时发布；
  每片取最高 `local_version`、未被撤销的切片。规则版本不一致 → 保持未决。
- 发布记录绑定**版本向量** `{shard: local_version}` + epoch + 规则版本，
  `result_id` 由内容确定性哈希得出（恢复后重放得到同一 ID）。
- 分类处理：`stale_version`（旧局部版本）、`stale_epoch`（交接后的旧分片）、
  `window_expired`（超时缺片，记录缺失清单）、`slice_revoked`（撤销）。
- 交接清单生效后旧分片立即失效，旧 A 与新子片 A1/A2 不会重复计入。
- 发布后的更正产生**补偿事件**（`kind=compensation`，链式 `base_result_id`，
  带差量 `delta`），历史发布记录永不改写。

## 两阶段发布与恢复

1. 发布记录先追加到哈希链日志并 fsync；2. 再投递下游；3. 记录 ack。
在 1–3 之间崩溃时，`recover()` 重放日志找到未确认的发布，用**同一个
result_id** 重投；下游按 `result_id` 幂等去重，差量只应用一次。

## 独立校验器（`winmerge/validator.py`）

不复用汇合器状态机，从日志独立重放并审计：哈希链完整性、撕裂写
（部分写入）、重复计入（版本向量与活跃分片集精确重合、血缘不相交）、
发布值重算、补偿链纪律、重复/未知确认、result_id 可重算。支持
`snapshot`/`verify` 对历史前缀做快照校验。

```bash
python3 -m winmerge.validator check     <state_dir> --initial-shards A,B
python3 -m winmerge.validator snapshot  <state_dir> --out snap.json
python3 -m winmerge.validator verify    <state_dir> --snapshot snap.json
```

## 运行

```bash
python3 demo.py            # 完整场景：合成 8、规则不匹配未决、A 拆分交错
                           # 抵达、进程在发布与确认间崩溃、恢复幂等、补偿
python3 -m pytest tests/   # 17 项行为与校验器测试
```
