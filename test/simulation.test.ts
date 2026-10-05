import { strict as assert } from "assert";
import {
  Session,
  SessionRouter,
  SessionTimeoutError,
  SimulatedNode,
  StalePolicy,
  VersionVector,
} from "../src";

let passed = 0;
async function scenario(name: string, fn: () => Promise<void> | void): Promise<void> {
  await fn();
  passed++;
  console.log(`ok - ${name}`);
}

async function expectTimeout(fn: () => Promise<unknown>): Promise<SessionTimeoutError> {
  try {
    await fn();
  } catch (err) {
    assert.ok(err instanceof SessionTimeoutError, `expected SessionTimeoutError, got ${err}`);
    return err as SessionTimeoutError;
  }
  throw new Error("expected SessionTimeoutError, but read succeeded");
}

async function main(): Promise<void> {
  await scenario("session watermark never decreases across responses", () => {
    const session = new Session("s1");
    session.recordResponse("p0", 5);
    session.recordResponse("p0", 3); // stale response must not lower the watermark
    session.recordResponse("p0", 7);
    assert.equal(session.requiredVersion("p0"), 7);
    assert.equal(session.requiredVersion("p1"), 0);
  });

  await scenario("session merge takes the max watermark per partition", () => {
    const a = new Session("a");
    a.recordResponse("p0", 4);
    a.recordResponse("p1", 9);
    const b = new Session("b");
    b.recordResponse("p0", 6);
    b.recordResponse("p2", 2);
    a.merge(b);
    assert.equal(a.requiredVersion("p0"), 6);
    assert.equal(a.requiredVersion("p1"), 9);
    assert.equal(a.requiredVersion("p2"), 2);
    // b is untouched
    assert.equal(b.requiredVersion("p1"), 0);
    // VersionVector.merge produces an independent vector
    const merged = VersionVector.merge(a.watermark(), b.watermark());
    assert.equal(merged.get("p0"), 6);
    merged.record("p0", 100);
    assert.equal(a.requiredVersion("p0"), 6);
  });

  await scenario("SWITCH policy fails over to a fresh replica when the primary lags", async () => {
    // A write lands on node-b at v6; node-a still replicates (stuck at v5).
    const nodeA = new SimulatedNode("node-a", { latencyMs: 1, partitions: { p0: 5 } });
    const nodeB = new SimulatedNode("node-b", { latencyMs: 1, partitions: { p0: 6 } });
    const router = new SessionRouter([nodeA, nodeB], {
      policy: StalePolicy.SWITCH,
      timeoutMs: 500,
    });
    const session = new Session();
    const first = await router.read(session, "p0");
    assert.equal(first.version, 5);
    nodeA.advance("p0", 5); // no-op; node-a stays behind
    const second = await router.read(session, "p0");
    // watermark is 5, node-a still serves 5 which is acceptable; force a write
    // observed on node-b to make node-a genuinely stale:
    session.recordResponse("p0", 6);
    const third = await router.read(session, "p0");
    assert.equal(third.nodeId, "node-b");
    assert.equal(third.version, 6);
    assert.ok(second.version <= third.version);
  });

  await scenario("SWITCH policy times out when every replica lags", async () => {
    const nodeA = new SimulatedNode("node-a", { latencyMs: 1, partitions: { p0: 3 } });
    const nodeB = new SimulatedNode("node-b", { latencyMs: 1, partitions: { p0: 4 } });
    const router = new SessionRouter([nodeA, nodeB], {
      policy: StalePolicy.SWITCH,
      timeoutMs: 200,
    });
    const session = new Session();
    session.recordResponse("p0", 10); // watermark beyond every replica
    const err = await expectTimeout(() => router.read(session, "p0"));
    assert.equal(err.policy, StalePolicy.SWITCH);
    assert.equal(err.requiredVersion, 10);
  });

  await scenario("WAIT policy blocks until the replica catches up", async () => {
    const node = new SimulatedNode("node-a", { latencyMs: 1, partitions: { p0: 3 } });
    const router = new SessionRouter([node], {
      policy: StalePolicy.WAIT,
      timeoutMs: 1000,
      pollIntervalMs: 5,
    });
    const session = new Session();
    session.recordResponse("p0", 8);
    // Replication catches up after ~30ms.
    setTimeout(() => node.advance("p0", 8), 30);
    const result = await router.read(session, "p0");
    assert.equal(result.version, 8);
    assert.equal(session.requiredVersion("p0"), 8);
  });

  await scenario("WAIT policy respects the timeout when the replica never catches up", async () => {
    const node = new SimulatedNode("node-a", { latencyMs: 1, partitions: { p0: 3 } });
    const router = new SessionRouter([node], {
      policy: StalePolicy.WAIT,
      timeoutMs: 60,
      pollIntervalMs: 5,
    });
    const session = new Session();
    session.recordResponse("p0", 8);
    const started = Date.now();
    const err = await expectTimeout(() => router.read(session, "p0"));
    assert.equal(err.policy, StalePolicy.WAIT);
    assert.ok(Date.now() - started >= 50, "should have waited until the deadline");
  });

  await scenario("TIMEOUT policy fails immediately on a stale replica", async () => {
    const node = new SimulatedNode("node-a", { latencyMs: 1, partitions: { p0: 3 } });
    const router = new SessionRouter([node], {
      policy: StalePolicy.TIMEOUT,
      timeoutMs: 1000,
    });
    const session = new Session();
    session.recordResponse("p0", 4);
    const started = Date.now();
    const err = await expectTimeout(() => router.read(session, "p0"));
    assert.equal(err.policy, StalePolicy.TIMEOUT);
    assert.ok(Date.now() - started < 100, "should fail fast without waiting");
  });

  await scenario("TIMEOUT policy enforces the deadline against slow nodes", async () => {
    const node = new SimulatedNode("node-a", { latencyMs: 80, partitions: { p0: 9 } });
    const router = new SessionRouter([node], {
      policy: StalePolicy.TIMEOUT,
      timeoutMs: 20,
    });
    const session = new Session();
    const err = await expectTimeout(() => router.read(session, "p0"));
    assert.equal(err.policy, StalePolicy.TIMEOUT);
  });

  await scenario("merged sessions keep monotonic reads across partitions and nodes", async () => {
    // Two partitions, three nodes with skewed replication.
    const nodeA = new SimulatedNode("node-a", { latencyMs: [1, 3], partitions: { p0: 10, p1: 4 } });
    const nodeB = new SimulatedNode("node-b", { latencyMs: [1, 3], partitions: { p0: 8, p1: 7 } });
    const nodeC = new SimulatedNode("node-c", { latencyMs: [1, 3], partitions: { p0: 10, p1: 7 } });
    const router = new SessionRouter([nodeA, nodeB, nodeC], {
      policy: StalePolicy.SWITCH,
      timeoutMs: 500,
    });

    // Session 1 reads p0 on a fresh node; session 2 reads p1 elsewhere.
    const s1 = new Session("s1");
    const s2 = new Session("s2");
    assert.equal((await router.read(s1, "p0")).version, 10);
    // s2 starts empty: first read lands on node-a (p1=4)
    assert.equal((await router.read(s2, "p1")).version, 4);
    // s2 then observes p1=7 (e.g. routed after node-a's result was recorded by someone else)
    s2.recordResponse("p1", 7);

    // Merged session must require max per partition: p0=10, p1=7.
    const merged = new Session("merged");
    merged.merge(s1);
    merged.merge(s2);
    assert.equal(merged.requiredVersion("p0"), 10);
    assert.equal(merged.requiredVersion("p1"), 7);

    // Every subsequent read must satisfy the merged watermark.
    const r0 = await router.read(merged, "p0");
    assert.ok(r0.version >= 10);
    const r1 = await router.read(merged, "p1");
    assert.ok(r1.version >= 7);
    assert.notEqual(r1.nodeId, "node-a"); // node-a is stale for p1, must be skipped
  });

  await scenario("randomized workload: every returned version is monotonic per partition", async () => {
    const partitions = ["p0", "p1", "p2"];
    const makeNodes = () => [
      new SimulatedNode("n0", { latencyMs: [0, 2], partitions: { p0: 0, p1: 0, p2: 0 } }),
      new SimulatedNode("n1", { latencyMs: [0, 2], partitions: { p0: 0, p1: 0, p2: 0 } }),
      new SimulatedNode("n2", { latencyMs: [0, 2], partitions: { p0: 0, p1: 0, p2: 0 } }),
    ];
    const nodes = makeNodes();
    const router = new SessionRouter(nodes, {
      policy: StalePolicy.SWITCH,
      timeoutMs: 1000,
    });
    const session = new Session("workload");
    const lastReturned: Record<string, number> = { p0: 0, p1: 0, p2: 0 };
    let versionClock = 0;

    for (let step = 0; step < 60; step++) {
      // Simulate a write: bump the global clock and replicate to a random
      // subset of nodes (the rest lag behind).
      versionClock++;
      const partition = partitions[Math.floor(Math.random() * partitions.length)];
      for (const node of nodes) {
        if (Math.random() < 0.7) {
          node.advance(partition, versionClock);
        }
      }
      const result = await router.read(session, partition);
      assert.ok(
        result.version >= lastReturned[partition],
        `partition ${partition}: returned ${result.version} after ${lastReturned[partition]}`
      );
      assert.ok(result.version >= session.requiredVersion(partition) ||
                result.version === lastReturned[partition]);
      lastReturned[partition] = result.version;
      assert.equal(session.requiredVersion(partition), result.version);
    }
    assert.ok(versionClock > 0);
  });

  console.log(`\n${passed} scenarios passed`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
