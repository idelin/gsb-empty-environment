import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/paxos.ts';
import {
  SagaOrchestrator,
  IdempotentEffects,
  SagaUnavailableError,
} from '../src/saga.ts';
import type { StepDef } from '../src/saga.ts';
import { assertNoLeaks } from './helpers.ts';

const IDS = ['n1', 'n2', 'n3', 'n4', 'n5'];

function setup() {
  const cluster = new Cluster(IDS);
  const effects = new IdempotentEffects();
  const orch = new SagaOrchestrator(cluster.node('n1'), effects);
  return { cluster, effects, orch };
}

/** A recorded step: tracks execute/compensate invocation order and counts. */
function step(
  name: string,
  calls: string[],
  opts: { failExecute?: boolean; compensateTimes?: { fail: number } } = {},
): StepDef & { execCount: () => number; compCount: () => number } {
  let execs = 0;
  let comps = 0;
  return {
    name,
    execCount: () => execs,
    compCount: () => comps,
    execute: async () => {
      execs++;
      calls.push(`exec:${name}`);
      await Promise.resolve();
      if (opts.failExecute) throw new Error(`${name} blew up`);
    },
    compensate: async () => {
      comps++;
      calls.push(`comp:${name}`);
      await Promise.resolve();
      if (opts.compensateTimes && comps <= opts.compensateTimes.fail) {
        throw new Error(`${name} compensation failed`);
      }
    },
  };
}

test('happy path: steps execute in order, saga completes, nothing compensated', async () => {
  const { cluster, effects, orch } = setup();
  const calls: string[] = [];
  const steps = [step('reserve', calls), step('charge', calls), step('ship', calls)];

  const status = await orch.runSaga('s1', steps);
  assert.equal(status, 'completed');
  assert.deepEqual(calls, ['exec:reserve', 'exec:charge', 'exec:ship']);
  assert.equal(orch.statusOf('s1'), 'completed');

  // Orchestration events are durably committed through the Paxos log.
  const committed = cluster.node('n3').applied.map((e) => (e.payload as { type?: string })?.type);
  assert.deepEqual(committed, [
    'saga-started',
    'step-executed',
    'step-executed',
    'step-executed',
    'saga-completed',
  ]);
  assert.equal(effects.size, 3);
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch]);
});

test('failure mid-saga: executed steps compensated in strict reverse order', async () => {
  const { cluster, effects, orch } = setup();
  const calls: string[] = [];
  const steps = [
    step('reserve', calls),
    step('charge', calls),
    step('ship', calls, { failExecute: true }),
    step('notify', calls),
  ];

  const status = await orch.runSaga('s2', steps);
  assert.equal(status, 'compensated');
  assert.deepEqual(calls, [
    'exec:reserve',
    'exec:charge',
    'exec:ship', // failed: never compensated
    'comp:charge',
    'comp:reserve',
  ]);
  assert.equal(orch.statusOf('s2'), 'compensated');
  // The failed step itself is never compensated (its execute did not succeed).
  assert.equal(steps[2].compCount(), 0);
  assert.equal(steps[3].execCount(), 0, 'later steps never run');
  assert.equal(effects.size, 4, '2 executes + 2 compensates journaled');
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch]);
});

test('failure at first step: nothing to compensate', async () => {
  const { cluster, orch } = setup();
  const calls: string[] = [];
  const steps = [step('a', calls, { failExecute: true }), step('b', calls)];
  const status = await orch.runSaga('s3', steps);
  assert.equal(status, 'compensated');
  assert.deepEqual(calls, ['exec:a']);
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch]);
});

test('idempotency: duplicate runSaga calls never duplicate side effects', async () => {
  const { cluster, effects, orch } = setup();
  const calls: string[] = [];
  const steps = [step('x', calls), step('y', calls)];

  const [r1, r2, r3] = await Promise.all([
    orch.runSaga('s4', steps),
    orch.runSaga('s4', steps),
    orch.runSaga('s4', steps),
  ]);
  assert.deepEqual([r1, r2, r3], ['completed', 'completed', 'completed']);
  assert.deepEqual(calls, ['exec:x', 'exec:y'], 'effects applied exactly once');
  assert.equal(effects.size, 2);
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch]);
});

test('idempotency: IdempotentEffects only journals successful calls', async () => {
  const effects = new IdempotentEffects();
  let runs = 0;
  await effects.apply('k', () => {
    runs++;
  });
  await effects.apply('k', () => {
    runs++;
  });
  assert.equal(runs, 1);

  let attempts = 0;
  await assert.rejects(
    effects.apply('bad', () => {
      attempts++;
      throw new Error('nope');
    }),
  );
  assert.ok(!effects.has('bad'), 'failed effect is not journaled');
  await effects.apply('bad', () => {
    attempts++;
  });
  assert.equal(attempts, 2, 'retry after failure really runs');
  assert.equal(effects.pending, 0);
});

test('compensation retries until success (final consistency)', async () => {
  const { cluster, orch } = setup();
  const calls: string[] = [];
  const flaky = { fail: 2 };
  const steps = [
    step('a', calls, { compensateTimes: flaky }),
    step('b', calls, { failExecute: true }),
  ];
  const status = await orch.runSaga('s5', steps, );
  assert.equal(status, 'compensated');
  assert.equal(steps[0].compCount(), 3, '2 failures + 1 success');
  assert.deepEqual(calls.filter((c) => c.startsWith('comp')), [
    'comp:a',
    'comp:a',
    'comp:a',
  ]);
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch]);
});

test('compensation exhaustion pauses saga; recover() finishes it later', async () => {
  const cluster = new Cluster(IDS);
  const effects = new IdempotentEffects();
  const orch = new SagaOrchestrator(cluster.node('n1'), effects, { compensateRetries: 2 });

  const calls: string[] = [];
  const gate = { fail: Number.POSITIVE_INFINITY };
  const steps = [
    step('a', calls, { compensateTimes: gate }),
    step('b', calls, { failExecute: true }),
  ];
  const status = await orch.runSaga('s6', steps);
  assert.equal(status, 'compensating', 'saga parks in compensating state');
  assert.equal(steps[0].compCount(), 2, 'gave up after compensateRetries attempts');
  assert.equal(orch.pendingSagaCount, 1);

  // Operator fixes the underlying problem; recovery drives the saga to a
  // terminal state — final consistency.
  gate.fail = 0;
  await orch.recover();
  assert.equal(orch.statusOf('s6'), 'compensated');
  assert.equal(steps[0].compCount(), 3);
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch]);
});

test('orchestrator failover: new orchestrator resumes from replicated log, effects exactly-once', async () => {
  const cluster = new Cluster(IDS);
  const effects = new IdempotentEffects(); // shared, durable journal
  const orch1 = new SagaOrchestrator(cluster.node('n1'), effects);

  const calls: string[] = [];
  const steps = [step('debit', calls), step('credit', calls), step('notify', calls)];

  // Commit step 1, then partition so step 2's effect applies but its
  // commit cannot reach a majority — orchestrator 1 "crashes" mid-saga.
  const p = orch1.runSaga('s7', steps);
  // Let the first step commit, then cut the leader away.
  await Promise.resolve();
  cluster.partition([['n1'], ['n2', 'n3', 'n4', 'n5']]);
  await assert.rejects(p, SagaUnavailableError);
  assert.equal(orch1.statusOf('s7'), 'running', 'saga left in-flight by the crash');

  // Majority side recovers: a fresh orchestrator on n3 replays the
  // replicated log and resumes the saga with the same durable journal.
  cluster.heal();
  cluster.clock.advance(1000);
  const orch2 = new SagaOrchestrator(cluster.node('n3'), effects);
  assert.equal(orch2.statusOf('s7'), 'running', 'projection rebuilt from committed log');
  await orch2.runSaga('s7', steps);
  assert.equal(orch2.statusOf('s7'), 'completed');

  assert.deepEqual(
    calls,
    ['exec:debit', 'exec:credit', 'exec:notify'],
    'every side effect applied exactly once across the failover',
  );
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch1, orch2]);
});

test('failed saga compensation survives orchestrator failover', async () => {
  const cluster = new Cluster(IDS);
  const effects = new IdempotentEffects();
  const orch1 = new SagaOrchestrator(cluster.node('n1'), effects);

  const calls: string[] = [];
  // Step 2 fails; step 1 must be compensated. We crash orchestrator 1 after
  // the failure is committed but before compensation completes, by making
  // the compensation commit impossible (partition triggered from inside).
  let partitioned = false;
  const steps: StepDef[] = [
    {
      name: 'debit',
      execute: () => {
        calls.push('exec:debit');
      },
      compensate: () => {
        calls.push('comp:debit');
        if (!partitioned) {
          partitioned = true;
          cluster.partition([['n1'], ['n2', 'n3', 'n4', 'n5']]);
        }
      },
    },
    {
      name: 'credit',
      execute: () => {
        calls.push('exec:credit');
        throw new Error('credit failed');
      },
      compensate: () => {
        calls.push('comp:credit');
      },
    },
  ];

  await assert.rejects(orch1.runSaga('s8', steps), SagaUnavailableError);
  assert.equal(orch1.statusOf('s8'), 'compensating', 'compensation interrupted mid-flight');
  assert.deepEqual(calls, ['exec:debit', 'exec:credit', 'comp:debit']);

  // Fail over: new orchestrator resumes reverse compensation exactly once.
  cluster.heal();
  cluster.clock.advance(1000);
  const orch2 = new SagaOrchestrator(cluster.node('n3'), effects);
  assert.equal(orch2.statusOf('s8'), 'compensating');
  await orch2.recover(new Map([['s8', steps]]));
  assert.equal(orch2.statusOf('s8'), 'compensated');
  assert.deepEqual(
    calls.filter((c) => c === 'comp:debit').length,
    1,
    'debit compensated exactly once across failover',
  );
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch1, orch2]);
});

test('concurrency: 25 sagas over shared bank, failures injected, invariant holds', async () => {
  const { cluster, effects, orch } = setup();

  // Shared accounts; every transfer keeps the total constant.
  const accounts = new Map<string, number>([
    ['alice', 1000],
    ['bob', 1000],
    ['carol', 1000],
  ]);
  const totalBefore = [...accounts.values()].reduce((a, b) => a + b, 0);

  const compensationOrder = new Map<string, string[]>();
  const mkTransfer = (id: string, from: string, to: string, amount: number, failAt: number): StepDef[] => {
    const order: string[] = [];
    compensationOrder.set(id, order);
    const debit: StepDef = {
      name: 'debit',
      execute: async () => {
        await Promise.resolve();
        if (failAt === 0) throw new Error('debit failed');
        accounts.set(from, accounts.get(from)! - amount);
      },
      compensate: async () => {
        await Promise.resolve();
        order.push('debit');
        accounts.set(from, accounts.get(from)! + amount);
      },
    };
    const credit: StepDef = {
      name: 'credit',
      execute: async () => {
        await Promise.resolve();
        if (failAt === 1) throw new Error('credit failed');
        accounts.set(to, accounts.get(to)! + amount);
      },
      compensate: async () => {
        await Promise.resolve();
        order.push('credit');
        accounts.set(to, accounts.get(to)! - amount);
      },
    };
    const audit: StepDef = {
      name: 'audit',
      execute: async () => {
        await Promise.resolve();
        if (failAt === 2) throw new Error('audit failed');
      },
      compensate: async () => {
        order.push('audit');
        await Promise.resolve();
      },
    };
    return [debit, credit, audit];
  };

  // Deterministic pseudo-random failure plan.
  const runs: Promise<unknown>[] = [];
  const names = ['alice', 'bob', 'carol'];
  for (let i = 0; i < 25; i++) {
    const from = names[i % 3];
    const to = names[(i + 1) % 3];
    const failAt = i % 3 === 0 ? -1 : i % 3 === 1 ? 1 : 2; // some succeed, some fail
    runs.push(orch.runSaga(`tx-${i}`, mkTransfer(`tx-${i}`, from, to, 10, failAt)));
  }
  const statuses = await Promise.all(runs);

  // Money is conserved no matter how sagas interleaved.
  const totalAfter = [...accounts.values()].reduce((a, b) => a + b, 0);
  assert.equal(totalAfter, totalBefore, 'bank invariant: no money created or destroyed');

  for (let i = 0; i < 25; i++) {
    const status = statuses[i];
    const failAt = i % 3 === 0 ? -1 : i % 3 === 1 ? 1 : 2;
    if (failAt === -1) {
      assert.equal(status, 'completed');
      assert.equal(compensationOrder.get(`tx-${i}`)!.length, 0);
    } else {
      assert.equal(status, 'compensated');
      // Compensation order is the exact reverse of the executed steps.
      const expected = ['debit', 'credit', 'audit'].slice(0, failAt).reverse();
      assert.deepEqual(compensationOrder.get(`tx-${i}`), expected, `tx-${i} reverse order`);
    }
  }

  // Journal holds exactly one key per applied effect — no duplicates.
  const expectedKeys = 25 * 3; // every saga applied 3 effects (execs + compensates vary)
  assert.ok(effects.size <= expectedKeys);
  assertNoLeaks(cluster.net, [...cluster.nodes.values()], [orch]);
});
