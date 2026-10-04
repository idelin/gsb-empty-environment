import { test } from "node:test";
import assert from "node:assert/strict";
import { attachRecovery, buildCrashScenario } from "../src/scenario.ts";
import { LogRecordType } from "../src/log-record.ts";
import { Mutex } from "../src/mutex.ts";
import { attach, makeTempDir, pageValue } from "./helpers.ts";
import { DiskStorage } from "../src/storage.ts";

test("并发: N 个并发 recover() 被互斥串行化, 结果与单次执行一致", async () => {
  const dir = makeTempDir();
  const scenario = await buildCrashScenario(dir);
  const { engine, wal } = await attachRecovery(scenario);

  const N = 8;
  const reports = await Promise.all(
    Array.from({ length: N }, () => engine.recover()),
  );

  // 全部 N 个调用都成功返回
  assert.equal(reports.length, N);
  // 补偿工作总量恰好等于单次恢复: 3 个 CLR + 3 个 END, 不重复
  const totalClr = reports.reduce((s, r) => s + r.undo.clrWritten, 0);
  const totalEnd = reports.reduce((s, r) => s + r.undo.endWritten, 0);
  assert.equal(totalClr, 3, "exactly one recovery performs compensation");
  assert.equal(totalEnd, 3);
  // 其余 N-1 次为完全无操作
  const noops = reports.filter(
    (r) => r.undo.clrWritten === 0 && r.redo.applied === 0,
  );
  assert.equal(noops.length, N - 1);

  // 最终状态正确
  const disk = new DiskStorage(scenario.dbPath);
  for (const [pageId, value] of Object.entries(scenario.expected)) {
    assert.equal(pageValue(disk, pageId), value);
  }

  // 严格无泄漏: 锁完全释放, 无等待者, 无残留事务
  assert.equal(engine.lock.isLocked, false);
  assert.equal(engine.lock.pending, 0);
  assert.equal(wal.appendLock.isLocked, false);
  assert.equal(wal.appendLock.pending, 0);
  assert.equal(engine.analysis(wal.allRecords()).txnTable.size, 0);
});

test("并发: 恢复期间交错发起, 不产生重复 CLR 或重复 END", async () => {
  const dir = makeTempDir();
  const scenario = await buildCrashScenario(dir);
  const { engine, wal } = await attachRecovery(scenario);

  // 交错启动: 第一个 recover 进行中时再发起第二个
  const first = engine.recover();
  const second = engine.recover();
  const third = engine.recover();
  await Promise.all([first, second, third]);

  const log = wal.allRecords();
  const clrs = log.filter((r) => r.type === LogRecordType.CLR);
  const ends = log.filter((r) => r.type === LogRecordType.END);
  // 崩溃前 T5 已有 1 个 CLR; 恢复恰好新增 3 个
  assert.equal(clrs.length, 4);
  // END: T0/T1/T2 各 1 (崩溃前) + 3 个 loser 各 1 (恢复写入)
  assert.equal(ends.length, 6);
  assert.equal(engine.lock.isLocked, false);
  assert.equal(engine.lock.pending, 0);
});

test("并发: WAL append 互斥, LSN 严格单调且无空洞", async () => {
  const dir = makeTempDir();
  const h = attach(dir);

  const M = 64;
  await Promise.all(
    Array.from({ length: M }, (_, i) =>
      h.wal.append({
        type: LogRecordType.BEGIN,
        txnId: i + 1,
        prevLSN: 0,
      }),
    ),
  );

  const lsns = h.wal.allRecords().map((r) => r.lsn).sort((a, b) => a - b);
  assert.deepEqual(lsns, Array.from({ length: M }, (_, i) => i + 1));
  assert.equal(h.wal.appendLock.isLocked, false);
  assert.equal(h.wal.appendLock.pending, 0);
});

test("并发: Mutex 在异常路径下仍释放锁 (无泄漏)", async () => {
  const mutex = new Mutex();
  await assert.rejects(
    mutex.runExclusive(async () => {
      throw new Error("boom");
    }),
    /boom/,
  );
  assert.equal(mutex.isLocked, false);
  assert.equal(mutex.pending, 0);

  // 异常后锁仍可正常获取
  const value = await mutex.runExclusive(() => 42);
  assert.equal(value, 42);
  assert.equal(mutex.isLocked, false);
});

test("并发: Mutex 严格 FIFO 串行化临界区", async () => {
  const mutex = new Mutex();
  const order: number[] = [];
  let inside = 0;
  let maxInside = 0;

  await Promise.all(
    Array.from({ length: 16 }, (_, i) =>
      mutex.runExclusive(async () => {
        inside += 1;
        maxInside = Math.max(maxInside, inside);
        await new Promise((r) => setTimeout(r, 1));
        order.push(i);
        inside -= 1;
      }),
    ),
  );

  assert.equal(maxInside, 1, "critical section never overlaps");
  assert.equal(order.length, 16);
  assert.equal(mutex.isLocked, false);
  assert.equal(mutex.pending, 0);
});
