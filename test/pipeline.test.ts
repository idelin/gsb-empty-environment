import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { LogStreamPipeline } from "../src/pipeline.ts";
import { FileStorage, type LogRecord, type Storage } from "../src/storage.ts";

/** 在指定 LSN 注入写盘失败，用于模拟异常回滚。 */
class FailAtStorage implements Storage {
  private readonly inner: Storage;
  private readonly failAtLsn: number;
  private remaining: number;

  constructor(inner: Storage, failAtLsn: number, times = Number.POSITIVE_INFINITY) {
    this.inner = inner;
    this.failAtLsn = failAtLsn;
    this.remaining = times;
  }

  async append(record: LogRecord): Promise<void> {
    if (record.lsn === this.failAtLsn && this.remaining > 0) {
      this.remaining--;
      throw new Error(`injected disk failure at LSN ${record.lsn}`);
    }
    return this.inner.append(record);
  }

  readAll(): Promise<LogRecord[]> {
    return this.inner.readAll();
  }
}

const STREAMS = 10;
const PER_STREAM = 200;
const TOTAL = STREAMS * PER_STREAM; // 2000
const BARRIER_1 = 500;
const BARRIER_2 = 1200;
const FAIL_AT = 1250;

async function makePipeline(failAt?: number, failTimes?: number) {
  const dir = await mkdtemp(join(tmpdir(), "logpipe-"));
  const file = new FileStorage(join(dir, "log.jsonl"));
  await file.reset();
  const storage =
    failAt === undefined ? file : new FailAtStorage(file, failAt, failTimes);
  const pipeline = new LogStreamPipeline(storage);
  return { pipeline, file };
}

/** 10 个并发生产者，每个写 200 条，交错让出事件循环。 */
function runProducers(pipeline: LogStreamPipeline): number[] {
  const assigned: number[] = [];
  const producers: Promise<void>[] = [];
  for (let s = 0; s < STREAMS; s++) {
    const streamId = `stream-${s}`;
    producers.push(
      (async () => {
        for (let i = 0; i < PER_STREAM; i++) {
          const lsn = pipeline.append(streamId, `msg-${streamId}-${i}`);
          assigned.push(lsn);
          await Promise.resolve(); // 让出，模拟并发交错
        }
      })(),
    );
  }
  return Object.assign(assigned, { done: Promise.all(producers) }) as number[] & {
    done: Promise<void[]>;
  };
}

test("验收：10 并发流 × 2000 条，屏障@500/@1200，1250 写盘失败回滚", async () => {
  const { pipeline, file } = await makePipeline(FAIL_AT);

  // 事件日志：验证屏障前日志未落盘前，后续数据不得发布。
  const events: string[] = [];
  const published: number[] = [];
  pipeline.onPublish = (r) => {
    published.push(r.lsn);
    events.push(`publish:${r.lsn}`);
  };

  const barrier1 = pipeline.insertBarrierAt(BARRIER_1);
  const barrier2 = pipeline.insertBarrierAt(BARRIER_2);
  barrier1.settled.then(() => events.push(`barrier:${BARRIER_1}`));
  barrier2.settled.then(() => events.push(`barrier:${BARRIER_2}`));

  const assigned = runProducers(pipeline);
  await assigned.done;
  await pipeline.whenIdle();

  // 1) LSN 单调且唯一：1..2000 全覆盖
  assert.equal(assigned.length, TOTAL);
  assert.deepEqual(
    [...assigned].sort((a, b) => a - b),
    Array.from({ length: TOTAL }, (_, i) => i + 1),
  );

  // 2) 回滚发生且 LSN 空间回退
  assert.equal(pipeline.lastRollbackLsn, FAIL_AT);

  // 3) 屏障 @500、@1200 均已解决（1200 之前日志全量持久）
  assert.equal(barrier1.state, "resolved");
  assert.equal(barrier2.state, "resolved");
  const persisted = await file.readAll();
  assert.equal(persisted.length, FAIL_AT - 1);
  assert.deepEqual(
    persisted.map((r) => r.lsn),
    Array.from({ length: FAIL_AT - 1 }, (_, i) => i + 1),
    "LSN 1..1249 必须按序全量落盘",
  );
  const persistedSet = new Set(persisted.map((r) => r.lsn));
  for (let lsn = 1; lsn <= BARRIER_2; lsn++) {
    assert.ok(persistedSet.has(lsn), `LSN ${lsn} 必须持久`);
  }

  // 4) 屏障语义：LSN>500 的发布必须晚于屏障@500 解决；LSN>1200 同理
  const b1Idx = events.indexOf(`barrier:${BARRIER_1}`);
  const b2Idx = events.indexOf(`barrier:${BARRIER_2}`);
  assert.ok(b1Idx >= 0 && b2Idx >= 0);
  events.forEach((e, idx) => {
    if (!e.startsWith("publish:")) return;
    const lsn = Number(e.slice(8));
    if (lsn > BARRIER_1) assert.ok(idx > b1Idx, `LSN ${lsn} 在屏障@500 解决前被发布`);
    if (lsn > BARRIER_2) assert.ok(idx > b2Idx, `LSN ${lsn} 在屏障@1200 解决前被发布`);
  });
  assert.deepEqual(published, persisted.map((r) => r.lsn), "发布序列必须等于落盘序列");

  // 5) 内存计数守恒：全部缓冲槽释放，无泄漏
  assert.equal(pipeline.pool.totalAcquired, TOTAL);
  assert.equal(pipeline.pool.totalReleased, TOTAL);
  assert.equal(pipeline.pool.outstanding, 0);
});

test("无故障路径：2000 条全部落盘并按序发布", async () => {
  const { pipeline, file } = await makePipeline();
  const published: number[] = [];
  pipeline.onPublish = (r) => published.push(r.lsn);
  pipeline.insertBarrierAt(BARRIER_1);
  pipeline.insertBarrierAt(BARRIER_2);

  const assigned = runProducers(pipeline);
  await assigned.done;
  await pipeline.whenIdle();

  const persisted = await file.readAll();
  assert.equal(persisted.length, TOTAL);
  assert.deepEqual(published, Array.from({ length: TOTAL }, (_, i) => i + 1));
  assert.equal(pipeline.pool.outstanding, 0);
});

test("回滚后恢复写入：LSN 复用且后续日志正常落盘", async () => {
  const { pipeline, file } = await makePipeline(3, 1);
  for (let i = 0; i < 5; i++) pipeline.append("s", `batch1-${i}`);
  await pipeline.whenIdle();
  assert.equal(pipeline.lastRollbackLsn, 3);
  assert.equal((await file.readAll()).map((r) => r.lsn).join(","), "1,2");

  for (let i = 0; i < 3; i++) pipeline.append("s", `batch2-${i}`);
  await pipeline.whenIdle();
  assert.deepEqual(
    (await file.readAll()).map((r) => r.lsn),
    [1, 2, 3, 4, 5],
  );
  assert.equal(pipeline.pool.outstanding, 0);
});

test("越界屏障被拒绝，界内屏障不受影响", async () => {
  const { pipeline } = await makePipeline(5);
  const b1 = pipeline.insertBarrierAt(3);
  const b2 = pipeline.insertBarrierAt(10);
  for (let i = 0; i < 10; i++) pipeline.append("s", `m${i}`);
  await pipeline.whenIdle();
  assert.equal(b1.state, "resolved");
  assert.equal(b2.state, "rejected");
  await assert.rejects(b2.settled, /rollback at LSN 5/);
});
