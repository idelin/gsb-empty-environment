import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  StreamPipeline,
  FileCheckpointStore,
  BatchError,
  isJsonObject,
  formatPath,
  type JsonValue,
  type PipelineEvent,
  type RecordEnvelope,
  type TransformStep,
} from "../src/index.ts";

// ---- 变换步骤：校验 -> 规范化 -> 丰富 --------------------------------------

const validate: TransformStep = {
  name: "validate",
  apply(data, ctx) {
    if (!isJsonObject(data)) ctx.fail([], "record must be an object");
    const name = ctx.at(["user", "name"]);
    if (typeof name !== "string" || name.trim() === "") {
      ctx.fail(["user", "name"], `user.name must be a non-empty string, got ${JSON.stringify(name)}`);
    }
    const items = ctx.at(["items"]);
    if (!Array.isArray(items)) ctx.fail(["items"], "items must be an array");
    items.forEach((item, i) => {
      const price = isJsonObject(item) ? item.price : undefined;
      if (typeof price !== "number" || price < 0) {
        ctx.fail(["items", i, "price"], `price must be a non-negative number, got ${JSON.stringify(price)}`);
      }
    });
    return data;
  },
};

const normalize: TransformStep = {
  name: "normalize",
  apply(data) {
    const record = data as { user: { name: string }; items: { price: number }[] };
    return {
      ...record,
      user: { ...record.user, name: record.user.name.trim() },
    } as JsonValue;
  },
};

const enrich: TransformStep = {
  name: "enrich",
  apply(data) {
    const record = data as { items: { price: number }[] };
    const total = record.items.reduce((sum, item) => sum + item.price, 0);
    return { ...record, total } as JsonValue;
  },
};

const steps = [validate, normalize, enrich];

// ---- 输入记录：r3 的 user.name 非法，r5 的 items[1].price 非法 --------------

const records: RecordEnvelope[] = [
  { id: "r1", seq: 0, data: { user: { name: "  Ada " }, items: [{ price: 3 }, { price: 4 }] } },
  { id: "r2", seq: 1, data: { user: { name: "Bob" }, items: [{ price: 10 }] } },
  { id: "r3", seq: 2, data: { user: { name: 42 }, items: [{ price: 1 }] } },
  { id: "r4", seq: 3, data: { user: { name: "Cid" }, items: [] } },
  { id: "r5", seq: 4, data: { user: { name: "Dee" }, items: [{ price: 2 }, { price: "x" }] } },
];

function print(event: PipelineEvent): void {
  if (event.type === "record") {
    console.log(`  record  seq=${event.seq} id=${event.id} ${JSON.stringify(event.data)}`);
  } else if (event.type === "error") {
    const e = event.error;
    console.log(`  error   id=${e.recordId} step=${e.step} path=${formatPath(e.path)} :: ${e.message}`);
  } else {
    console.log(`  checkpoint seq=${event.seq} emitted=${event.emitted} failed=${event.failed}`);
  }
}

async function demoIsolated(): Promise<void> {
  console.log("\n=== 1) isolated 模式：坏记录隔离，好记录继续 ===");
  const pipeline = new StreamPipeline({ id: "demo-isolated", steps, mode: "isolated" });
  for await (const event of pipeline.run(records)) print(event);
  console.log(`  stats: ${JSON.stringify(pipeline.stats)}`);
}

async function demoAtomic(): Promise<void> {
  console.log("\n=== 2) atomic 模式：任一失败，整批不发布 ===");
  const pipeline = new StreamPipeline({ id: "demo-atomic", steps, mode: "atomic" });
  let published = 0;
  try {
    for await (const event of pipeline.run(records)) {
      if (event.type === "record") published += 1;
      print(event);
    }
  } catch (err) {
    if (err instanceof BatchError) {
      console.log(`  BatchError: ${err.message}`);
    } else {
      throw err;
    }
  }
  console.log(`  published records: ${published} (预期 0)`);
}

async function demoResume(): Promise<void> {
  console.log("\n=== 3) 崩溃恢复：checkpoint 不重复产出，错误不重复计数 ===");
  const store = new FileCheckpointStore(join(tmpdir(), "stream-pipeline-demo.checkpoint.json"));
  const opts = { id: "demo-resume", steps, mode: "isolated" as const, store };

  console.log("  -- 首次运行，处理到 seq=2 后模拟崩溃 --");
  const first = new StreamPipeline(opts);
  for await (const event of first.run(records)) {
    print(event);
    if (event.type === "checkpoint" && event.seq >= 2) break; // 模拟进程崩溃
  }
  console.log(`  stats after crash: ${JSON.stringify(first.stats)}`);

  console.log("  -- 恢复运行（新实例、同一 checkpoint 文件、完整输入） --");
  const resumed = new StreamPipeline(opts);
  for await (const event of resumed.run(records)) print(event);
  console.log(`  stats after resume: ${JSON.stringify(resumed.stats)}`);
  console.log("  观察：r1/r2 未重复产出；r3 的错误未重复计数（failed 仍为 2 中的 1 条 r3）。");
}

await demoIsolated();
await demoAtomic();
await demoResume();
