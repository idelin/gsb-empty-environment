import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  SagaFailure,
  SagaOrchestrator,
  type SagaStep,
  type TransactionSnapshot,
} from '../src/saga.ts';

interface Ctx {
  log: string[];
}

function step(
  name: string,
  opts: { failOnExecute?: boolean; compensateHook?: (attempt: number) => void } = {},
): SagaStep<Ctx, string> {
  return {
    name,
    execute(ctx) {
      ctx.log.push(`execute:${name}`);
      if (opts.failOnExecute) throw new Error(`boom in ${name}`);
      return `data:${name}`;
    },
    compensate(ctx, data, handle) {
      ctx.log.push(`compensate:${name}:${data}`);
      opts.compensateHook?.(handle.attempt);
    },
  };
}

async function settle(p: Promise<TransactionSnapshot>): Promise<TransactionSnapshot> {
  try {
    return await p;
  } catch (err) {
    assert.ok(err instanceof SagaFailure);
    return err.snapshot;
  }
}

describe('saga orchestrator: happy path', () => {
  it('executes steps in order and completes', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const ctx: Ctx = { log: [] };
    const snap = await orch.run('tx-1', [step('a'), step('b'), step('c')], ctx);
    assert.deepEqual(ctx.log, ['execute:a', 'execute:b', 'execute:c']);
    assert.equal(snap.state, 'COMPLETED');
    assert.deepEqual(
      snap.transitions.map((t) => `${t.from}->${t.to}`),
      ['PENDING->RUNNING', 'RUNNING->COMPLETED'],
    );
    assert.deepEqual(snap.steps.map((s) => s.status), ['EXECUTED', 'EXECUTED', 'EXECUTED']);
    orch.assertNoLeaks();
  });

  it('passes execute data and idempotency keys to compensation', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const ctx: Ctx = { log: [] };
    await settle(orch.run('tx-keys', [step('a'), step('b', { failOnExecute: true })], ctx));
    assert.deepEqual(ctx.log, ['execute:a', 'execute:b', 'compensate:a:data:a']);
    const snap = orch.snapshot('tx-keys');
    assert.equal(snap?.steps[0]?.idempotencyKey, 'tx-keys#0:a');
  });
});

describe('saga orchestrator: reverse compensation', () => {
  it('compensates in strict reverse order on failure', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const ctx: Ctx = { log: [] };
    const err = await orch
      .run('tx-2', [step('s1'), step('s2'), step('s3'), step('s4', { failOnExecute: true })], ctx)
      .catch((e: unknown) => e);
    assert.ok(err instanceof SagaFailure);
    assert.equal(err.stepName, 's4');
    assert.deepEqual(
      ctx.log.filter((l) => l.startsWith('compensate')),
      ['compensate:s3:data:s3', 'compensate:s2:data:s2', 'compensate:s1:data:s1'],
    );
    const snap = orch.snapshot('tx-2');
    assert.equal(snap?.state, 'COMPENSATED');
    assert.deepEqual(snap?.steps.map((s) => s.status), ['COMPENSATED', 'COMPENSATED', 'COMPENSATED']);
    assert.deepEqual(
      snap?.transitions.map((t) => `${t.from}->${t.to}`),
      ['PENDING->RUNNING', 'RUNNING->COMPENSATING', 'COMPENSATING->COMPENSATED'],
    );
    orch.assertNoLeaks();
  });

  it('does not compensate the step that failed to execute', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const ctx: Ctx = { log: [] };
    await settle(orch.run('tx-3', [step('x', { failOnExecute: true })], ctx));
    assert.deepEqual(ctx.log, ['execute:x']);
    assert.equal(orch.snapshot('tx-3')?.steps.length, 0);
    orch.assertNoLeaks();
  });

  it('retries a flaky compensation until it succeeds', async () => {
    const orch = new SagaOrchestrator<Ctx>({ compensationRetries: 3 });
    const ctx: Ctx = { log: [] };
    let attempts = 0;
    const flaky = step('flaky', {
      compensateHook: (attempt) => {
        attempts++;
        if (attempt < 2) throw new Error('transient');
        assert.equal(attempt, 2);
      },
    });
    await settle(orch.run('tx-4', [flaky, step('boom', { failOnExecute: true })], ctx));
    assert.equal(attempts, 2);
    const snap = orch.snapshot('tx-4');
    assert.equal(snap?.state, 'COMPENSATED');
    assert.equal(snap?.steps[0]?.compensateAttempts, 2);
    orch.assertNoLeaks();
  });

  it('marks COMPENSATION_FAILED but still compensates remaining steps', async () => {
    const orch = new SagaOrchestrator<Ctx>({ compensationRetries: 2 });
    const ctx: Ctx = { log: [] };
    const released: string[] = [];
    const bad = step('bad', { compensateHook: () => { throw new Error('permanent'); } });
    const good: SagaStep<Ctx, string> = {
      name: 'good',
      execute(c) { c.log.push('execute:good'); return 'res-good'; },
      compensate(_c, data) { released.push(String(data)); },
    };
    await settle(orch.run('tx-5', [good, bad, step('z', { failOnExecute: true })], ctx));
    assert.deepEqual(released, ['res-good'], 'remaining step must still be compensated');
    const snap = orch.snapshot('tx-5');
    assert.equal(snap?.state, 'COMPENSATION_FAILED');
    assert.equal(snap?.steps[1]?.status, 'COMPENSATION_FAILED');
    assert.equal(snap?.steps[1]?.compensateAttempts, 2);
    assert.equal(snap?.steps[0]?.status, 'COMPENSATED');
    assert.throws(() => orch.assertNoLeaks(), /compensation failed/);
  });
});

describe('saga orchestrator: idempotency', () => {
  it('executes a transaction id at most once under concurrent run() calls', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const ctx: Ctx = { log: [] };
    let executions = 0;
    const counted: SagaStep<Ctx, string> = {
      name: 'counted',
      async execute() {
        executions++;
        await new Promise((r) => setTimeout(r, 10));
        return 'ok';
      },
      compensate() {},
    };
    const results = await Promise.all([
      orch.run('tx-idem', [counted], ctx),
      orch.run('tx-idem', [counted], ctx),
      orch.run('tx-idem', [counted], ctx),
    ]);
    assert.equal(executions, 1);
    for (const r of results) assert.equal(r.state, 'COMPLETED');
    orch.assertNoLeaks();
  });

  it('re-running a finished id never re-executes nor re-compensates', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const ctx: Ctx = { log: [] };
    await settle(orch.run('tx-done', [step('a'), step('b', { failOnExecute: true })], ctx));
    const before = ctx.log.length;
    await settle(orch.run('tx-done', [step('a'), step('b', { failOnExecute: true })], ctx));
    assert.equal(ctx.log.length, before, 'no step may run twice for the same id');
    assert.deepEqual(
      ctx.log.filter((l) => l.startsWith('compensate')),
      ['compensate:a:data:a'],
    );
    orch.assertNoLeaks();
  });
});

describe('saga orchestrator: concurrency & leak safety', () => {
  class Pool {
    inUse = 0;
    totalAcquires = 0;
    totalReleases = 0;
    acquire(): number {
      this.inUse++;
      this.totalAcquires++;
      return this.totalAcquires;
    }
    release(): void {
      if (this.inUse <= 0) throw new Error('double release detected');
      this.inUse--;
      this.totalReleases++;
    }
  }

  it('releases every acquired resource exactly once across 200 concurrent txs', async () => {
    const orch = new SagaOrchestrator<Record<string, never>>({ compensationRetries: 1 });
    const pool = new Pool();
    const compensateCalls = new Map<string, number>();

    const makeSteps = (txId: string, failAt: number): SagaStep<Record<string, never>, number>[] =>
      Array.from({ length: 5 }, (_, i) => ({
        name: `res-${i}`,
        async execute() {
          if (i === failAt) throw new Error(`injected failure at ${i}`);
          await new Promise((r) => setTimeout(r, Math.floor(Math.random() * 3)));
          return pool.acquire();
        },
        compensate(_ctx: Record<string, never>, _data: number) {
          const key = `${txId}:${i}`;
          compensateCalls.set(key, (compensateCalls.get(key) ?? 0) + 1);
          pool.release();
        },
      }));

    let seed = 42;
    const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;

    const txs: Promise<TransactionSnapshot>[] = [];
    const failAtPerTx: number[] = [];
    for (let t = 0; t < 200; t++) {
      const failAt = rand() < 0.6 ? Math.floor(rand() * 5) : -1;
      failAtPerTx.push(failAt);
      txs.push(settle(orch.run(`tx-${t}`, makeSteps(`tx-${t}`, failAt), {})));
    }
    const snapshots = await Promise.all(txs);

    let expectedHeld = 0;
    for (let t = 0; t < 200; t++) {
      const snap = snapshots[t]!;
      const failAt = failAtPerTx[t]!;
      if (failAt === -1) {
        assert.equal(snap.state, 'COMPLETED');
        expectedHeld += 5;
      } else {
        assert.equal(snap.state, 'COMPENSATED');
        assert.equal(snap.steps.length, failAt);
        for (const s of snap.steps) {
          assert.equal(s.compensateAttempts, 1);
          const key = `tx-${t}:${s.name.split('-')[1]}`;
          assert.equal(compensateCalls.get(key), 1, `exactly-once compensation for ${key}`);
        }
      }
    }

    assert.equal(pool.inUse, expectedHeld);
    assert.equal(pool.totalAcquires, pool.totalReleases + expectedHeld);
    orch.assertNoLeaks();

    for (let i = 0; i < expectedHeld; i++) pool.release();
    assert.equal(pool.inUse, 0);
    assert.equal(pool.totalAcquires, pool.totalReleases);
  });

  it('interleaves many failing txs without cross-transaction interference', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const logs = new Map<string, string[]>();
    const makeSteps = (id: string): SagaStep<Ctx, string>[] =>
      ['p', 'q', 'r'].map((n, i) => ({
        name: n,
        async execute() {
          logs.get(id)!.push(`execute:${n}`);
          await new Promise((r) => setTimeout(r, (i * 7) % 3));
          if (n === 'r') throw new Error('fail r');
          return `${id}:${n}`;
        },
        compensate() {
          logs.get(id)!.push(`compensate:${n}`);
        },
      }));
    const runs: Promise<TransactionSnapshot>[] = [];
    for (let t = 0; t < 50; t++) {
      logs.set(`tx-${t}`, []);
      runs.push(settle(orch.run(`tx-${t}`, makeSteps(`tx-${t}`), { log: [] })));
    }
    await Promise.all(runs);
    for (const [, log] of logs) {
      assert.deepEqual(log, ['execute:p', 'execute:q', 'execute:r', 'compensate:q', 'compensate:p']);
    }
    orch.assertNoLeaks();
  });

  it('assertNoLeaks passes only when all transactions are terminal', async () => {
    const orch = new SagaOrchestrator<Ctx>();
    const ctx: Ctx = { log: [] };
    await orch.run('ok', [step('a')], ctx);
    await settle(orch.run('rolled-back', [step('b'), step('c', { failOnExecute: true })], ctx));
    assert.doesNotThrow(() => orch.assertNoLeaks());
    assert.equal(orch.listTransactions().length, 2);
  });
});
