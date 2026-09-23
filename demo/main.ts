/**
 * 运行样例：节点订阅 + 异步资源归属作用域。
 * 演示：依赖逆序清理、子作用域借用、迟到取得归还、清理失败聚合、
 *       环检测、重复销毁幂等、独立账本审计与日志重放。
 *
 * 运行：node --no-warnings demo/main.ts
 */
import {
  ManualScheduler,
  OwnershipLedger,
  Scope,
  DependencyCycleError,
} from "../src/index.ts";

const log = (s: string) => console.log(s);

const scheduler = new ManualScheduler();
const ledger = new OwnershipLedger();
const root = new Scope({ scheduler, ledger, id: "root" });

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// 1) 父作用域取得三个有依赖关系的资源。
const configH = root.acquire("config", async () => ({ name: "config.json" }), {
  dispose: (v) => log(`  release config  (${v.name})`),
});
const dbH = root.acquire("db", async () => {
  await delay(5);
  return { conn: "postgres://..." };
}, {
  deps: ["config"],
  dispose: (v) => log(`  release db      (${v.conn})`),
});
const httpH = root.acquire("http", async () => ({ server: {} }), {
  deps: ["db"],
  dispose: () => {
    log("  release http    (故意抛错: port busy)");
    throw new Error("port busy");
  },
});
await Promise.all([configH.promise, dbH.promise, httpH.promise]);

// 2) 子作用域借用父的 db，并取得自己的 worker。
const child = root.createChild("child");
const lease = child.borrow<{ conn: string }>("db");
log(`child 借到 db: ${lease.value.conn}`);

const workerH = child.acquire("worker", async () => ({ pid: 4242 }), {
  dispose: (v) => log(`  release worker  (pid=${v.pid})`),
});
await workerH.promise;
lease.release();

// 3) 一个在途 acquire：工厂在作用域关闭后才完成（迟到取得）。
root.acquire("late", async () => {
  await delay(20);
  return { late: "resource" };
}, {
  dispose: (v) => log(`  迟到取得立即归还 (${v.late})`),
});
const lateHandle = root.acquire("late-visible", async () => {
  await delay(25);
  return { late: "resource-2" };
}, {
  dispose: (v) => log(`  迟到取得立即归还 (${v.late})`),
});
lateHandle.promise.catch((e) => log(`  late-visible 的用户收到: ${e.name}`));

// 4) 依赖环（用于展示确定性聚合）。
root.acquire("cyc-a", async () => ({}), { dispose: () => log("  release cyc-a") });
root.acquire("cyc-b", async () => ({}), { dispose: () => log("  release cyc-b") });
await scheduler.drain();
root.link("cyc-a", "cyc-b");
root.link("cyc-b", "cyc-a");

log("\n== 开始销毁 root（第一次）==");
const p1 = root.destroy();
const p2 = root.destroy(); // 重复销毁：幂等
log(`双重销毁返回同一 Promise: ${p1 === p2}`);

let error: AggregateError | null = null;
try {
  await p1;
} catch (e) {
  error = e as AggregateError;
}

log(`\n== 销毁完成，聚合到 ${error?.errors.length ?? 0} 个错误：==`);
for (const e of error?.errors ?? []) {
  if (e instanceof DependencyCycleError) {
    log(`  - ${e.name}: ${e.message}`);
  } else {
    log(`  - ${e.name}: ${e.message}`);
  }
}

log("\n== 独立所有权账本审计 ==");
try {
  ledger.audit();
  log(`  OK：${ledger.size} 条资源，每条最终恰好释放一次，无泄漏。`);
} catch (e) {
  log(`  FAIL: ${(e as Error).message}`);
}

log("\n== 日志可回放性校验 ==");
log(`  root 重放一致: ${root.journal.verifyReplay()}`);
log(`  child 重放一致: ${child.journal.verifyReplay()}`);

log("\n== 销毁后操作被拒绝 ==");
try {
  root.acquire("x", async () => ({}));
} catch (e) {
  log(`  acquire -> ${(e as Error).name}`);
}
