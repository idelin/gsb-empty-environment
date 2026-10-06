# seqstore

跨表查询库：多个表复制同一条**统一提交日志**，每个事务（即使跨多张表）
只获得一个全局提交序号。查询据此保证不会读到某个事务的一半。

## 模型

- `Primary`：唯一的事实源，按序追加统一提交日志（`LogEntry{Seq, Writes}`）。
- `Replica`：每张表一份日志副本。日志条目允许**乱序到达**，副本缓存后
  严格按序号连续应用，`Watermark` 只连续推进、绝不跳号；每个键保留全部
  版本，因此任意已应用序号的历史状态都可查询。
- `Store`：查询入口。
  - `ReadAt(seq, keys)`：在统一序号 `seq` 上跨表读取。任一相关表的
    `Watermark < seq` 时返回 `*WaitError{Table, Have, Want}`，告诉调用方
    **需要等待的序号**，而不是返回混入部分事务的结果。
  - `Read(keys)`：不阻塞，取所有相关表 `Watermark` 的最小值作为一致
    快照读取，并返回该快照序号。
  - `WaitFor(ctx, tables, seq)`：阻塞等待各表追平序号。

## 验证（乱序复制模型）

测试将提交日志**打乱顺序**逐条投递给各表副本，每一步都核对：

- `TestCrossTableTransferConservation`：跨表转账在任何一致快照下总额守恒；
  对最新序号的读取要么得到完整结果，要么得到 `WaitError`，绝不出现
  半笔转账。
- `TestLaggingTableNeverMixesPartialTransaction`：转账只复制到一张表时，
  查询返回等待序号；非阻塞读取回退到转账前的一致快照。
- `TestHistoricalQueries`：乱序复制完成后，任意历史序号的查询精确复现
  该提交点的状态。
- `TestWaitForBlocksUntilReplicaCatchesUp`：等待在副本追平后解除。

运行：

```sh
go test ./...
```
