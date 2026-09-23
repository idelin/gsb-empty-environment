/**
 * 运行样例（无需任何第三方依赖）：
 *   node examples/demo.ts
 *
 * 用两个数据源 A、B 演示：
 *   1) 首轮全部成功 -> 整组原子发布 v1
 *   2) 第二轮 B 失败 -> 视图保持 v1，报告失败源 B
 *   3) 单独重试 B 成功 -> 整组只发布一次 v2
 *   4) 第三轮 A 悬挂期间开启第四轮 -> 旧轮 abort、迟到结果丢弃，绝不污染新视图
 *   5) 重放事件日志 -> 从空状态重建出完全相同的一致视图
 */
import { RefreshGroup, type RefreshResult } from "../src/coordinator.ts";
import { MemoryEventStore } from "../src/store.ts";
import { initialState, reduce, type SourceFetcher } from "../src/events.ts";

type Data = string;

class Gate {
  resolve!: (v: { value: Data; version: number }) => void;
  reject!: (e: Error) => void;
  promise = new Promise<{ value: Data; version: number }>((res, rej) => {
    this.resolve = res;
    this.reject = rej;
  });
}

/** 脚本式数据源：每次调用弹出一个 Gate，主流程手动决定成败。 */
function scriptedSource(name: string) {
  const calls: Gate[] = [];
  const fetcher: SourceFetcher<Data> = () => {
    const gate = new Gate();
    calls.push(gate);
    return gate.promise;
  };
  return {
    name,
    fetcher,
    nth: (i: number) => {
      const gate = calls[i];
      if (!gate) throw new Error(`${name} 的第 ${i + 1} 次取数尚未开始`);
      return gate;
    },
    get latest() {
      return calls[calls.length - 1];
    },
  };
}

const tick = () => new Promise((r) => setImmediate(r));

function printView(label: string, group: RefreshGroup<Data>): void {
  const view = group.getView();
  const entries = [...view.sources.entries()].map(([k, v]) => `${k}=${v}`).join(", ") || "(空)";
  console.log(`  读者视图[${label}] -> 逻辑版本 v${view.version}, 数据 { ${entries} }`);
}

function printResult(result: RefreshResult<Data>): void {
  console.log(
    `  结果: 轮次#${result.round} ${result.outcome}` +
      (result.published ? `，已发布 v${result.version}` : `，失败源=[${result.failedSources.join(", ")}]，未发布`),
  );
}

const store = new MemoryEventStore<Data>();
const sourceA = scriptedSource("A");
const sourceB = scriptedSource("B");
const group = new RefreshGroup<Data>(
  { A: sourceA.fetcher, B: sourceB.fetcher },
  store,
);

async function main(): Promise<void> {
  console.log("== 场景 1：首轮全部成功，整组原子发布 v1 ==");
  const r1 = group.refreshAll(1);
  await tick();
  sourceA.nth(0).resolve({ value: "a1", version: 1 });
  sourceB.nth(0).resolve({ value: "b1", version: 1 });
  printResult(await r1);
  printView("发布后", group);

  console.log("\n== 场景 2：刷新 v2，A 成功但 B 失败 -> 保持旧视图 v1 ==");
  const r2 = group.refreshAll(2);
  await tick();
  sourceA.latest.resolve({ value: "a2", version: 2 });
  sourceB.latest.reject(new Error("B 网络抖动"));
  printResult(await r2);
  printView("部分失败后（A 新 B 旧不能冒充整组已刷新）", group);

  console.log("\n== 场景 3：仅重试失败源 B，成功后整组发布一次 v2 ==");
  const retry = group.retrySource(2, "B");
  await tick();
  printView("重试进行中（读者看不到中间态）", group);
  sourceB.latest.resolve({ value: "b2", version: 2 });
  printResult(await retry);
  printView("重试成功后", group);
  const publishCount = store.load().filter((e) => e.type === "RoundPublished").length;
  console.log(`  日志中 RoundPublished 事件数量 = ${publishCount}（v2 只发布一次）`);

  console.log("\n== 场景 4：刷新 v3 时 A 悬挂，立刻开启 v4 -> 旧轮过期、迟到结果丢弃 ==");
  const r3 = group.refreshAll(3);
  await tick();
  const staleGateA = sourceA.latest;
  sourceB.latest.resolve({ value: "b3", version: 3 });
  await tick();
  const r4 = group.refreshAll(4);
  printResult(await r3);
  console.log("  v3 的 A 迟到返回成功（底层忽略 abort）……");
  staleGateA.resolve({ value: "a3-stale", version: 3 });
  await tick();
  printView("旧轮迟到结果处理后", group);
  sourceA.latest.resolve({ value: "a4", version: 4 });
  sourceB.latest.resolve({ value: "b4", version: 4 });
  printResult(await r4);
  printView("v4 发布后（无任何 v3 数据混入）", group);

  console.log("\n== 场景 5：重放事件日志，从空状态重建 ==");
  let rebuilt = initialState<Data>();
  for (const event of store.load()) rebuilt = reduce(rebuilt, event);
  const entries = [...rebuilt.view.sources.entries()].map(([k, v]) => `${k}=${v}`).join(", ");
  console.log(`  重放 ${store.load().length} 个事件后 -> 逻辑版本 v${rebuilt.view.version}, 数据 { ${entries} }`);
  const online = group.getView();
  const same =
    rebuilt.view.version === online.version &&
    JSON.stringify([...rebuilt.view.sources.entries()]) === JSON.stringify([...online.sources.entries()]);
  console.log(`  重建视图与在线视图一致：${same ? "是" : "否"}`);
  console.log("\n事件序列：");
  for (const [i, e] of store.load().entries()) {
    console.log(`  ${String(i + 1).padStart(2, "0")}. ${e.type} ${JSON.stringify(e).slice(0, 90)}`);
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
