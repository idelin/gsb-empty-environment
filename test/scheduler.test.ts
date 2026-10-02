import assert from "node:assert/strict";
import test from "node:test";
import {
  QueueFullError,
  TenantScheduler,
  UnknownTenantError,
  type Task,
  type TenantConfig,
} from "../src/scheduler.ts";

function config(overrides: Partial<TenantConfig> = {}): TenantConfig {
  return {
    weight: 1,
    quotaPerWindow: 10_000,
    windowMs: 60_000,
    maxConcurrency: 1_000,
    maxQueue: 10_000,
    ...overrides,
  };
}

function makeTask(id: string, tenantId: string, cost: number, run?: () => Promise<void>): Task {
  return { id, tenantId, cost, run: run ?? (async () => {}) };
}

test("DRR weighted fairness: service is proportional to weights", () => {
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("A", config({ weight: 2 }));
  scheduler.registerTenant("B", config({ weight: 1 }));

  for (let i = 0; i < 60; i++) scheduler.submit(makeTask(`a-${i}`, "A", 100));
  for (let i = 0; i < 30; i++) scheduler.submit(makeTask(`b-${i}`, "B", 100));

  for (let round = 0; round < 10; round++) scheduler.runRound();

  // A gets 200 deficit/round (2 tasks), B gets 100 (1 task).
  assert.equal(scheduler.tenantStats("A").started, 20);
  assert.equal(scheduler.tenantStats("B").started, 10);
});

test("DRR deficit carry-over: oversized task waits until deficit accumulates", () => {
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("A", config({ weight: 1 }));
  scheduler.submit(makeTask("big", "A", 150));

  scheduler.runRound(); // deficit 100 < 150: no dispatch
  assert.equal(scheduler.tenantStats("A").started, 0);
  scheduler.runRound(); // deficit 200: dispatch, leftover 50
  assert.equal(scheduler.tenantStats("A").started, 1);
});

test("DRR deficit resets when a tenant drains its queue", () => {
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("A", config({ weight: 1 }));

  scheduler.submit(makeTask("t1", "A", 50));
  scheduler.runRound(); // deficit 100, dispatch t1, leftover 50
  assert.equal(scheduler.tenantStats("A").started, 1);

  scheduler.runRound(); // queue empty -> deficit reset to 0
  scheduler.submit(makeTask("t2", "A", 150));
  scheduler.runRound(); // deficit 100 < 150: still waiting
  assert.equal(scheduler.tenantStats("A").started, 1);
  scheduler.runRound(); // deficit 200: dispatch
  assert.equal(scheduler.tenantStats("A").started, 2);
});

test("quota caps tasks per window and resets on window rollover", () => {
  let now = 1_000_000;
  const scheduler = new TenantScheduler(100, () => now);
  scheduler.registerTenant("A", config({ quotaPerWindow: 2, windowMs: 1000 }));
  for (let i = 0; i < 5; i++) scheduler.submit(makeTask(`t-${i}`, "A", 1));

  scheduler.runRound();
  scheduler.runRound();
  assert.equal(scheduler.tenantStats("A").started, 2); // hard quota cap
  assert.equal(scheduler.usedQuotaOf("A"), 2);
  assert.equal(scheduler.queuedOf("A"), 3);

  now += 1000; // window rolls over
  scheduler.runRound();
  assert.equal(scheduler.tenantStats("A").started, 4);
  assert.equal(scheduler.usedQuotaOf("A"), 2);

  now += 1000;
  scheduler.runRound();
  assert.equal(scheduler.tenantStats("A").started, 5);
  assert.equal(scheduler.queuedOf("A"), 0);
});

test("quota-blocked tenants do not bank DRR credit", () => {
  let now = 0;
  const scheduler = new TenantScheduler(100, () => now);
  scheduler.registerTenant("A", config({ quotaPerWindow: 1, windowMs: 1000 }));
  scheduler.submit(makeTask("t1", "A", 100));
  scheduler.submit(makeTask("t2", "A", 100));

  scheduler.runRound(); // dispatches t1, then quota blocks
  scheduler.runRound();
  scheduler.runRound();
  assert.equal(scheduler.tenantStats("A").started, 1);

  now += 1000; // new window: deficit must be exactly one quantum, not banked
  scheduler.runRound();
  assert.equal(scheduler.tenantStats("A").started, 2);
});

test("tenant isolation: a flooding tenant is rejected at its own queue cap", () => {
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("noisy", config({ maxQueue: 3 }));
  scheduler.registerTenant("quiet", config({}));

  let rejected = 0;
  for (let i = 0; i < 100; i++) {
    try {
      scheduler.submit(makeTask(`n-${i}`, "noisy", 100));
    } catch (error) {
      assert.ok(error instanceof QueueFullError);
      rejected++;
    }
  }
  assert.equal(rejected, 97);
  assert.equal(scheduler.tenantStats("noisy").rejected, 97);

  // The quiet tenant is completely unaffected.
  for (let i = 0; i < 10; i++) scheduler.submit(makeTask(`q-${i}`, "quiet", 1));
  scheduler.runRound();
  assert.equal(scheduler.tenantStats("quiet").started, 10);
  assert.equal(scheduler.queuedOf("quiet"), 0);
});

test("max concurrency caps simultaneously running tasks per tenant", async () => {
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("A", config({ maxConcurrency: 2 }));

  let maxObserved = 0;
  let running = 0;
  let releaseAll!: () => void;
  const gateOpen = new Promise<void>((resolve) => (releaseAll = resolve));
  for (let i = 0; i < 5; i++) {
    scheduler.submit(
      makeTask(`t-${i}`, "A", 1, () => {
        running++;
        maxObserved = Math.max(maxObserved, running);
        return gateOpen.then(() => {
          running--;
        });
      }),
    );
  }

  scheduler.runRound();
  assert.equal(scheduler.inFlightOf("A"), 2); // capped
  assert.equal(scheduler.queuedOf("A"), 3);
  scheduler.runRound();
  assert.equal(scheduler.inFlightOf("A"), 2); // still capped

  releaseAll();
  await scheduler.drain();
  assert.equal(maxObserved, 2); // never exceeded the cap
  assert.equal(scheduler.tenantStats("A").completed, 5);
});

test("edge cases: unknown tenant, invalid config, invalid cost", () => {
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("A", config());

  assert.throws(() => scheduler.submit(makeTask("x", "ghost", 1)), UnknownTenantError);
  assert.throws(() => scheduler.submit(makeTask("x", "A", 0)), /positive integer/);
  assert.throws(() => scheduler.submit(makeTask("x", "A", -5)), /positive integer/);
  assert.throws(() => scheduler.registerTenant("A", config()), /already registered/);
  assert.throws(() => scheduler.registerTenant("B", config({ weight: 0 })), /weight/);
  assert.throws(() => scheduler.registerTenant("C", config({ weight: -1 })), /weight/);
  assert.throws(() => scheduler.registerTenant("D", config({ quotaPerWindow: 0 })), /quotaPerWindow/);
  assert.throws(() => scheduler.registerTenant("E", config({ maxConcurrency: 0 })), /maxConcurrency/);
  assert.throws(() => scheduler.registerTenant("F", config({ maxQueue: -1 })), /maxQueue/);
  assert.throws(() => scheduler.registerTenant("G", config({ windowMs: 0 })), /windowMs/);
  assert.throws(() => scheduler.tenantStats("ghost"), UnknownTenantError);
});

test("failed tasks are accounted, never leaked", async () => {
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("A", config());
  scheduler.submit(makeTask("ok", "A", 1));
  scheduler.submit(
    makeTask("boom", "A", 1, async () => {
      throw new Error("task exploded");
    }),
  );
  await scheduler.drain();

  const stats = scheduler.tenantStats("A");
  assert.equal(stats.completed, 1);
  assert.equal(stats.failed, 1);
  assert.equal(stats.started, 2);
  assert.equal(scheduler.inFlightOf("A"), 0);
  assert.equal(scheduler.queuedOf("A"), 0);

  const results = scheduler.taskResults();
  const failed = results.find((r) => r.taskId === "boom");
  assert.equal(failed!.outcome, "failed");
  assert.ok(failed!.error instanceof Error);
});

test("concurrency safety: 1500 concurrent submissions, strict no-leak assertions", async () => {
  const scheduler = new TenantScheduler(50);
  const tenants = ["t1", "t2", "t3"];
  const quotas: Record<string, number> = { t1: 600, t2: 600, t3: 600 };
  scheduler.registerTenant("t1", config({ weight: 3, quotaPerWindow: quotas.t1 }));
  scheduler.registerTenant("t2", config({ weight: 2, quotaPerWindow: quotas.t2 }));
  scheduler.registerTenant("t3", config({ weight: 1, quotaPerWindow: quotas.t3 }));

  const executions = new Map<string, number>();
  const submissions: Promise<void>[] = [];
  for (let i = 0; i < 1500; i++) {
    const tenantId = tenants[i % tenants.length];
    const id = `task-${i}`;
    submissions.push(
      Promise.resolve().then(() => {
        scheduler.submit(
          makeTask(id, tenantId, 1 + (i % 5), async () => {
            executions.set(id, (executions.get(id) ?? 0) + 1);
          }),
        );
      }),
    );
  }
  await Promise.all(submissions);
  await scheduler.drain();

  // No task lost, none executed twice, none left behind.
  assert.equal(executions.size, 1500);
  for (const [id, count] of executions) {
    assert.equal(count, 1, `task ${id} executed ${count} times`);
  }
  for (const tenantId of tenants) {
    const stats = scheduler.tenantStats(tenantId);
    assert.equal(stats.submitted, 500);
    assert.equal(stats.started, 500);
    assert.equal(stats.completed, 500);
    assert.equal(stats.failed, 0);
    assert.equal(stats.rejected, 0);
    assert.equal(scheduler.inFlightOf(tenantId), 0);
    assert.equal(scheduler.queuedOf(tenantId), 0);
    // Quota was never exceeded within the (single, long) window.
    assert.ok(stats.started <= quotas[tenantId]);
  }
  assert.equal(scheduler.hasPendingWork(), false);
  assert.equal(scheduler.taskResults().length, 1500);
});

test("empty scheduler drains immediately", async () => {
  const scheduler = new TenantScheduler(100);
  assert.equal(scheduler.runRound(), 0);
  await scheduler.drain();
  assert.equal(scheduler.hasPendingWork(), false);
});
