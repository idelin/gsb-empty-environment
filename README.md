# oplog-recovery

从操作日志恢复状态的 TypeScript 实现（零依赖，仅用 Node.js 内置模块与内置测试运行器）。

## 特性

- **哈希链操作日志**：每条记录携带 `seq / branch / opId / type / payload / prevHash / hash`，
  任何篡改都会在 `parseAndVerify` 时被定位到**确切操作序号**（`LogCorruptionError.seq`）。
- **检查点绑定前缀摘要**：检查点记录与恢复检查点都绑定 `prefixDigest`（前缀哈希链摘要）+
  `stateDigest`；前缀摘要不符或快照被篡改时拒绝拼接（`CheckpointSpliceError`）。
- **逐步 / 回退 / 取消**：`step()` 单条前进；`rollback()` 借助日志中存储的**逆补丁**反向步进；
  `cancel()` 中止会话并回到基线状态，`reset()` 后方可继续。
- **事务隔离**：`tx_begin..tx_commit` 之间的操作先缓冲，提交时原子应用；
  读者经 `getState()` 只能看到已提交的冻结快照，永远看不到事务中间态；
  `tx_abort` 与日志末尾未提交的事务直接丢弃。
- **副作用恰好一次**：`effect` 记录触发外部动作并追加 `effect_ack` 确认；
  回放时预扫描确认表，已确认的外部动作**绝不重新触发**；缺失 ack 的动作在恢复时恰好执行一次。
- **重放安全**：重复 `opId` 最多应用一次；缺失中间操作（序号空洞）报
  `MissingOperationError`，损坏的中间记录**不可跳过冒充成功**；恢复后最终摘要等于原状态摘要。
- **对照验证**：`interpretFullLog` 是独立的全日志解释器；测试对每个前缀（逐故障点）
  断言增量恢复结果与其完全一致。
- **检查点回收**：`reclaimCheckpoints()` 只保留最新检查点，返回回收数量。

## 结构

```
src/hash.ts         规范化 JSON + sha256 摘要
src/state.ts        键值状态、补丁应用与逆补丁推导、冻结快照
src/log.ts          记录类型、LogBuilder（构建哈希链日志）、parseAndVerify（校验定位）
src/interpreter.ts  独立全日志解释器（对照基准）
src/recovery.ts     RecoveryEngine：step / rollback / cancel / checkpoint / restore / reclaim
test/recovery.test.ts  node:test 测试（16 项）
examples/demo.ts    运行样例
```

## 运行

需要 Node ≥ 23.6（或 ≥ 22.6 加 `--experimental-strip-types`），直接运行 TypeScript，无需编译。

```sh
node --test test/*.test.ts   # 运行测试
node examples/demo.ts        # 运行样例
```

## 运行样例（`node examples/demo.ts`）

```
=== 1. Original run: build the operation log ===
log records: 17, side effects executed: [email-1]
original state digest: 1ca466d5d64848e445b4d210b0fb56e529de6a871900912f7b26159edb71808c

=== 2. Crash & recover: step-by-step replay from the serialized log ===
  applied seq  0 op
  ...
  applied seq  9 effect  # effect email-1 already confirmed: not re-triggered
  ...
  applied seq 15 tx_abort  # transaction tx-temp aborted: buffered ops discarded
  applied seq 16 op
recovered digest:      1ca466d5d64848e445b4d210b0fb56e529de6a871900912f7b26159edb71808c
interpreter digest:    1ca466d5d64848e445b4d210b0fb56e529de6a871900912f7b26159edb71808c
side effects re-executed during replay: [] (acked effects are not re-triggered)
digests equal: true

=== 3. Rollback via inverse patches ===
after 5 reverse steps, cursor=11, keys=["acct:a","acct:b","flag:x","user:1","user:2"]
re-stepped to end, digest matches original: true

=== 4. Checkpoint save / reclaim / restore ===
checkpoints before reclaim: 2, reclaimed: 1
restored checkpoint at seq 16, digest matches: true

=== 5. Corruption is located to its operation seq ===
rejected: log corrupt at seq 8: record hash mismatch

=== 6. Cancel an in-progress recovery ===
keys before cancel: ["user:1","user:2"]
keys after cancel:  []
step after cancel rejected: RecoveryCancelledError
```

## 测试结果（`node --test test/*.test.ts`，Node v23.9.0）

```
✔ full replay: recovery digest equals original state and independent interpreter
✔ every prefix of the recovery matches the independent interpreter (per-failure-point cross-check)
✔ readers never see transaction intermediate state
✔ aborted and uncommitted transactions are discarded
✔ duplicate operation IDs are not replayed twice
✔ missing intermediate operations cannot be skipped
✔ corruption at any record is located to its exact seq (all failure points)
✔ a corrupt log yields no partial success state
✔ checkpoint splice with a foreign prefix digest is rejected
✔ checkpoint record bound to a wrong state digest is rejected during replay
✔ checkpoint restore resumes to an identical final digest; reclamation keeps the newest
✔ rollback applies inverse patches to any prefix and re-stepping reproduces the final digest
✔ cancel aborts recovery, blocks stepping until reset, and preserves baseline
✔ confirmed side effects are never re-triggered during replay
✔ an effect without ack is executed exactly once during recovery
✔ final digest after recovery equals the original state digest
ℹ tests 16  ℹ pass 16  ℹ fail 0
```
