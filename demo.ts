import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { LogStreamPipeline } from "./src/pipeline.ts";
import { FileStorage, type LogRecord, type Storage } from "./src/storage.ts";

class FailAtStorage implements Storage {
  private inner: Storage;
  private failAt: number;
  constructor(inner: Storage, failAt: number) {
    this.inner = inner;
    this.failAt = failAt;
  }
  append(r: LogRecord) {
    if (r.lsn === this.failAt) throw new Error(`disk failure at LSN ${r.lsn}`);
    return this.inner.append(r);
  }
  readAll() {
    return this.inner.readAll();
  }
}

const dir = await mkdtemp(join(tmpdir(), "logpipe-demo-"));
const file = new FileStorage(join(dir, "log.jsonl"));
await file.reset();
const pipeline = new LogStreamPipeline(new FailAtStorage(file, 1250));

pipeline.onPublish = (r) => {
  if (r.lsn <= 3 || r.lsn % 400 === 0 || r.lsn >= 1247) {
    console.log(`  publish  LSN=${r.lsn}  [${r.streamId}] ${r.payload}`);
  }
};

const b500 = pipeline.insertBarrierAt(500);
const b1200 = pipeline.insertBarrierAt(1200);
b500.settled.then(() => console.log(">> barrier@500  resolved（前 500 条已落盘）"));
b1200.settled.then(() => console.log(">> barrier@1200 resolved（前 1200 条已落盘）"));

console.log("== 10 个并发流，每个写入 200 条（共 2000 条）==");
const producers = Array.from({ length: 10 }, (_, s) =>
  (async () => {
    for (let i = 0; i < 200; i++) {
      pipeline.append(`stream-${s}`, `msg-${i}`);
      await Promise.resolve();
    }
  })(),
);
await Promise.all(producers);
await pipeline.whenIdle();

const persisted = await file.readAll();
const stats = pipeline.stats;
console.log("\n== 结果 ==");
console.log(`落盘记录数      : ${persisted.length}（LSN 1..${persisted.at(-1)?.lsn}）`);
console.log(`回滚发生位置    : LSN ${pipeline.lastRollbackLsn}（模拟写盘失败）`);
console.log(`barrier@500     : ${b500.state}`);
console.log(`barrier@1200    : ${b1200.state}`);
console.log(`已发布 LSN 上限 : ${stats.publishedLsn}`);
console.log(`内存缓冲 申请   : ${pipeline.pool.totalAcquired}`);
console.log(`内存缓冲 释放   : ${pipeline.pool.totalReleased}`);
console.log(`内存缓冲 残留   : ${pipeline.pool.outstanding}（守恒校验 ${
  pipeline.pool.totalAcquired === pipeline.pool.totalReleased &&
  pipeline.pool.outstanding === 0
    ? "通过"
    : "失败"
}）`);
console.log(`日志文件        : ${join(dir, "log.jsonl")}`);
