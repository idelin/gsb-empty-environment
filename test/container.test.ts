import { test } from "node:test";
import assert from "node:assert/strict";
import { Container, DependencyCycleError } from "../src/container.ts";
import { Barrier, Deferred, Gate } from "../src/barrier.ts";

test("concurrent requests share a single construction", async () => {
  const container = new Container();
  const entered = new Deferred();
  const release = new Gate();
  let factoryCalls = 0;

  container.register("svc", async () => {
    factoryCalls += 1;
    entered.resolve();
    await release.wait();
    return { id: factoryCalls };
  });

  const first = container.get("svc");
  const second = container.get("svc");
  await entered.promise; // factory is in flight before the second request lands
  release.open();

  const [a, b] = await Promise.all([first, second]);
  assert.equal(factoryCalls, 1);
  assert.equal(a, b); // same singleton instance
  assert.equal(await container.get("svc"), a); // cached afterwards
});

test("dependency cycle along one request chain reports the closed loop", async () => {
  const container = new Container();
  container.register("a", (r) => r.get("b"));
  container.register("b", (r) => r.get("c"));
  container.register("c", (r) => r.get("a"));

  const error = await container.get("a").then(
    () => assert.fail("expected a DependencyCycleError"),
    (e: unknown) => e,
  );
  assert.ok(error instanceof DependencyCycleError);
  assert.equal(error.message, "Dependency cycle detected: a -> b -> c -> a");
  assert.deepEqual(error.chain, ["a", "b", "c", "a"]);
});

test("concurrent requests forming a cycle are both rejected, no deadlock", async () => {
  const container = new Container();
  const bothFactoriesRunning = new Barrier(2);

  container.register("a", async (r) => {
    await bothFactoriesRunning.wait();
    return r.get("b");
  });
  container.register("b", async (r) => {
    await bothFactoriesRunning.wait();
    return r.get("a");
  });

  const [resultA, resultB] = await Promise.allSettled([
    container.get("a"),
    container.get("b"),
  ]);

  assert.equal(resultA.status, "rejected");
  assert.equal(resultB.status, "rejected");
  const reasons = [resultA, resultB].map((r) => (r as PromiseRejectedResult).reason);
  for (const reason of reasons) assert.ok(reason instanceof DependencyCycleError);
  // The detected loop is reported once and propagates to both waiters.
  const loops = reasons.map((r: DependencyCycleError) => r.chain.join(" -> "));
  for (const loop of loops) assert.match(loop, /^(a -> b -> a|b -> a -> b)$/);
});

test("failed construction shares the failure, then a new generation retries", async () => {
  const container = new Container();
  const entered = new Deferred();
  const release = new Gate();
  let attempts = 0;

  container.register("flaky", async () => {
    attempts += 1;
    if (attempts === 1) {
      entered.resolve();
      await release.wait();
      throw new Error("boom");
    }
    return "recovered";
  });

  const first = container.get("flaky");
  const second = container.get("flaky");
  await entered.promise; // both requests attached to the in-flight generation
  release.open();

  await assert.rejects(first, /boom/);
  await assert.rejects(second, /boom/);
  assert.equal(attempts, 1); // one construction attempt served both waiters

  // The failed generation is discarded; the next request starts a fresh one.
  assert.equal(await container.get("flaky"), "recovered");
  assert.equal(attempts, 2);
  assert.equal(await container.get("flaky"), "recovered"); // cached now
  assert.equal(attempts, 2);
});

test("constructed resources are disposed in reverse dependency order, exactly once", async () => {
  const container = new Container();
  const events: string[] = [];
  const track = (name: string) => [
    async () => {
      events.push(`build ${name}`);
      return name;
    },
    () => {
      events.push(`dispose ${name}`);
    },
  ] as const;

  const [buildB, disposeB] = track("b");
  const [buildC, disposeC] = track("c");
  container.register("b", buildB, disposeB);
  container.register("c", buildC, disposeC);
  container.register(
    "a",
    async (r) => {
      await r.get("b");
      await r.get("c");
      events.push("build a");
      return "a";
    },
    () => {
      events.push("dispose a");
    },
  );

  await container.get("a");
  await container.dispose();
  await container.dispose(); // idempotent: must not dispose twice

  assert.deepEqual(events, [
    "build b",
    "build c",
    "build a",
    "dispose a",
    "dispose c",
    "dispose b",
  ]);
});

test("failed constructions are not disposed; disposed container rejects new gets", async () => {
  const container = new Container();
  let disposedGood = 0;
  let disposedBad = 0;

  container.register(
    "good",
    async () => "good-instance",
    () => {
      disposedGood += 1;
    },
  );
  container.register(
    "bad",
    async () => {
      throw new Error("never constructed");
    },
    () => {
      disposedBad += 1;
    },
  );

  await container.get("good");
  await assert.rejects(container.get("bad"), /never constructed/);

  await container.dispose();
  assert.equal(disposedGood, 1);
  assert.equal(disposedBad, 0);

  await assert.rejects(container.get("good"), /disposed/);
});
