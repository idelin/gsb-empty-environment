/**
 * Runnable demo: dependency-ordered teardown, a failing release, a late
 * acquisition, child-scope borrowing, event subscription, journal replay
 * and the exactly-once ownership ledger.
 *
 *   npm run demo
 */
import { Ledger, Scheduler, Scope, ScopeCloseError } from "../src/index.ts";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const scheduler = new Scheduler({ auto: true });
const ledger = new Ledger();
const root = new Scope({ id: "root", scheduler, ledger });

const releaseLog: string[] = [];
root.subscribe((e) => {
  if (e.op.type === "status") {
    console.log(`[event] ${e.op.id} -> ${e.op.status}`);
  }
});

const db = root.acquire({
  name: "db",
  acquire: async () => ({ query: (sql: string) => `rows(${sql})` }),
  release: async () => {
    releaseLog.push("db");
  },
});
const cache = root.acquire({
  name: "cache",
  deps: [db],
  acquire: async () => ({ get: () => "cached" }),
  release: async () => {
    releaseLog.push("cache");
  },
});
root.acquire({
  name: "flaky-metrics",
  deps: [cache],
  acquire: async () => ({}),
  release: async () => {
    releaseLog.push("flaky-metrics");
    throw new Error("metrics sink unreachable");
  },
});

// Child scope borrows the parent's db; closing the child must not release it.
const worker = root.fork("worker");
worker.acquire({
  name: "worker-pool",
  acquire: async () => ({}),
  release: async () => {
    releaseLog.push("worker-pool");
  },
});
const borrowedDb = await worker.borrow(db);
console.log("[borrow] worker borrowed:", borrowedDb.query("select 1"));
await worker.close();
console.log("[borrow] after worker.close(), db status:", db.status);

// Late acquisition: completes only after root.close() has begun.
const slow = root.acquire({
  name: "slow-config",
  acquire: async () => {
    await sleep(80);
    return { loaded: true };
  },
  release: async () => {
    releaseLog.push("slow-config(late-return)");
  },
});

console.log("\n--- closing root ---");
try {
  await root.close();
  console.log("closed cleanly");
} catch (err) {
  if (err instanceof ScopeCloseError) {
    console.log(`closed with aggregate: ${err.message}`);
    for (const inner of err.errors) {
      console.log(`  - ${inner instanceof Error ? inner.message : inner}`);
    }
  }
}
await slow.value.catch((e: Error) =>
  console.log(`[late] slow-config waiter rejected: ${e.name}`),
);

console.log("\nrelease order:", releaseLog.join(" -> "));
console.log("replay matches snapshot:",
  JSON.stringify(root.store.replay()) === JSON.stringify(root.store.snapshot()));

ledger.assertExactOnce();
console.log("ledger: every resource released exactly once ✔");
console.log("\nledger books:");
for (const [id, entry] of Object.entries(ledger.snapshot())) {
  console.log(`  ${id}: ${entry.events.join(", ")}`);
}
