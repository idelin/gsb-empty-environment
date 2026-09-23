/**
 * 运行样例（可直接：node src/example.ts，Node >= 22.18 原生执行 TS）。
 *
 * 场景：两个客户端对同一条记录做乐观写。
 *  A: 基于已提交版本 0，设置 title="A 的标题"
 *  B: 也基于版本 0（尚未看到任何确认），设置 count +10
 * 服务端随后通知：A 失败（校验不通过），B 成功。
 */

import { OptimisticStore } from "./store.ts";

function line(title: string): void {
  console.log(`\n--- ${title} ---`);
}

const store = new OptimisticStore({ title: "原始", count: 0 });

line("初始已提交状态（读者视角）");
console.log(store.snapshot());

line("A 乐观写：title -> A 的标题（baseVersion=0）");
store.submit("op-A", 0, [{ op: "set", path: ["title"], value: "A 的标题" }]);
console.log("A 自己看到的乐观视图:", { version: store.version, state: store.view() });
console.log("其他读者此时看到:", store.snapshot(), "（未确认，不可见）");

line("B 在 A 之后再写：count +10（它仍只知道已提交版本 0）");
store.submit("op-B", 0, [{ op: "add", path: ["count"], delta: 10 }]);
console.log("乐观视图（A 与 B 的叠加）:", { version: store.version, state: store.view() });

line("服务端先确认 B（B 排在 A 后面，返回阻塞，状态不动）");
console.log(store.confirm("op-B", 0));
console.log("读者看到:", store.snapshot());

line("A 失败：仅撤销 A 自身，并连带推进被它阻塞的 B 提交");
console.log(store.reject("op-A"));
console.log("读者看到（B 的写入完整保留，A 没有覆盖 B）:", store.snapshot());

line("B 的确认迟到：幂等，不产生第二条日志");
console.log(store.confirm("op-B", 0));
console.log("日志条数 = 版本:", store.version);

line("A 的成功响应迟到（旧响应，版本不匹配/操作已拒绝）：忽略，不回退");
console.log(store.confirm("op-A", 0));
console.log("最终状态:", store.snapshot());

line("从已确认日志回放重建（状态可重放）");
const rebuilt = OptimisticStore.hydrate({ title: "原始", count: 0 }, store.log());
console.log(rebuilt.snapshot());
