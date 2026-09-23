import { test } from "node:test";
import assert from "node:assert/strict";
import { RefreshGroup } from "../src/coordinator.ts";
import { MemoryEventStore } from "../src/store.ts";
import { initialState, reduce } from "../src/events.ts";

type Payload = string;

const flush = () => new Promise((r) => setImmediate(r));

class Deferred<T> {
  resolve!: (v: T) => void;
  reject!: (e: unknown) => void;
  promise = new Promise<T>((res, rej) => {
    this.resolve = res;
    this.reject = rej;
  });
}

/** 每次调用 fetcher 都生成一个独立的可控 gate，并记录该次调用收到的 signal。 */
function gatedSource() {
  const gates: Array<{
    deferred: Deferred<{ value: Payload; version: number }>;
    signal: AbortSignal | undefined;
  }> = [];
  const fetcher = (signal: AbortSignal) => {
    const gate = { deferred: new Deferred<{ value: Payload; version: number }>(), signal };
    gates.push(gate);
    return gate.deferred.promise;
  };
  return { fetcher, gates, get last() { return gates[gates.length - 1]; } };
}

type Fetcher = (signal: AbortSignal) => Promise<{ value: Payload; version: number }>;

function makeGroup(sources: Record<string, Fetcher>) {
  return new RefreshGroup<Payload>(sources, new MemoryEventStore<Payload>());
}

test("全部源成功：原子发布到同一逻辑版本，过程中读者看不到中间态", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const group = makeGroup({ A: a.fetcher, B: b.fetcher });

  const done = group.refreshAll(1);
  await flush();
  assert.equal(group.getView().version, 0);
  assert.equal(group.getRound(1).outcome, "running");

  a.last.deferred.resolve({ value: "a1", version: 1 });
  await flush();
  // 只有 A 成功，整组仍未发布
  assert.equal(group.getView().version, 0);
  assert.equal(group.getRound(1).outcome, "running");

  b.last.deferred.resolve({ value: "b1", version: 1 });
  const result = await done;

  assert.equal(result.published, true);
  assert.equal(result.version, 1);
  assert.deepEqual(result.failedSources, []);
  assert.equal(group.getView().version, 1);
  assert.deepEqual([...group.getView().sources.entries()], [
    ["A", "a1"],
    ["B", "b1"],
  ]);
});

test("部分失败：保持旧一致视图、报告失败源，A 新 B 旧不能冒充已刷新", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const group = makeGroup({ A: a.fetcher, B: b.fetcher });

  const done = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  b.last.deferred.reject(new Error("B 网络抖动"));
  const result = await done;

  assert.equal(result.published, false);
  assert.equal(result.outcome, "partial");
  assert.deepEqual(result.failedSources, ["B"]);
  assert.equal(group.getView().version, 0);
  assert.equal(group.getView().sources.size, 0);
  assert.equal(group.getRound(1).outcome, "partial");
});

test("单源重试成功后整组只发布一次，视图跳到目标版本", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const group = makeGroup({ A: a.fetcher, B: b.fetcher });

  const first = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  b.last.deferred.reject(new Error("B 失败"));
  await first;

  const retry = group.retrySource(1, "B");
  await flush();
  assert.equal(group.getView().version, 0, "重试完成前视图不变");
  assert.equal(group.getRound(1).outcome, "running");

  b.last.deferred.resolve({ value: "b1", version: 1 });
  const result = await retry;

  assert.equal(result.published, true);
  assert.equal(group.getView().version, 1);
  assert.deepEqual([...group.getView().sources.entries()], [
    ["A", "a1"],
    ["B", "b1"],
  ]);
  const published = group.events().filter((e) => e.type === "RoundPublished");
  assert.equal(published.length, 1, "发布事件有且只有一条");
  assert.equal(b.gates.length, 2, "B 首轮 + 重试各取数一次");
});

test("非法重试被拒绝：已成功的源、并发重复重试、已发布轮次", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const group = makeGroup({ A: a.fetcher, B: b.fetcher });

  const first = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  b.last.deferred.reject(new Error("B 失败"));
  await first;

  assert.throws(() => group.retrySource(1, "A"), /已成功/);

  const retry1 = group.retrySource(1, "B");
  await flush();
  assert.throws(() => group.retrySource(1, "B"), /在途/);
  b.last.deferred.resolve({ value: "b1", version: 1 });
  await retry1;

  assert.throws(() => group.retrySource(1, "B"), /已发布/);
  assert.throws(() => group.retrySource(99, "B"), /不存在/);
});

test("版本不一致按失败处理：源返回旧版本数据不能参与整组发布", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const group = makeGroup({ A: a.fetcher, B: b.fetcher });

  const first = group.refreshAll(2);
  await flush();
  a.last.deferred.resolve({ value: "a2", version: 2 });
  b.last.deferred.resolve({ value: "b1", version: 1 }); // B 给了旧版本
  const result = await first;

  assert.equal(result.published, false);
  assert.deepEqual(result.failedSources, ["B"]);
  assert.equal(group.getView().version, 0);

  const retry = group.retrySource(1, "B");
  await flush();
  b.last.deferred.resolve({ value: "b2", version: 2 });
  await retry;
  assert.equal(group.getView().version, 2);
  assert.deepEqual([...group.getView().sources.entries()], [
    ["A", "a2"],
    ["B", "b2"],
  ]);
});

test("开启新一轮使旧轮过期：旧轮在途请求收到 abort 且迟到结果被丢弃", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const group = makeGroup({ A: a.fetcher, B: b.fetcher });

  const round1 = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  await flush();
  // B 仍在途时开启轮 2
  const round2 = group.refreshAll(2);

  const r1 = await round1;
  assert.equal(r1.outcome, "superseded");
  assert.equal(b.gates[0].signal?.aborted, true, "旧轮在途取数应被 abort");

  // 旧轮的 B 无视 abort，迟到返回版本 1 的结果
  b.gates[0].deferred.resolve({ value: "b1-stale", version: 1 });
  await flush();
  assert.equal(group.getView().version, 0, "旧轮迟到结果绝不能发布");
  assert.equal(group.getRound(1).outcome, "superseded");
  assert.throws(() => group.retrySource(1, "B"), /已过期/);

  // 轮 2 的取数是新的 gates，正常成功后发布版本 2
  a.last.deferred.resolve({ value: "a2", version: 2 });
  b.last.deferred.resolve({ value: "b2", version: 2 });
  const r2 = await round2;
  assert.equal(r2.published, true);
  assert.equal(group.getView().version, 2);
  assert.deepEqual([...group.getView().sources.entries()], [
    ["A", "a2"],
    ["B", "b2"],
  ]);
  // 轮 1 从未产生发布事件
  assert.equal(
    group.events().filter((e) => e.type === "RoundPublished" && e.round === 1).length,
    0,
  );
});

test("无视 abort 的慢源：上一轮迟到的成功结果不能混入下一轮", async () => {
  const slowGates: Array<{
    deferred: Deferred<{ value: Payload; version: number }>;
    signal: AbortSignal;
  }> = [];
  const b = gatedSource();
  const group = makeGroup({
    A: (signal) => {
      const deferred = new Deferred<{ value: Payload; version: number }>();
      slowGates.push({ deferred, signal });
      return deferred.promise;
    },
    B: b.fetcher,
  });

  group.refreshAll(1);
  await flush();
  b.last.deferred.resolve({ value: "b1", version: 1 });
  await flush();

  // B 已成功、A 悬挂时开启轮 2
  const round2 = group.refreshAll(2);
  await flush();
  assert.equal(slowGates.length, 2, "轮 1、轮 2 各调用一次慢源");
  assert.equal(slowGates[0].signal.aborted, true, "轮 1 的在途请求应被 abort");

  // 轮 1 的 A 无视 abort，迟到返回版本 1 的成功结果
  slowGates[0].deferred.resolve({ value: "a1-stale", version: 1 });
  await flush();
  assert.equal(group.getView().version, 0, "旧轮迟到成功必须丢弃，不能发布");
  assert.equal(group.getRound(1).outcome, "superseded");

  // 轮 2：B 成功、A 仍悬挂 → 不发布
  b.last.deferred.resolve({ value: "b2", version: 2 });
  await flush();
  assert.equal(group.getView().version, 0);
  assert.equal(group.getRound(2).outcome, "running");

  // 轮 2 的 A 终于成功 → 才发布版本 2，且数据不含旧轮结果
  slowGates[1].deferred.resolve({ value: "a2", version: 2 });
  const result = await round2;
  assert.equal(result.published, true);
  assert.equal(group.getView().version, 2);
  assert.deepEqual([...group.getView().sources.entries()], [
    ["A", "a2"],
    ["B", "b2"],
  ]);
});

test("多轮连续发布：新版本快照不含任何上一轮数据", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const group = makeGroup({ A: a.fetcher, B: b.fetcher });

  const r1 = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  b.last.deferred.resolve({ value: "b1", version: 1 });
  await r1;

  const r2 = group.refreshAll(2);
  await flush();
  a.last.deferred.resolve({ value: "a2", version: 2 });
  b.last.deferred.resolve({ value: "b2", version: 2 });
  await r2;

  assert.equal(group.getView().version, 2);
  assert.deepEqual([...group.getView().sources.entries()], [
    ["A", "a2"],
    ["B", "b2"],
  ]);
  assert.equal(
    group.events().filter((e) => e.type === "RoundPublished").length,
    2,
  );
});

test("事件日志回放：纯 reducer 重建状态与在线状态逐字段一致", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const store = new MemoryEventStore<Payload>();
  const group = new RefreshGroup<Payload>({ A: a.fetcher, B: b.fetcher }, store);

  const first = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  b.last.deferred.reject(new Error("boom"));
  await first;
  const retry = group.retrySource(1, "B");
  await flush();
  b.last.deferred.resolve({ value: "b1", version: 1 });
  await retry;

  let replay = initialState<Payload>();
  for (const event of store.load()) replay = reduce(replay, event);

  assert.equal(replay.view.version, 1);
  assert.deepEqual([...replay.view.sources.entries()], [...group.getView().sources.entries()]);
  assert.equal(replay.roundSeq, 1);
  assert.equal(replay.currentRound, 1);
  const round = replay.rounds.get(1)!;
  assert.equal(round.published, true);
  assert.equal(round.sources.get("A")!.status, "success");
  assert.equal(round.sources.get("B")!.attempt, 2, "B 有首轮失败 + 重试两次尝试");
});

test("进程重启：用历史事件构造新协调器，视图与轮次状态完整恢复", async () => {
  const a = gatedSource();
  const b = gatedSource();
  const store = new MemoryEventStore<Payload>();
  const group = new RefreshGroup<Payload>({ A: a.fetcher, B: b.fetcher }, store);
  const r1 = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  b.last.deferred.resolve({ value: "b1", version: 1 });
  await r1;

  const restored = new RefreshGroup<Payload>(
    {
      A: async () => ({ value: "unused", version: 99 }),
      B: async () => ({ value: "unused", version: 99 }),
    },
    new MemoryEventStore<Payload>(store.load()),
  );
  assert.equal(restored.getView().version, 1);
  assert.deepEqual([...restored.getView().sources.entries()], [
    ["A", "a1"],
    ["B", "b1"],
  ]);
  assert.equal(restored.getRound(1).outcome, "published");
});

test("reducer 拒绝乱序/重复事件：同一轮重复发布会抛错", () => {
  let s = initialState<Payload>();
  s = reduce(s, { type: "RefreshStarted", round: 1, version: 1, sources: ["A"] });
  s = reduce(s, { type: "SourceAttemptStarted", round: 1, source: "A", attempt: 1 });
  s = reduce(s, { type: "SourceSucceeded", round: 1, source: "A", version: 1, value: "a1" });
  s = reduce(s, { type: "RoundPublished", round: 1, version: 1, values: [["A", "a1"]] });

  assert.throws(
    () => reduce(s, { type: "RoundPublished", round: 1, version: 1, values: [["A", "a1"]] }),
    /已发布过/,
  );
  assert.throws(
    () => reduce(s, { type: "RefreshStarted", round: 3, version: 3, sources: ["A"] }),
    /非法轮次/,
  );
});

test("getView 返回快照副本，读者无法篡改内部一致视图", async () => {
  const a = gatedSource();
  const group = makeGroup({ A: a.fetcher });
  const done = group.refreshAll(1);
  await flush();
  a.last.deferred.resolve({ value: "a1", version: 1 });
  await done;

  const view = group.getView();
  view.sources.set("A", "hacked");
  view.version = 999;
  assert.equal(group.getView().version, 1);
  assert.equal(group.getView().sources.get("A"), "a1");
});
