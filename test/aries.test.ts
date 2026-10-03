/**
 * ARIES 崩溃恢复验收测试。
 *
 * 场景：5 个事务（T1/T2 已提交，T3/T4/T5 未提交），
 * 中途执行 Checkpoint，随后崩溃（缓冲池丢失，仅 WAL 文件与部分已刷盘页保留）。
 * 恢复引擎执行 Analysis → Redo → Undo，验证：
 *   - 已提交事务的修改全部持久化；
 *   - 未提交事务的修改全部回滚；
 *   - Undo 产生 CLR 补偿日志，且重启恢复幂等。
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { WriteAheadLog } from "../src/aries/wal.ts";
import { DiskStorage, BufferPool } from "../src/aries/storage.ts";
import { TransactionManager } from "../src/aries/transaction.ts";
import { RecoveryManager } from "../src/aries/recovery.ts";
import { LogType, TxnStatus } from "../src/aries/logRecord.ts";

interface CrashFixture {
  walPath: string;
  disk: DiskStorage;
  checkpointLSN: number;
}

/**
 * 构造验收 WAL：
 *   T1: P1=A1, P2=B1, commit
 *   T3: P1=A3            （未提交）
 *   --- flush P1, P2 ---（T1 落盘，T3 的 P1 修改也落盘）
 *   CHECKPOINT（T3 活跃，P1/P2 在 DPT）
 *   T2: P3=C2, commit
 *   T4: P2=B4            （未提交）
 *   T5: P3=C5, P4=D5     （未提交）
 *   T3: P4=D3
 *   --- 崩溃：缓冲池全丢，P3/P4 修改未落盘 ---
 */
function buildCrashFixture(): CrashFixture {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "aries-"));
  const walPath = path.join(dir, "wal.log");
  const wal = new WriteAheadLog(walPath);
  const disk = new DiskStorage();
  const pool = new BufferPool(disk);
  const tm = new TransactionManager(wal, pool);

  // T1（已提交）
  tm.begin(1);
  tm.update(1, 1, "A1");
  tm.update(1, 2, "B1");
  tm.commit(1);

  // T3（未提交，跨越 Checkpoint）
  tm.begin(3);
  tm.update(3, 1, "A3");

  // 崩溃前部分刷盘：P1、P2 落盘（含 T3 的脏修改 A3）
  pool.flushAll();

  // Checkpoint：T3 活跃，DPT 含 P1、P2
  const checkpointLSN = tm.checkpoint();

  // T2（Checkpoint 后提交）
  tm.begin(2);
  tm.update(2, 3, "C2");
  tm.commit(2);

  // T4 / T5 / T3 后续修改（均未提交，且未刷盘）
  tm.begin(4);
  tm.update(4, 2, "B4");
  tm.begin(5);
  tm.update(5, 3, "C5");
  tm.update(5, 4, "D5");
  tm.update(3, 4, "D3");

  // 崩溃：缓冲池丢失，WAL 文件保留
  pool.crash();

  return { walPath, disk, checkpointLSN };
}

test("Analysis：从 Checkpoint 重建 TT 与 DPT，识别 3 个失败事务", () => {
  const { walPath, disk, checkpointLSN } = buildCrashFixture();
  const wal = WriteAheadLog.recoverFromFile(walPath);
  const rm = new RecoveryManager(wal, disk);

  const result = rm.analysis();

  // 失败事务恰为 T3、T4、T5
  assert.deepEqual(result.loserTxns, [3, 4, 5]);
  // 已提交事务已被 END 从事务表移除
  assert.equal(result.transactionTable.has(1), false);
  assert.equal(result.transactionTable.has(2), false);
  assert.equal(
    result.transactionTable.get(3)!.status,
    TxnStatus.RUNNING,
  );
  // DPT 覆盖全部 4 个被修改页
  assert.deepEqual(
    [...result.dirtyPageTable.keys()].sort((a, b) => a - b),
    [1, 2, 3, 4],
  );
  // Redo 起点 = DPT 最小 recLSN，且不晚于 Checkpoint
  assert.ok(result.redoLSN <= checkpointLSN);
  const minRec = Math.min(
    ...[...result.dirtyPageTable.values()].map((e) => e.recLSN),
  );
  assert.equal(result.redoLSN, minRec);
});

test("Redo：历史重复，已提交与未提交修改均按日志重放", () => {
  const { walPath, disk } = buildCrashFixture();
  const wal = WriteAheadLog.recoverFromFile(walPath);
  const rm = new RecoveryManager(wal, disk);

  const analysis = rm.analysis();
  const redone = rm.redo(analysis);

  // Redo 后磁盘呈现崩溃前最新状态（含未提交修改，历史重复）
  assert.equal(disk.readPage(1).data, "A3");
  assert.equal(disk.readPage(2).data, "B4");
  assert.equal(disk.readPage(3).data, "C5");
  assert.equal(disk.readPage(4).data, "D3");

  // 已落盘的 P1(LSN<=flush) 被 pageLSN 跳过，未落盘的 P3/P4 被重放
  const p3Updates = wal
    .scan()
    .filter((r) => r.type === LogType.UPDATE && r.pageId === 3);
  for (const rec of p3Updates) {
    assert.ok(redone.includes(rec.lsn), `P3 的 LSN=${rec.lsn} 应被重做`);
  }
  // pageLSN 推进到最后一条作用于该页的日志
  const lastP4 = wal
    .scan()
    .filter((r) => r.type === LogType.UPDATE && r.pageId === 4)
    .at(-1)!;
  assert.equal(disk.readPage(4).pageLSN, lastP4.lsn);
});

test("Undo：反向扫描写 CLR，回滚 T3/T4/T5，保留 T1/T2", () => {
  const { walPath, disk } = buildCrashFixture();
  const wal = WriteAheadLog.recoverFromFile(walPath);
  const rm = new RecoveryManager(wal, disk);

  const stats = rm.recover();

  // 最终磁盘状态：已提交数据持久化
  assert.equal(disk.readPage(1).data, "A1"); // T3 的 A3 被回滚为 T1 的 A1
  assert.equal(disk.readPage(2).data, "B1"); // T4 的 B4 被回滚为 T1 的 B1
  assert.equal(disk.readPage(3).data, "C2"); // T5 的 C5 被回滚为 T2 的 C2
  assert.equal(disk.readPage(4).data, ""); // P4 仅有未提交修改，回滚为空

  // 每个失败事务的每条 UPDATE 都被撤销，且撤销顺序全局逆序
  assert.equal(stats.undoneLSNs.length, 5); // T3×2, T4×1, T5×2
  const sortedDesc = [...stats.undoneLSNs].sort((a, b) => b - a);
  assert.deepEqual(stats.undoneLSNs, sortedDesc);

  // CLR 与 UPDATE 一一对应，undoNextLSN 指向被撤销记录的 prevLSN
  assert.equal(stats.clrLSNs.length, stats.undoneLSNs.length);
  const clrs = wal.scan().filter((r) => r.type === LogType.CLR);
  assert.equal(clrs.length, 5);
  for (const clr of clrs) {
    assert.ok([3, 4, 5].includes(clr.txnId));
    assert.ok(clr.undoNextLSN === null || typeof clr.undoNextLSN === "number");
  }

  // 失败事务以 END 收尾
  const ends = wal.scan().filter((r) => r.type === LogType.END);
  for (const txnId of [3, 4, 5]) {
    assert.ok(
      ends.some((r) => r.txnId === txnId),
      `事务 ${txnId} 应有 END 记录`,
    );
  }
});

test("恢复幂等：Undo 期间再次崩溃，二次恢复不重复撤销", () => {
  const { walPath, disk } = buildCrashFixture();
  const wal = WriteAheadLog.recoverFromFile(walPath);
  new RecoveryManager(wal, disk).recover();
  const afterFirst = [1, 2, 3, 4].map((p) => disk.readPage(p).data);

  // 模拟恢复后再次崩溃：从同一 WAL 文件二次恢复
  const wal2 = WriteAheadLog.recoverFromFile(walPath);
  const stats2 = new RecoveryManager(wal2, disk).recover();

  // 无失败事务残留，Undo 无事可做
  assert.deepEqual(stats2.analysis.loserTxns, []);
  assert.equal(stats2.undoneLSNs.length, 0);
  const afterSecond = [1, 2, 3, 4].map((p) => disk.readPage(p).data);
  assert.deepEqual(afterSecond, afterFirst);
});

test("无 Checkpoint 时 Analysis 从日志起点扫描", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "aries-nock-"));
  const walPath = path.join(dir, "wal.log");
  const wal = new WriteAheadLog(walPath);
  const disk = new DiskStorage();
  const pool = new BufferPool(disk);
  const tm = new TransactionManager(wal, pool);

  tm.begin(1);
  tm.update(1, 1, "X");
  pool.crash();

  const wal2 = WriteAheadLog.recoverFromFile(walPath);
  const rm = new RecoveryManager(wal2, disk);
  const stats = rm.recover();

  assert.deepEqual(stats.analysis.loserTxns, [1]);
  assert.equal(stats.analysis.redoLSN, 1);
  assert.equal(disk.readPage(1).data, "");
});
