分布式事务数据库（CockroachDB 风格）在没有原子钟硬件的前提下，使用混合逻辑时钟（HLC，给定全集群最大时钟偏斜界 `maxOffset`）结合**读不确定性窗口（Read Uncertainty Interval）**与**写意图（Write Intent）**来保证严格的快照隔离与外部一致性（Linearizable External Consistency）：1）**HLC 推进与不确定性窗口读重启**：事务 `T` 启动时从协调节点 HLC 取初始读时间戳 `readTS` 并设定不确定性上界 `maxTS = readTS + maxOffset`；读取某节点 `N` 上键 `K` 时，若发现存在已提交版本的写入时间戳 `v.ts` 落在半开区间 `(readTS, maxTS]` 内（且 `v.ts` 高于此前已在节点 `N` 观测到的节点安全上界 `observed[N]`！），说明无法仅靠时钟判断该写入在物理时间上究竟早于还是晚于 `T` 的启动——引擎必须立刻更新 `observed[N]`、将 `T.readTS` 推高至 `v.ts`（但保持 `maxTS` 不变，防止无限漂移！）并触发 `ReadRestartError` 让事务在新时间戳下安全重读！2）**未决写意图（Write Intent）解析与优先级推戳（Push Timestamp）**：写操作在键上写入指向自身事务记录的 `Write Intent`；其他读写事务撞上某 `Intent` 时，查其事务记录状态——若已 `COMMITTED` 则按真实提交戳处理并原地解析清理该 Intent；若已 `ABORTED` 则忽略清理；若仍 `PENDING` 且 `intent.ts <= reader.maxTS`，则按双方事务优先级尝试推高冲突方时间戳（`PushTimestamp`）或中止低优事务，并维护精确事务记录状态机（`PENDING -> COMMITTED | ABORTED` 原子单向迁移，提交戳必须 `>=` 所有已写 Intent 的最大被推高戳！）；3）**提交后写偏移防线性化违例**：若节点在事务提交前发生时钟回拨或被推高提交戳，引擎保证同一事务全部 Intent 最终以统一的 `commitTS` 原子对外可见。为分布式事务存储层交付可导入的 Go HLC 不确定性读重启、Write Intent 解析与外部一致性仿真包。验收在注入多节点时钟偏移（`<= maxOffset`）的并发事务工作负载下，验证：落在 `(readTS, maxTS]` 的跨节点已提交写精准触发读重启且同一节点不死循环、`PENDING`/`COMMITTED`/`ABORTED` Intent 完整解析、优先级推戳防死锁以及所有已提交事务严格满足实时外部一致性（`T1` 物理提交先于 `T2` 开始则 `T2` 必见 `T1`！）。

## 交付物：`hlctxn` 包

可导入的 Go 仿真包位于 `hlctxn/`，模块路径 `prescreen.local/ps-dc61858403/hlctxn`。

- `hlctxn/clock.go`：HLC 时间戳与时钟（物理钟回拨保持单调、远端戳因果合并），含真实钟/仿真钟/偏斜钟。
- `hlctxn/engine.go`：多节点集群、FNV 分片、MVCC 版本链、写意图与事务记录状态机。
- `hlctxn/txn.go`：`Begin/Get/Put/Commit/Abort`，不确定性窗口扫描、读重启、意图解析、优先级推戳与原子提交。
- `hlctxn/retry.go`：`GetWithRestart`、`RunWithRetry` 有界自动重试与 `DrainIntents`。
- `hlctxn/doc.go`：包级设计说明与外部一致性（线性化）形式化论证。
- `hlctxn/*_test.go`：19 个单元/并发验收测试 + 2 个可运行 Example。

### 运行

```sh
go test ./...            # 全量验收
go test ./... -race      # 竞态检测
go test ./... -v         # 查看每个用例
```

### 验收覆盖

- 读重启：`(readTS, maxTS]` 且高于 `observed[N]` 的跨节点提交精准触发一次 `ReadRestartError`，同节点不二次重启；`maxTS` 全程不变。
- 意图解析：`COMMITTED`/`ABORTED`/`PENDING` 三态完整解析（懒解析模式可由读者原地触发）。
- 推戳防死锁：优先级 + 事务 ID 严格全序决胜，高优读者推戳、高优写者中止低优事务，落败者退避重试。
- 外部一致性：仿真钟定向偏斜场景与多节点偏斜并发工作负载均验证 `commit(T1) < start(T2) => T2 必见 T1`。
