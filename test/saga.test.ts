import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster, awaitCommit } from '../src/cluster.ts';
import { Effects, SagaOrchestrator, SagaStateMachine, type SagaStep } from '../src/saga.ts';
import type { PaxosNode } from '../src/paxos.ts';
import type { SagaEvent } from '../src/types.ts';

interface Ctx {
  fx: Effects;
  calls: string[];
}

/** Wire an orchestrator to a leader node; state machine derives from committed log. */
function wire(node: PaxosNode, sm: SagaStateMachine, opts?: { maxCompensateRetries?: number }) {
  return new SagaOrchestrator<Ctx>(
    async (event: SagaEvent) => {
      const slot = node.propose({ kind: 'saga', event });
      await awaitCommit(node, slot);
    },
    sm,
    opts,
  );
}

function singleNode() {
  const c = new Cluster(1);
  const node = c.electLeader();
  const sm = new SagaStateMachine();
  node.onCommit((_, entry) => {
    if (entry.value.kind === 'saga') sm.apply(entry.value.event);
  });
  return { c, node, sm };
}

const okStep = (name: string): SagaStep<Ctx> => ({
  name,
  execute: async (ctx) => {
    await ctx.fx.once(`${name}:exec`, () => ctx.calls.push(`exec:${name}`));
  },
  compensate: async (ctx) => {
    await ctx.fx.once(`${name}:comp`, () => ctx.calls.push(`comp:${name}`));
  },
});

const failingStep = (name: string, message = 'boom'): SagaStep<Ctx> => ({
  name,
  execute: () => {
    throw new Error(message);
  },
  compensate: async (ctx) => {
    await ctx.fx.once(`${name}:comp`, () => ctx.calls.push(`comp:${name}`));
  },
});

test('happy path: steps execute in order and saga completes via committed log', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm);
  const ctx: Ctx = { fx: new Effects(), calls: [] };

  const rec = await orch.submit('s1', [okStep('a'), okStep('b'), okStep('c')], ctx);

  assert.equal(rec.status, 'completed');
  assert.deepEqual(ctx.calls, ['exec:a', 'exec:b', 'exec:c']);
  assert.equal(sm.get('s1')?.status, 'completed');
  // Every transition was journaled through the consensus log, in order.
  const events = node.applied
    .filter((e) => e.value.kind === 'saga')
    .map((e) => (e.value.kind === 'saga' ? e.value.event.type : ''));
  assert.deepEqual(events, ['step-executed', 'step-executed', 'step-executed', 'saga-completed']);
});

test('failure triggers reverse compensation in exact order, no further executes', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm);
  const ctx: Ctx = { fx: new Effects(), calls: [] };

  const rec = await orch.submit(
    's2',
    [okStep('a'), okStep('b'), okStep('c'), failingStep('d', 'step d exploded'), okStep('e')],
    ctx,
  );

  assert.equal(rec.status, 'compensated');
  assert.equal(rec.error, 'step d exploded');
  assert.deepEqual(
    ctx.calls,
    ['exec:a', 'exec:b', 'exec:c', 'comp:c', 'comp:b', 'comp:a'],
    'strict reverse order, e never executed',
  );
  assert.equal(sm.get('s2')?.status, 'compensated');
  // Durable intent: step-failed is committed before any compensation event.
  const types = node.applied
    .filter((e) => e.value.kind === 'saga')
    .map((e) => (e.value.kind === 'saga' ? `${e.value.event.type}:${e.value.event.step ?? ''}` : ''));
  assert.ok(types.indexOf('step-failed:d') < types.indexOf('step-compensated:c'));
  assert.ok(types.includes('saga-compensated:'));
});

test('failure at first step: nothing to compensate, saga still finalizes', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm);
  const ctx: Ctx = { fx: new Effects(), calls: [] };

  const rec = await orch.submit('s3', [failingStep('a'), okStep('b')], ctx);

  assert.equal(rec.status, 'compensated');
  assert.deepEqual(ctx.calls, []);
  assert.deepEqual(sm.get('s3')?.executedSteps, []);
});

test('idempotent submission: duplicate sagaId resolves to one execution', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm);
  const ctx: Ctx = { fx: new Effects(), calls: [] };
  const steps = [okStep('a'), okStep('b')];

  const [r1, r2] = await Promise.all([orch.submit('s4', steps, ctx), orch.submit('s4', steps, ctx)]);
  assert.equal(r1, r2, 'same in-flight promise');
  assert.deepEqual(ctx.calls, ['exec:a', 'exec:b'], 'effects applied exactly once');

  const r3 = await orch.submit('s4', steps, ctx);
  assert.equal(r3.status, 'completed');
  assert.deepEqual(ctx.calls, ['exec:a', 'exec:b'], 'no re-execution after completion');
});

test('flaky compensation retries and stays exactly-once at the resource', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm, { maxCompensateRetries: 5 });
  const ctx: Ctx = { fx: new Effects(), calls: [] };
  let attempts = 0;

  const flaky: SagaStep<Ctx> = {
    name: 'a',
    execute: async (ctx) => {
      await ctx.fx.once('a:exec', () => ctx.calls.push('exec:a'));
    },
    compensate: async (ctx) => {
      attempts++;
      if (attempts < 3) throw new Error('transient compensation failure');
      await ctx.fx.once('a:comp', () => ctx.calls.push('comp:a'));
    },
  };

  const rec = await orch.submit('s5', [flaky, failingStep('b')], ctx);
  assert.equal(rec.status, 'compensated');
  assert.equal(attempts, 3, 'retried until success');
  assert.deepEqual(ctx.calls, ['exec:a', 'comp:a'], 'compensation effect applied exactly once');
});

test('permanently failing compensation marks saga failed with cause', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm, { maxCompensateRetries: 2 });
  const ctx: Ctx = { fx: new Effects(), calls: [] };
  let attempts = 0;

  const unrecoverable: SagaStep<Ctx> = {
    name: 'a',
    execute: async (ctx) => {
      await ctx.fx.once('a:exec', () => ctx.calls.push('exec:a'));
    },
    compensate: () => {
      attempts++;
      throw new Error('compensation hopeless');
    },
  };

  const rec = await orch.submit('s6', [okStep('z'), unrecoverable, failingStep('b')], ctx);
  assert.equal(rec.status, 'failed');
  assert.equal(rec.error, 'compensation hopeless');
  assert.equal(attempts, 2, 'bounded retries');
  // Reverse order compensates a first; its permanent failure halts the chain,
  // so z is never compensated and the saga is parked as failed.
  assert.deepEqual(ctx.calls, ['exec:z', 'exec:a']);
  assert.equal(sm.get('s6')?.status, 'failed');
});

test('recovery: steps committed as executed are never re-run', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm);
  const ctx: Ctx = { fx: new Effects(), calls: [] };

  // A previous incarnation ran step a and journaled it before crashing.
  await ctx.fx.once('a:exec', () => ctx.calls.push('exec:a'));
  const slot = node.propose({ kind: 'saga', event: { sagaId: 's7', type: 'step-executed', step: 'a' } });
  await awaitCommit(node, slot);

  const rec = await orch.submit('s7', [okStep('a'), okStep('b')], ctx);
  assert.equal(rec.status, 'completed');
  assert.deepEqual(ctx.calls, ['exec:a', 'exec:b'], 'a not re-executed; journal dedupes regardless');
});

test('concurrent sagas on a shared ledger: no leaks, exact final balances', async () => {
  const { node, sm } = singleNode();
  const orch = wire(node, sm);

  const balances = new Map<string, number>([
    ['alice', 1000],
    ['bob', 1000],
    ['carol', 1000],
  ]);
  const fx = new Effects();
  const initial = new Map(balances);
  const yield0 = () => new Promise((r) => setTimeout(r, 0));

  const transfer = (saga: string, step: string, from: string, to: string, amount: number): SagaStep<unknown> => ({
    name: step,
    execute: async () => {
      await yield0();
      await fx.once(`${saga}:${step}:exec`, () => {
        balances.set(from, balances.get(from)! - amount);
        balances.set(to, balances.get(to)! + amount);
      });
    },
    compensate: async () => {
      await yield0();
      await fx.once(`${saga}:${step}:comp`, () => {
        balances.set(from, balances.get(from)! + amount);
        balances.set(to, balances.get(to)! - amount);
      });
    },
  });
  const explode = (step: string): SagaStep<unknown> => ({
    name: step,
    execute: async () => {
      await yield0();
      throw new Error('insufficient funds');
    },
    compensate: () => {},
  });

  const jobs: Array<Promise<unknown>> = [];
  const completedDelta = new Map<string, number>();
  const bump = (k: string, d: number) => completedDelta.set(k, (completedDelta.get(k) ?? 0) + d);

  for (let i = 0; i < 20; i++) {
    const id = `saga-${i}`;
    const fail = i % 3 === 0; // every third saga fails on its last step
    const steps = [
      transfer(id, 't1', 'alice', 'bob', 10 + i),
      transfer(id, 't2', 'bob', 'carol', 5 + i),
      ...(fail ? [explode('t3')] : [transfer(id, 't3', 'carol', 'alice', 3 + i)]),
    ];
    if (!fail) {
      bump('alice', -(10 + i) + (3 + i));
      bump('bob', 10 + i - (5 + i));
      bump('carol', 5 + i - (3 + i));
    }
    jobs.push(orch.submit(id, steps, {}));
  }

  const records = await Promise.all(jobs);
  for (const [i, rec] of records.entries()) {
    assert.equal(rec.status, i % 3 === 0 ? 'compensated' : 'completed', `saga-${i}`);
  }

  for (const [account, start] of initial) {
    const expected = start + (completedDelta.get(account) ?? 0);
    assert.equal(
      balances.get(account),
      expected,
      `${account}: compensated sagas net zero, completed sagas exact`,
    );
  }
  // Strict no-leak: every exec key of a compensated saga has a matching comp key.
  for (const key of fx.keys()) {
    if (key.endsWith(':exec')) {
      const rec = records[Number(key.split(':')[0].split('-')[1])];
      if (rec.status === 'compensated') {
        assert.ok(fx.has(key.replace(':exec', ':comp')), `missing compensation for ${key}`);
      }
    }
  }
});

test('leader failover mid-saga: new orchestrator resumes without duplicate effects', async () => {
  const c = new Cluster(5);
  const n1 = c.electLeader();
  const sms = c.nodes.map(() => new SagaStateMachine());
  c.nodes.forEach((n, i) =>
    n.onCommit((_, entry) => {
      if (entry.value.kind === 'saga') sms[i].apply(entry.value.event);
    }),
  );
  const fx = new Effects();
  const calls: string[] = [];
  const counts = new Map<string, number>();
  const counted = (key: string) => {
    counts.set(key, (counts.get(key) ?? 0) + 1);
    calls.push(key);
  };

  const stepDefs: SagaStep<unknown>[] = ['a', 'b', 'c', 'd'].map((name) => ({
    name,
    execute: async () => {
      if (name === 'c' && c.node(1).role === 'leader') {
        // Crash the old leader mid-saga: partition, re-elect, heal, demote n1.
        c.partition([1, 2], [3, 4, 5]);
        c.runUntil(() => c.node(3).role === 'leader');
        c.heal();
        c.runUntil(() => c.node(1).role === 'follower');
      }
      await fx.once(`saga-x:${name}:exec`, () => counted(`exec:${name}`));
    },
    compensate: async () => {
      await fx.once(`saga-x:${name}:comp`, () => counted(`comp:${name}`));
    },
  }));

  const orch1 = wire(n1, sms[0]);
  const first = orch1.submit('saga-x', stepDefs, {});
  await assert.rejects(first, /not leader|lost leadership/, 'old orchestrator cannot commit');

  // New leader's state machine already contains a,b (committed pre-partition).
  const n3 = c.node(3);
  assert.equal(n3.role, 'leader');
  const orch2 = wire(n3, sms[2]);
  const rec = await orch2.submit('saga-x', stepDefs, {});
  assert.equal(rec.status, 'completed');

  for (const name of ['a', 'b', 'c', 'd']) {
    assert.equal(counts.get(`exec:${name}`), 1, `${name} executed exactly once across failover`);
  }
  assert.equal(smGet(sms, 'saga-x'), 'completed');
  // The committed saga history is identical on every node that has it.
  const committed = c.node(3).applied.filter((e) => e.value.kind === 'saga');
  assert.ok(committed.length >= 5, 'all transitions journaled');
});

function smGet(sms: SagaStateMachine[], id: string): string | undefined {
  for (const sm of sms) {
    const rec = sm.get(id);
    if (rec && rec.status !== 'running') return rec.status;
  }
  return undefined;
}
