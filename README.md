# 窗口结果汇合器（兼容切片版）

分片上报的局部结果只在**互相兼容**时才被合成整窗结果：同一规则版本、
存活分片、各分片最新局部版本。所有状态保存在本地文件，可完整回放，
不依赖任何外部消息平台。

## 输入协议（`combiner/protocol.py`）

- `ShardReport`：`(window_id, shard_id, epoch, local_version, watermark,
  rule_version, value, kind)`。`kind` 为 `upsert` 或 `revoke`（撤销）。
- `HandoverManifest`：`(window_id, parent, children, epoch)`，声明父分片
  在指定 epoch 被子分片替换。
- 每次发布绑定**版本向量** `{shard: local_version}` 与各片水位；
  `result_id` 是对发布内容的确定性哈希，同一逻辑结果永远是同一 ID。

## 语义（`combiner/combiner.py`）

- **缺片**：窗口保持未决，直到截止时间的 `tick()` 触发超时。
- **旧版本**：`local_version` 更低判 `stale_version`，相同判 `duplicate`；
  同版本不同规则版本视为按新规则重发，替换旧切片。
- **兼容判定**：按 `rule_version` 分组，某组覆盖全部期望分片才发布；
  其它规则版本的切片继续等待（B 属另一规则版本时窗口保持未决）。
- **撤销**：移除该切片；窗口已发布时产生**补偿事件**（负差量），
  历史发布记录原样保留。
- **超时**：发布 `complete=false` 的部分结果并列出 `missing` 分片。
- **扩缩容**：应用交接清单后父分片立即退役，其切片被移除，迟到的父
  分片上报被拒绝——旧分片与新子分片不会重复计入。
- **发布后更正**：产生 `compensation` 记录（`delta` + `supersedes`），
  绝不静默改写历史。

## 持久化与恢复（`combiner/storage.py`）

```
journal.log              追加式事件日志（fsync，容忍最后一行撕裂）
published/<id>.json      带 SHA-256 校验和的发布记录（tmp+rename 原子写入）
acks.log                 下游确认日志
snapshots/<w>-<seq>.json 绑定日志偏移量的窗口快照
```

发布流水线：`publish_intent` → 写发布记录 → 日志 `published` → 下游投递
→ 写 ack。在投递与 ack 之间崩溃后，`recover()` 重放日志、补发未确认的
记录——结果 ID 不变，下游按 ID 幂等去重，差量不会重复应用。

## 独立校验器（`combiner/validator.py`）

只读磁盘文件、自带一套独立重放实现，检查：

- 日志完整性（仅允许最后一行撕裂）；
- 发布记录可解析、校验和与确定性 ID 正确、与日志互相印证（部分写入/
  孤儿 tmp 文件会被发现）；
- 每条版本向量只含已接受、同规则版本、属期望分片的切片，且父子分片
  不重复计入；发布值与补偿差量可由切片精确重算；
- 确认日志无重复、无悬空 ack；
- 每份历史快照与“重放到其偏移量”的状态完全一致。

## 运行

```bash
python3 demo.py /tmp/combiner-data   # 端到端场景（含崩溃恢复）
python3 -m pytest tests/ -q          # 14 项测试
```

兼容 Python 3.11+，仅标准库。
