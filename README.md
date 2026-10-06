# seqstore

多表一致性查询库：事务由**统一提交序号**定序，可跨表写入；各表独立（可乱序）
复制日志。查询若要求的序号超过某表已应用的水位，返回 `NeedSeqError` 告知
需要等待的序号，**绝不返回混入部分事务的不完整结果**。

## 语义

- `Commit(writes...)`：为事务分配全局递增的提交序号，写入统一日志。
- `Apply(table, seq)`：表按任意顺序应用日志条目；只有连续前缀（水位
  `Watermark`）以内的提交对读可见，提前应用的未来提交在水位到达前不可见。
- `Query(seq, tables...)`：打开固定在 `seq` 的跨表快照。任一相关表水位不足
  时返回 `*NeedSeqError{Table, Required, Applied}`，调用方等待该表追平
  `Required` 后重试。
- `Snapshot.Get(table, key)`：读取不晚于快照序号的最新版本，支持历史查询。

## 验证（engine_test.go）

- `TestNeedSeqInsteadOfPartial`：表滞后时返回等待序号而非部分转账结果。
- `TestOutOfOrderApplyGatedByWatermark`：乱序应用被水位门控，未来提交不泄漏。
- `TestHistoricalQueries`：历史序号查询精确重现过去状态。
- `TestOutOfOrderReplicationConservation`：随机模型——300 轮跨表转账、乱序
  复制与随机滞后；任何时刻查询要么报 `NeedSeqError`，要么总额守恒；追平后
  每个历史序号都与参考模型逐账户一致。

运行测试：`go test ./...`
