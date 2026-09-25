import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  StreamPipeline,
  MemoryCheckpointStore,
  FileCheckpointStore,
  TransformError,
  BatchError,
  getAtPath,
  jsonEquals,
  isJsonObject,
  type JsonValue,
  type PipelineEvent,
  type RecordEnvelope,
  type TransformStep,
} from "../src/index.ts";

// ---- 测试用步骤 ------------------------------------------------------------

const validate: TransformStep = {
  name: "validate",
  apply(data, ctx) {
    if (!isJsonObject(data)) ctx.fail([], "record must be an object");
    if (typeof ctx.at(["user", "name"]) !== "string") {
      ctx.fail(["user", "name"], "user.name must be a string");
    }
    const items = ctx.at(["items"]);
    if (!Array.isArray(items)) ctx.fail(["items"], "items must be an array");
    items.forEach((item, i) => {
      const price = isJsonObject(item) ? item.price : undefined;
      if (typeof price !== "number") {
        ctx.fail(["items", i, "price"], `price must be a number, got ${JSON.stringify(price)}`);
      }
    });
    return data;
  },
};

const enrich: TransformStep = {
  name: "enrich",
  apply(data) {
    const record = data as { items: { price: number }[] };
    return { ...record, total: record.items.reduce((s, i) => s + i.price, 0) } as JsonValue;
  },
};

const steps = [validate, enrich];

function rec(id: string, seq: number, data: JsonValue): RecordEnvelope {
  return { id, seq, data };
}

const good = (id: string, seq: number, name: string, prices: number[]): RecordEnvelope =>
  rec(id, seq, { user: { name }, items: prices.map((price) => ({ price })) });

async function collect(
  pipeline: StreamPipeline,
  input: RecordEnvelope[],
): Promise<PipelineEvent[]> {
  const events: PipelineEvent[] = [];
  for await (const event of pipeline.run(input)) events.push(event);
  return events;
}

const recordsOf = (events: PipelineEvent[]) =>
  events.filter((e): e is Extract<PipelineEvent, { type: "record" }> => e.type === "record");
const errorsOf = (events: PipelineEvent[]) =>
  events.filter((e): e is Extract<PipelineEvent, { type: "error" }> => e.type === "error");

// ---- 用例 ------------------------------------------------------------------

test("isolated：输出顺序与输入顺序一致", async () => {
  const pipeline = new StreamPipeline({ steps, mode: "isolated" });
  const input = [good("a", 0, "A", [1]), good("b", 1, "B", [2]), good("c", 2, "C", [3])];
  const events = await collect(pipeline, input);
  assert.deepEqual(recordsOf(events).map((e) => e.id), ["a", "b", "c"]);
  assert.deepEqual(recordsOf(events).map((e) => e.seq), [0, 1, 2]);
});

test("isolated：坏一条不丢后续好记录", async () => {
  const pipeline = new StreamPipeline({ steps, mode: "isolated" });
  const input = [
    good("ok-1", 0, "A", [1]),
    rec("bad", 1, { user: { name: 7 }, items: [] }),
    good("ok-2", 2, "B", [2]),
  ];
  const events = await collect(pipeline, input);
  assert.deepEqual(recordsOf(events).map((e) => e.id), ["ok-1", "ok-2"]);
  assert.equal(errorsOf(events).length, 1);
  assert.equal(pipeline.stats.emitted, 2);
  assert.equal(pipeline.stats.failed, 1);
});

test("错误携带记录 ID、步骤名与具体路径，且路径可取到出错值", async () => {
  const pipeline = new StreamPipeline({ steps, mode: "isolated" });
  const badData = { user: { name: "X" }, items: [{ price: 1 }, { price: "oops" }, { price: 3 }] };
  const events = await collect(pipeline, [rec("rec-9", 0, badData)]);
  const [errEvent] = errorsOf(events);
  assert.ok(errEvent, "expected an error event");
  const err = errEvent.error;
  assert.ok(err instanceof TransformError);
  assert.equal(err.recordId, "rec-9");
  assert.equal(err.step, "validate");
  assert.deepEqual(err.path, ["items", 1, "price"]);
  assert.match(err.message, /\$\.items\[1\]\.price/);
  // 错误来源可定位：用路径从原始记录取回出错值
  assert.equal(getAtPath(badData, err.path), "oops");
});

test("atomic：任一失败不发布任何结果，抛 BatchError", async () => {
  const store = new MemoryCheckpointStore();
  const pipeline = new StreamPipeline({ id: "atomic-1", steps, mode: "atomic", store });
  const input = [
    good("ok-1", 0, "A", [1]),
    rec("bad", 1, { user: { name: null }, items: [] }),
    good("ok-2", 2, "B", [2]),
  ];
  const events: PipelineEvent[] = [];
  await assert.rejects(async () => {
    for await (const event of pipeline.run(input)) events.push(event);
  }, (err: unknown) => {
    assert.ok(err instanceof BatchError);
    assert.equal(err.recordId, "bad");
    assert.equal(err.step, "validate");
    return true;
  });
  assert.equal(recordsOf(events).length, 0, "不得发布任何记录");
  assert.equal(await store.load("atomic-1"), undefined, "不得写入检查点");
});

test("atomic：全部成功才发布，且顺序保持", async () => {
  const pipeline = new StreamPipeline({ steps, mode: "atomic" });
  const input = [good("a", 0, "A", [1, 2]), good("b", 1, "B", [3])];
  const events = await collect(pipeline, input);
  assert.deepEqual(recordsOf(events).map((e) => e.id), ["a", "b"]);
  assert.deepEqual((recordsOf(events)[0].data as { total: number }).total, 3);
});

test("恢复后已产出记录不重复产出", async () => {
  const store = new MemoryCheckpointStore();
  const opts = { id: "resume-1", steps, mode: "isolated" as const, store };
  const input = [good("a", 0, "A", [1]), good("b", 1, "B", [2]), good("c", 2, "C", [3])];

  // 首次运行：消费到第一条 checkpoint 后中断（模拟崩溃）
  const first = new StreamPipeline(opts);
  const firstEvents: PipelineEvent[] = [];
  for await (const event of first.run(input)) {
    firstEvents.push(event);
    if (event.type === "checkpoint") break;
  }
  assert.deepEqual(recordsOf(firstEvents).map((e) => e.id), ["a"]);

  // 恢复：新实例 + 同一 store + 完整输入
  const resumed = new StreamPipeline(opts);
  const resumedEvents = await collect(resumed, input);
  assert.deepEqual(recordsOf(resumedEvents).map((e) => e.id), ["b", "c"], "a 不得重复产出");
  assert.equal(resumed.stats.emitted, 3, "累计产出仍为 3，不重复计数");
});

test("恢复后错误记录不被重复计数", async () => {
  const store = new MemoryCheckpointStore();
  const opts = { id: "resume-2", steps, mode: "isolated" as const, store };
  const bad = rec("bad", 0, { user: { name: 1 }, items: [] });

  const first = new StreamPipeline(opts);
  const firstEvents = await collect(first, [bad]);
  assert.equal(errorsOf(firstEvents).length, 1);
  assert.equal(first.stats.failed, 1);

  // 恢复后重试同一条坏记录：仍失败，但只计一次、不重复发 error 事件
  const resumed = new StreamPipeline(opts);
  const resumedEvents = await collect(resumed, [bad, good("ok", 1, "A", [5])]);
  assert.equal(errorsOf(resumedEvents).length, 0, "已登记的错误不重复产出事件");
  assert.equal(resumed.stats.failed, 1, "错误计数不翻倍");
  assert.deepEqual(recordsOf(resumedEvents).map((e) => e.id), ["ok"]);
});

test("恢复后重试成功的记录从错误台账移除并正常产出", async () => {
  const store = new MemoryCheckpointStore();
  const opts = { id: "resume-3", steps, mode: "isolated" as const, store };
  const bad = rec("flaky", 0, { user: { name: 1 }, items: [] });

  const first = new StreamPipeline(opts);
  await collect(first, [bad]);
  assert.equal(first.stats.failed, 1);

  const fixed = rec("flaky", 0, { user: { name: "ok" }, items: [{ price: 2 }] });
  const resumed = new StreamPipeline(opts);
  const events = await collect(resumed, [fixed]);
  assert.deepEqual(recordsOf(events).map((e) => e.id), ["flaky"]);
  assert.equal(resumed.stats.failed, 0);
  assert.equal(resumed.stats.emitted, 1);
});

test("文件检查点跨进程式恢复：不重复产出", async () => {
  const dir = await mkdtemp(join(tmpdir(), "pipeline-test-"));
  const file = join(dir, "checkpoint.json");
  const opts = { id: "file-1", steps, mode: "isolated" as const };
  const input = [good("a", 0, "A", [1]), good("b", 1, "B", [2])];

  const first = new StreamPipeline({ ...opts, store: new FileCheckpointStore(file) });
  for await (const event of first.run(input)) {
    if (event.type === "checkpoint") break; // 模拟崩溃
  }

  const resumed = new StreamPipeline({ ...opts, store: new FileCheckpointStore(file) });
  const events = await collect(resumed, input);
  assert.deepEqual(recordsOf(events).map((e) => e.id), ["b"]);
});

test("对象键序无关、数组顺序保留", async () => {
  const a: JsonValue = { x: 1, y: [1, 2, { p: "a", q: "b" }] };
  const b: JsonValue = { y: [1, 2, { q: "b", p: "a" }], x: 1 };
  const c: JsonValue = { x: 1, y: [2, 1, { p: "a", q: "b" }] };
  assert.ok(jsonEquals(a, b), "对象键序不影响相等性");
  assert.ok(!jsonEquals(a, c), "数组顺序不同则不相等");

  // 流水线输出保留数组顺序
  const pipeline = new StreamPipeline({ steps, mode: "isolated" });
  const events = await collect(pipeline, [good("a", 0, "A", [3, 1, 2])]);
  const out = recordsOf(events)[0].data as { items: { price: number }[] };
  assert.deepEqual(out.items.map((i) => i.price), [3, 1, 2]);
});

test("checkpoint 事件序号单调、统计一致", async () => {
  const pipeline = new StreamPipeline({ steps, mode: "isolated" });
  const input = [
    good("a", 0, "A", [1]),
    rec("bad", 1, { user: {}, items: [] }),
    good("b", 2, "B", [2]),
  ];
  const events = await collect(pipeline, input);
  const checkpoints = events.filter((e) => e.type === "checkpoint");
  assert.deepEqual(checkpoints.map((c) => c.seq), [0, 1, 2]);
  const last = checkpoints.at(-1)!;
  assert.equal(last.emitted, 2);
  assert.equal(last.failed, 1);
});
