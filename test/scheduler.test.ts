import { test } from "node:test";
import assert from "node:assert/strict";
import { TenantScheduler, SchedulerError } from "../src/scheduler/scheduler.ts";

const tick = () => new Promise<void>((r) => setTimeout(r, 0));

function makeScheduler(options: ConstructorParameters<typeof TenantScheduler>[0] = {}) {
  return new TenantScheduler(options);
}

/**
 * Submit `jobs` while a blocker holds the single global slot, so every job
 * is queued before any dispatch happens; then release the blocker and record
 * the dispatch order produced purely by the DRR policy.
 */
async function dispatchOrderUnderContention(
  tenants: Array<{ id: string; weight: number }>,
  jobs: Array<{ tenant: string; cost?: number }>,
): Promise<string[]> {
  const sched = makeScheduler({ maxConcurrentTotal: 1 });
  for (const t of tenants) sched.registerTenant(t.id, { weight: t.weight });

  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  const blocker = sched.submit(tenants[0].id, { run: () => gate });

  const order: string[] = [];
  const promises = jobs.map((j) =>
    sched.submit(j.tenant, { cost: j.cost, run: () => void order.push(j.tenant) }),
  );
  release();
  await blocker;
  await Promise.all(promises);
  await sched.drain();
  assert.ok(sched.idle);
  return order;
}

test("DRR fairness: dispatch share matches weight ratio 2:1", async () => {
  const jobs: Array<{ tenant: string }> = [];
  for (let i = 0; i < 60; i++) jobs.push({ tenant: "heavy" });
  for (let i = 0; i < 30; i++) jobs.push({ tenant: "light" });
  const order = await dispatchOrderUnderContention(
    [
      { id: "heavy", weight: 2 },
      { id: "light", weight: 1 },
    ],
    jobs,
  );
  assert.equal(order.length, 90);
  // Every full DRR cycle of 3 dispatches serves heavy twice and light once.
  for (let i = 0; i + 3 <= order.length; i += 3) {
    const window = order.slice(i, i + 3);
    assert.equal(window.filter((t) => t === "heavy").length, 2, `window at ${i}: ${window}`);
    assert.equal(window.filter((t) => t === "light").length, 1, `window at ${i}: ${window}`);
  }
});

test("DRR fairness: equal weights with unequal job costs stay cost-proportional", async () => {
  const jobs: Array<{ tenant: string; cost: number }> = [];
  for (let i = 0; i < 20; i++) jobs.push({ tenant: "a", cost: 3 });
  for (let i = 0; i < 60; i++) jobs.push({ tenant: "b", cost: 1 });
  const order = await dispatchOrderUnderContention(
    [
      { id: "a", weight: 1 },
      { id: "b", weight: 1 },
    ],
    jobs,
  );
  assert.equal(order.length, 80);
  // DRR guarantee: cumulative *cost* handed to each tenant never drifts
  // apart by more than a small constant bound (quantum + max job cost).
  let costA = 0;
  let costB = 0;
  for (const tenant of order) {
    if (tenant === "a") costA += 3;
    else costB += 1;
    assert.ok(Math.abs(costA - costB) <= 5, `drift ${costA - costB} after dispatching ${tenant}`);
  }
  assert.equal(costA, 60);
  assert.equal(costB, 60);
});

test("DRR fairness: idle tenant does not accumulate credit (no burst on return)", async () => {
  const sched = makeScheduler({ maxConcurrentTotal: 1 });
  sched.registerTenant("steady", { weight: 1 });
  sched.registerTenant("bursty", { weight: 1 });

  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  const blocker = sched.submit("steady", { run: () => gate });

  const steadyJobs: Promise<unknown>[] = [];
  for (let i = 0; i < 10; i++) steadyJobs.push(sched.submit("steady", { run: () => "s" }));
  release();
  await blocker;
  await Promise.all(steadyJobs);

  // "bursty" arrives after steady drained; it must not find a huge
  // pre-existing deficit on either tenant that would distort service.
  const order: string[] = [];
  const more: Promise<unknown>[] = [];
  for (let i = 0; i < 6; i++) {
    more.push(sched.submit("steady", { run: () => void order.push("s") }));
    more.push(sched.submit("bursty", { run: () => void order.push("b") }));
  }
  await Promise.all(more);
  await sched.drain();
  // With equal weights and both backlogged, service alternates 1:1.
  const sRun = order.join("");
  assert.match(sRun, /^(sb|bs)+$/, `expected strict alternation, got ${sRun}`);
});

test("isolation: per-tenant concurrency quota is never exceeded", async () => {
  const sched = makeScheduler();
  sched.registerTenant("capped", { maxConcurrent: 3 });
  sched.registerTenant("free", {});

  let current = 0;
  let peak = 0;
  const jobs: Promise<unknown>[] = [];
  for (let i = 0; i < 50; i++) {
    jobs.push(
      sched.submit("capped", {
        run: async () => {
          current += 1;
          peak = Math.max(peak, current);
          await tick();
          current -= 1;
        },
      }),
    );
    jobs.push(sched.submit("free", { run: async () => tick() }));
  }
  await Promise.all(jobs);
  await sched.drain();
  assert.ok(peak <= 3, `peak concurrency ${peak} exceeded quota 3`);
  assert.equal(peak, 3, "quota should be fully utilized under load");
  assert.equal(current, 0, "in-flight counter leaked");
});

test("isolation: global concurrency cap is respected across tenants", async () => {
  const sched = makeScheduler({ maxConcurrentTotal: 2 });
  sched.registerTenant("t1", { weight: 1 });
  sched.registerTenant("t2", { weight: 1 });
  sched.registerTenant("t3", { weight: 1 });

  let current = 0;
  let peak = 0;
  const jobs: Promise<unknown>[] = [];
  for (const id of ["t1", "t2", "t3"]) {
    for (let i = 0; i < 20; i++) {
      jobs.push(
        sched.submit(id, {
          run: async () => {
            current += 1;
            peak = Math.max(peak, current);
            await tick();
            current -= 1;
          },
        }),
      );
    }
  }
  await Promise.all(jobs);
  await sched.drain();
  assert.ok(peak <= 2, `global peak ${peak} exceeded cap 2`);
  assert.equal(current, 0);
});

test("isolation: a flooding tenant cannot starve a quiet tenant", async () => {
  const sched = makeScheduler({ maxConcurrentTotal: 4 });
  sched.registerTenant("flood", { weight: 1, maxConcurrent: 3 });
  sched.registerTenant("quiet", { weight: 1, maxConcurrent: 1 });

  const slow: Promise<unknown>[] = [];
  for (let i = 0; i < 30; i++) {
    slow.push(sched.submit("flood", { run: async () => tick() }));
  }
  const started = Date.now();
  await sched.submit("quiet", { run: () => "done" });
  const elapsed = Date.now() - started;
  await Promise.all(slow);
  await sched.drain();
  assert.ok(elapsed < 500, `quiet tenant waited ${elapsed}ms — starvation suspected`);
});

test("quota: bounded backlog rejects overflow with queue-full", async () => {
  const sched = makeScheduler();
  sched.registerTenant("tiny", { maxQueue: 1, maxConcurrent: 1 });

  const blocker = sched.submit("tiny", { run: async () => tick() });
  const queued = sched.submit("tiny", { run: () => 1 });
  await assert.rejects(sched.submit("tiny", { run: () => 2 }), (e: unknown) => {
    assert.ok(e instanceof SchedulerError);
    assert.equal(e.code, "queue-full");
    return true;
  });
  await Promise.all([blocker, queued]);
  await sched.drain();
});

test("quota: unknown tenant is rejected", async () => {
  const sched = makeScheduler();
  await assert.rejects(sched.submit("ghost", { run: () => 1 }), (e: unknown) => {
    assert.ok(e instanceof SchedulerError);
    assert.equal(e.code, "unknown-tenant");
    return true;
  });
});

test("no leak: failing tasks release their permits", async () => {
  const sched = makeScheduler();
  sched.registerTenant("flaky", { maxConcurrent: 2 });

  const jobs: Promise<unknown>[] = [];
  for (let i = 0; i < 20; i++) {
    const p =
      i % 3 === 0
        ? sched.submit("flaky", {
            run: () => {
              throw new Error("sync boom");
            },
          })
        : sched.submit("flaky", { run: async () => Promise.reject(new Error("async boom")) });
    jobs.push(p.catch((e: Error) => e.message));
  }
  const results = await Promise.all(jobs);
  assert.ok(results.every((r) => typeof r === "string" && r.includes("boom")));
  await sched.drain();

  assert.ok(sched.idle, "scheduler not idle after failures");
  assert.equal(sched.totalInFlight, 0);
  const s = sched.stats()[0];
  assert.equal(s.inFlight, 0);
  assert.equal(s.queued, 0);
  assert.equal(s.dispatched, 20);
  assert.equal(s.failed, 20);
  assert.equal(s.completed, 0);
});

test("no leak: results are delivered to the right submitter under concurrency", async () => {
  const sched = makeScheduler({ maxConcurrentTotal: 8 });
  sched.registerTenant("a", { weight: 3, maxConcurrent: 4 });
  sched.registerTenant("b", { weight: 1, maxConcurrent: 4 });

  const jobs: Promise<unknown>[] = [];
  for (let i = 0; i < 200; i++) {
    const tenant = i % 2 === 0 ? "a" : "b";
    jobs.push(
      sched.submit(tenant, {
        run: async () => {
          await tick();
          return `${tenant}:${i}`;
        },
      }),
    );
  }
  const results = await Promise.all(jobs);
  for (let i = 0; i < 200; i++) {
    const tenant = i % 2 === 0 ? "a" : "b";
    assert.equal(results[i], `${tenant}:${i}`);
  }
  await sched.drain();
  assert.ok(sched.idle);
  assert.equal(sched.totalInFlight, 0);
  for (const s of sched.stats()) {
    assert.equal(s.inFlight, 0, `tenant ${s.id} leaked in-flight permits`);
    assert.equal(s.queued, 0, `tenant ${s.id} leaked queued jobs`);
    assert.equal(s.dispatched, s.completed + s.failed);
  }
});

test("deficit accounting: unaffordable head retains deficit across visits", async () => {
  const sched = makeScheduler({ baseQuantum: 2 });
  sched.registerTenant("x", { weight: 1 });
  // One cost-5 job: visits charge 2,4,6 -> dispatch, leftover 1, reset on empty.
  await sched.submit("x", { cost: 5, run: () => 1 });
  await sched.drain();
  assert.equal(sched.stats()[0].deficit, 0);
  assert.ok(sched.idle);
});

test("validation: bad registrations and costs are rejected", async () => {
  const sched = makeScheduler();
  assert.throws(() => new TenantScheduler({ baseQuantum: 0 }));
  sched.registerTenant("t", {});
  assert.throws(() => sched.registerTenant("t", {}));
  assert.throws(() => sched.registerTenant("neg", { weight: -1 }));
  await assert.rejects(sched.submit("t", { cost: 0, run: () => 1 }));
  sched.unregisterTenant("t"); // idle tenant unregisters cleanly
  assert.equal(sched.stats().length, 0);
});

test("unregister: busy tenant cannot be removed, idle one can", async () => {
  const sched = makeScheduler();
  sched.registerTenant("temp", {});
  sched.unregisterTenant("temp");
  await assert.rejects(sched.submit("temp", { run: () => 1 }), /not registered/);

  sched.registerTenant("busy", { maxConcurrent: 1 });
  const p = sched.submit("busy", { run: async () => tick() });
  assert.throws(() => sched.unregisterTenant("busy"));
  await p;
  await sched.drain();
  sched.unregisterTenant("busy");
  assert.equal(sched.stats().length, 0);
});
