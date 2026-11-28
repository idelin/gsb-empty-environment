// Package hlctxn 是 CockroachDB 风格分布式事务存储层的可导入仿真包，
// 在没有原子钟硬件的前提下，用混合逻辑时钟（HLC）+ 读不确定性窗口 +
// 写意图（Write Intent）实现严格快照隔离与实时外部一致性（线性化）。
//
// 核心组件
//
//   - HLC（clock.go）：Timestamp 由物理墙钟 WallTime 与逻辑计数 Logical 组成，
//     Clock.Now 处理物理钟回拨（逻辑计数递增保持单调），Clock.Update 合并
//     远端时间戳维护 happened-before 因果全序。
//   - 集群与 MVCC（engine.go）：Cluster 管理多个 Node，每节点独立 HLC 与
//     分片存储；键经 FNV-1a 哈希分片，每个键维护按时间戳降序的已提交版本链
//     与至多一个写意图。事务记录 TxnRecord 承载权威状态机。
//   - 事务与意图（txn.go）：Begin/Get/Put/Commit/Abort 实现不确定性读重启、
//     意图解析、优先级推戳与原子提交。
//   - 重试辅助（retry.go）：GetWithRestart 与 RunWithRetry 封装有界自动重试。
//
// 读不确定性窗口规则
//
// 事务启动时 readTS 取协调节点 HLC，maxTS = readTS + maxOffset 且生命周期内
// 不可变。首次接触节点 N 时采样 observed[N]（节点 HLC 安全上界）。对版本 v：
//
//   - v.ts <= readTS：快照过去，直接可见；
//   - readTS < v.ts <= observed[N]：节点本地确定过去（HLC 单调性证明该写
//     先于观测事件），静默前推 readTS 并采用该版本，不产生重启；
//   - observed[N] < v.ts <= maxTS：无法仅凭时钟判断物理先后，更新
//     observed[N]、把 readTS 推到 v.ts（maxTS 不变）并返回 ReadRestartError；
//   - v.ts > maxTS 且 v.ts > observed[N]：确定性未来，跳过。
//
// 同一节点的硬重启由 restartedOn 与 observed 上界保证至多一次，不会死循环；
// ErrTooManyRestarts 是防御性断言。
//
// 写意图与事务状态机
//
// Put 在键上放置指向事务记录的意图。撞上他人意图时：
//
//   - COMMITTED：按真实 commitTS 解析，版本已在版本链，清理意图；
//   - ABORTED：直接丢弃清理；
//   - PENDING 且 intent.ts <= maxTS：按优先级全序（priority，再按 txn ID）
//     决胜——高优读者 PushTimestamp（推高对方 WriteTS/意图戳），高优写者
//     原子中止低优事务，落败者收到 ConflictError 退避重试。全序决胜杜绝
//     循环等待，因此无死锁。
//
// 状态迁移只允许 PENDING -> COMMITTED 或 PENDING -> ABORTED，原子且单向；
// commitTS 取协调节点 HLC 与所有意图最大被推高戳 WriteTS 的较大者，因此
// commitTS >= 全部意图时间戳，且同一事务的所有意图以统一 commitTS 原子可见。
//
// 外部一致性（线性化）论证
//
// 设事务 T1 在真实时间 commit(T1) 提交，事务 T2 在真实时间 start(T2) 开始，
// 且 commit(T1) < start(T2)。T1 提交时对每个存储节点执行 HLC.Update(commitTS)，
// 由 HLC 单调性，T2 之后首次接触该节点采样到的 observed[N] >= T1.commitTS。
// T2 读取 T1 写入的键时，其版本满足 v.ts = T1.commitTS <= observed[N]，
// 命中“节点本地确定过去”规则（无论 v.ts 是否超出全局 maxTS），故 T2 必然
// 读到 T1 的已提交写。真实时间先后被转化为 HLC 因果先后，线性化成立。
package hlctxn
