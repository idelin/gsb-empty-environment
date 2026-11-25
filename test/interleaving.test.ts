import assert from "node:assert/strict";
import { test } from "node:test";
import {
  AuthorizationService,
  InMemoryBusinessGateway,
  InMemoryPolicyStore,
  SubmitStatus,
  claimKey,
} from "../src/index.ts";
import {
  DeterministicRng,
  ScriptedCasStore,
  runAsAgent,
  type AgentId,
} from "../src/testing/scheduler.ts";
import { buildGrant, buildSubmit, testAliases, testKey } from "../src/testing/index.ts";

function makeService(store: ScriptedCasStore) {
  const gateway = new InMemoryBusinessGateway();
  const service = new AuthorizationService({
    store,
    policy: new InMemoryPolicyStore(),
    gateway,
    signingKeys: [testKey],
    aliases: testAliases,
  });
  return { service, gateway };
}

async function settle(store: ScriptedCasStore, promises: Array<Promise<unknown>>): Promise<void> {
  // Every storage op yields; drain queues and microtasks until all settle.
  for (let guard = 0; guard < 10_000 && promises.some((promise) => !(promise as PromiseWithState).settled); guard += 1) {
    store.step();
    await Promise.resolve();
  }
  await Promise.all(promises);
}

interface PromiseWithState {
  settled: boolean;
}

function track<T>(promise: Promise<T>): Promise<T> & PromiseWithState {
  const tracked = promise as Promise<T> & PromiseWithState;
  tracked.settled = false;
  tracked.then(
    () => {
      tracked.settled = true;
    },
    () => {
      tracked.settled = true;
    },
  );
  return tracked;
}

test("two windows racing one authorization: at most one holds the writer slot", async () => {
  for (let yieldEvery = 1; yieldEvery <= 4; yieldEvery += 1) {
    const store = new ScriptedCasStore();
    store.setYieldEvery(yieldEvery);
    const { service, gateway } = makeService(store);
    const grant = buildGrant();

    const windowOne = track(
      runAsAgent("window-1", () => service.submit(buildSubmit(grant, { idemKey: "idem-1" }))),
    );
    const windowTwo = track(
      runAsAgent("window-2", () => service.submit(buildSubmit(grant, { idemKey: "idem-2" }))),
    );
    await settle(store, [windowOne, windowTwo]);

    const results = [await windowOne, await windowTwo] as const;
    const successes = results.filter((result) => result.status === SubmitStatus.Success);
    const conflicts = results.filter((result) => result.status === SubmitStatus.Conflict);
    assert.equal(successes.length, 1, `yieldEvery=${yieldEvery}: exactly one success`);
    assert.equal(conflicts.length, 1, `yieldEvery=${yieldEvery}: exactly one conflict`);

    // The winner releases the single-writer pointer after its terminal
    // decision; audit the durable claim tickets instead.
    const report = await service.reportConsumption(grant.grantId, "confirm");
    assert.equal(report.actionConsumptions, 1);
    assert.equal(report.activeClaim, undefined);
    assert.equal(successes[0]!.decision!.idemKey, report.decisions[0]!.idemKey);
    // Only the winner produced business effects.
    assert.equal(gateway.effectCount(), 3);

    // If the pointer still exists it must belong to the single winner.
    const pointer = store.inspect(claimKey(grant.grantId, "confirm"));
    if (pointer) {
      assert.equal((pointer.value as { idemKey: string }).idemKey, successes[0]!.decision!.idemKey);
    }
  }
});

test("randomized schedule sweep preserves the single-writer invariant", async () => {
  const rng = new DeterministicRng(20261126);
  for (let iteration = 0; iteration < 120; iteration += 1) {
    const store = new ScriptedCasStore();
    store.setYieldEvery(1 + rng.nextInt(3));
    const { service, gateway } = makeService(store);
    const grant = buildGrant();

    const agents: Array<Promise<unknown> & { settled?: boolean }> = [
      runAsAgent("window-1", () => service.submit(buildSubmit(grant, { idemKey: `idem-a-${iteration}` }))),
      runAsAgent("window-2", () => service.submit(buildSubmit(grant, { idemKey: `idem-b-${iteration}` }))),
    ];
    for (const promise of agents) {
      promise.then(
        () => {
          promise.settled = true;
        },
        () => {
          promise.settled = true;
        },
      );
    }

    for (let guard = 0; guard < 20_000 && agents.some((promise) => !promise.settled); guard += 1) {
      const pending = store.pendingAgents();
      if (pending.length === 0) {
        await Promise.resolve();
        continue;
      }
      const chosen = pending[rng.nextInt(pending.length)] as AgentId;
      store.stepAgent(chosen);
      await Promise.resolve();
    }
    const results = (await Promise.all(agents)) as Array<{ status: string }>;
    assert.equal(results.filter((result) => result.status === SubmitStatus.Success).length, 1);
    assert.equal(results.filter((result) => result.status === SubmitStatus.Conflict).length, 1);
    assert.equal(gateway.effectCount(), 3);

    // Audit: the claim key was created exactly once (only one successful create CAS).
    const claimWrites = store.audit.filter(
      (entry) => entry.key === claimKey(grant.grantId, "confirm") && entry.version === 1,
    );
    assert.equal(claimWrites.length, 1);
  }
});

test("two parent actions reusing the same subId stay isolated", async () => {
  const store = new ScriptedCasStore();
  const { service, gateway } = makeService(store);
  const grant = buildGrant({ twoActions: true });

  const first = track(
    runAsAgent("window-1", () =>
      service.submit(buildSubmit(grant, { actionId: "confirm", idemKey: "idem-x" })),
    ),
  );
  const second = track(
    runAsAgent("window-2", () =>
      service.submit(
        buildSubmit(grant, {
          actionId: "confirm-alt",
          idemKey: "idem-y",
          subOperations: [
            { subId: "step", payload: { n: 1 } },
            { subId: "reserve", payload: { n: 2 } },
          ],
        }),
      ),
    ),
  );
  await settle(store, [first, second]);
  assert.equal((await first).status, SubmitStatus.Success);
  assert.equal((await second).status, SubmitStatus.Success);

  const primary = await service.reportConsumption(grant.grantId, "confirm");
  const alternate = await service.reportConsumption(grant.grantId, "confirm-alt");
  assert.deepEqual(primary.subConsumptions, { reserve: 1, ledger: 1, notify: 1 });
  assert.deepEqual(alternate.subConsumptions, { reserve: 1, step: 1 });
  assert.equal(gateway.effectCount(), 5);
});
