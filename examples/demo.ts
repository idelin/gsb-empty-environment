import {
  CoroutinePool,
  type CoroutineBody,
  type SchedulerEvent,
} from "../src/scheduler.ts";

function format(event: SchedulerEvent): string {
  switch (event.type) {
    case "dispatch":
      return `▶ dispatch  ${event.name} (p${event.priority}) 获得 CPU`;
    case "step":
      return `  step      ${event.name} 执行第 ${event.step} 步`;
    case "yield":
      return `  yield     ${event.name} 主动出让`;
    case "preempt":
      return event.reason === "higher-priority-ready"
        ? `  preempt   ${event.name} (p${event.priority}) 被更高优先级协程抢占`
        : `  preempt   ${event.name} (p${event.priority}) 时间片耗尽，轮转换出`;
    case "complete":
      return `■ complete  ${event.name} (p${event.priority}) 执行完毕`;
  }
}

const pool = new CoroutinePool({
  timeSlice: 2,
  onEvent: (event) => console.log(format(event)),
});

// 验收场景：A 优先级 1，B 优先级 3。
// A 运行期间派生 B；A 下一次 Yield 时 B 立即插队，B 完成后 A 继续。
const taskB: CoroutineBody = function* () {
  yield;
  yield;
};

const taskA: CoroutineBody = function* (ctx) {
  ctx.spawn({ name: "B", priority: 3 }, taskB);
  yield;
  yield;
  yield;
};

// 同优先级协程：演示时间片轮转。
const worker: CoroutineBody = function* () {
  yield;
  yield;
  yield;
};

pool.spawn({ name: "A", priority: 1 }, taskA);
pool.spawn({ name: "W1", priority: 2 }, worker);
pool.spawn({ name: "W2", priority: 2 }, worker);

console.log("=== 协程池调度轨迹 (timeSlice=2) ===");
pool.run();

console.log("\n=== 执行统计 ===");
for (const s of pool.stats()) {
  console.log(`${s.name} (p${s.priority}): ${s.totalSteps} 步, ${s.done ? "已完成" : "未完成"}`);
}
