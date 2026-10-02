# aries-wal

基于 WAL 的单机事务持久化与恢复管理器（TypeScript / Node.js，零依赖），实现 ARIES 恢复算法。

## 设计

```
src/
  types.ts    日志记录类型：BEGIN / UPDATE / CLR / COMMIT / ABORT / END / CHECKPOINT
  wal.ts      预写日志：append 同步写入 OS，flush 负责 fsync；LSN 单调递增
  storage.ts  磁盘页存储（页镜像 = 数据 + pageLSN，原子替换写入）
  buffer.ts   缓冲池：STEAL + NO-FORCE；刷脏页前先 flush 对应日志（WAL 协议）
  database.ts 事务管理器 + ARIES 三阶段恢复
```

**正常运行**

- 每个写操作**先记日志（前/后镜像），后改内存页**；
- 提交时写 `COMMIT` 并 `fsync` 日志（NO-FORCE：不强制刷页）；
- 脏页允许未提交即落盘（STEAL），但刷页前必须先把 `pageLSN` 之前的日志 `fsync`；
- 支持模糊检查点：`CHECKPOINT` 记录携带事务表与脏页表（DPT）快照。

**崩溃恢复（`Database.recover(dir)`）**

1. **Analysis**：从最近的 `CHECKPOINT` 起扫描 WAL，重建事务表与 DPT，区分 winner / loser；
2. **Redo**：从 DPT 最小 `recLSN` 起正向重放所有 `UPDATE`/`CLR`（重复历史，含未提交事务），
   仅当记录在 DPT 覆盖范围内且页上 `pageLSN < lsn` 时才应用；
3. **Undo**：对所有 loser 事务做**全局逆序**回滚（反复选取各链最大 LSN），
   每撤销一条写 `CLR`（`undoNextLSN` 指向下一条待撤销记录，崩溃于撤销中途也能幂等续作），
   链走到头写 `END`。

## 使用

```ts
import { Database } from './src/database.ts';

const db = Database.open('./data');
const tx = db.begin();
db.update(tx, 'accounts', 'A', 1000);
db.commit(tx);
db.crash();                          // 模拟突然断电

const { db: db2, report } = Database.recover('./data');
console.log(db2.read('accounts', 'A')); // 1000
```

## 运行样例

`npm run demo`：worker 进程执行两批共 200 笔转账，批次 2 进行到第 40 笔时被
`SIGKILL`（模拟断电，且未提交脏页已 STEAL 落盘）；重启后执行 ARIES 恢复并校验账目：

```
=== 阶段 3：重启，执行 ARIES 恢复 ===
  Analysis：checkpointLSN=0, winners=[], losers=[3], DPT={"accounts":2}
  Redo    ：从 LSN=2 起重放 38 条记录
  Undo    ：回滚 loser 事务 [3]，写入 80 条 CLR
=== 阶段 4：校验恢复结果 ===
  恢复后余额：A=900, B=1100
  期望余额  ：A=900, B=1100（批次 1 提交点）
  ✅ 恢复成功：已提交数据完整，未提交数据已回滚，账目平衡
```

## 测试

要求 Node.js ≥ 22.6（直接运行 TypeScript + 内置 `node:test`）。

```
npm test
```

测试结果（9/9 通过）：

```
✔ 已提交事务在崩溃后通过 Redo 恢复（NO-FORCE：提交不刷页）
✔ 未提交事务在崩溃后被 Undo 回滚
✔ 批量更新中途断电：已提交批次保留，进行中批次完整回滚
✔ STEAL：未提交脏页被刷盘后，恢复时 Undo 清理磁盘上的脏数据
✔ 主动 abort 回滚全部修改
✔ 检查点之后的崩溃恢复（Analysis 从 CHECKPOINT 起步）
✔ 恢复幂等：恢复过程中再次崩溃，二次恢复结果一致（CLR/undoNextLSN）
✔ 新增键的 Undo 会删除该键（前镜像不存在）
✔ 多个 loser 事务交错写入，全部正确回滚
ℹ tests 9  ℹ pass 9  ℹ fail 0
```

## 说明与限制

- 单机、单文件日志；页存储为整库 JSON 快照（接口语义等同按页存取）；
- 无并发控制：运行期 `abort` 假定不存在并发事务对同一键的交错写（恢复期的
  Undo 已实现标准的全局逆序，不受此限）；
- 检查点为单条 `CHECKPOINT` 记录（等价于 begin/end checkpoint 合并的模糊检查点）。
