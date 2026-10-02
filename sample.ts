/**
 * 运行样例：协程 A（优先级 1）执行期间使 B（优先级 3）就绪，
 * A 一次 Yield 后 CPU 立即让给 B；B 执行完毕后 A 继续运行。
 * 同时演示同优先级 C/D 在时间片配额下的轮转。
 */
import { CoroutinePool } from "./scheduler.ts";

const pool = new CoroutinePool({ timeSlice: 2 });

function* a(): Generator<string, void, void> {
  yield "A:step-1";
  pool.spawn("B", 3, b); // B 进入就绪态
  yield "A:yield"; // 主动出让 -> 高优先级 B 立即插队
  yield "A:step-2";
  yield "A:step-3";
}

function* b(): Generator<string, void, void> {
  yield "B:step-1";
  yield "B:step-2";
  yield "B:step-3"; // 超过时间片也不被低优先级打断：无优先级反转
}

function* c(): Generator<string, void, void> {
  yield "C:step-1";
  yield "C:step-2";
  yield "C:step-3";
}

function* d(): Generator<string, void, void> {
  yield "D:step-1";
  yield "D:step-2";
}

pool.spawn("A", 1, a);
pool.spawn("C", 1, c);
pool.spawn("D", 1, d);

const trace = pool.run();

console.log("执行轨迹:");
for (const [i, label] of trace.entries()) {
  console.log(`  ${String(i + 1).padStart(2)}. ${label}`);
}
console.log("\n调度事件:", pool.events.join("  "));
console.log("\n协程统计:");
console.table(pool.stats());
