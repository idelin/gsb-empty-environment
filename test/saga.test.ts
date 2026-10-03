import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Saga, SagaError } from '../src/saga.ts';
import type { SagaEvent, SagaStep } from '../src/saga.ts';

interface Ctx {
  log: string[];
}

const noSleep = (): Promise<void> => Promise.resolve();

function step(name: string, overrides: Partial<SagaStep<Ctx>> = {}): SagaStep<Ctx> {
  return {
    name,
    execute: (ctx) => {
      ctx.log.push(`execute:${name}`);
    },
    compensate: (ctx) => {
      ctx.log.push(`compensate:${name}`);
    },
    ...overrides,
  };
}

test('happy path executes steps in order and succeeds', async () => {
  const ctx: Ctx = { log: [] };
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga.addStep(step('a')).addStep(step('b')).addStep(step('c'));
  const status = await saga.run(ctx);
  assert.equal(status, 'succeeded');
  assert.equal(saga.status, 'succeeded');
  assert.deepEqual(ctx.log, ['execute:a', 'execute:b', 'execute:c']);
  assert.equal(saga.pendingStepCount, 0);
});

test('failure rolls back completed steps in reverse order', async () => {
  const ctx: Ctx = { log: [] };
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga
    .addStep(step('a'))
    .addStep(step('b'))
    .addStep(
      step('c', {
        execute: () => {
          ctx.log.push('execute:c');
          throw new Error('boom');
        },
      }),
    )
    .addStep(step('d'));
  const status = await saga.run(ctx);
  assert.equal(status, 'rolledback');
  assert.equal(saga.status, 'rolledback');
  assert.deepEqual(ctx.log, [
    'execute:a',
    'execute:b',
    'execute:c',
    'compensate:b',
    'compensate:a',
  ]);
  assert.equal(saga.pendingStepCount, 0);
  assert.equal(saga.failedCompensationCount, 0);
});

test('failing first step rolls back with no compensation', async () => {
  const ctx: Ctx = { log: [] };
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga.addStep(
    step('a', {
      execute: () => {
        throw new Error('early');
      },
    }),
  );
  assert.equal(await saga.run(ctx), 'rolledback');
  assert.deepEqual(ctx.log, []);
});

test('compensation retries until it succeeds', async () => {
  const ctx: Ctx = { log: [] };
  const events: SagaEvent[] = [];
  let attempts = 0;
  const saga = new Saga<Ctx>({ sleep: noSleep, maxCompensationRetries: 5 });
  saga.onEvent((event) => events.push(event));
  saga
    .addStep(
      step('flaky', {
        compensate: () => {
          attempts += 1;
          if (attempts < 3) throw new Error('transient');
          ctx.log.push('compensate:flaky');
        },
      }),
    )
    .addStep(
      step('x', {
        execute: () => {
          throw new Error('fail forward');
        },
      }),
    );
  const status = await saga.run(ctx);
  assert.equal(status, 'rolledback');
  assert.equal(attempts, 3);
  assert.deepEqual(ctx.log, ['execute:flaky', 'compensate:flaky']);
  const retries = events.filter((e) => e.type === 'compensation-retry');
  assert.deepEqual(
    retries.map((e) => e.attempt),
    [1, 2],
  );
  assert.equal(saga.failedCompensationCount, 0);
});

test('exhausted compensation retries park the failure; resume recovers (eventual consistency)', async () => {
  const ctx: Ctx = { log: [] };
  let compensateCalls = 0;
  let healthy = false;
  const saga = new Saga<Ctx>({ sleep: noSleep, maxCompensationRetries: 2 });
  saga
    .addStep(
      step('a', {
        compensate: () => {
          compensateCalls += 1;
          if (!healthy) throw new Error('store still down');
          ctx.log.push('compensate:a');
        },
      }),
    )
    .addStep(
      step('b', {
        execute: () => {
          throw new Error('forward failure');
        },
      }),
    );

  await assert.rejects(saga.run(ctx), (error: unknown) => {
    assert.ok(error instanceof SagaError);
    assert.equal(error.compensationFailures.length, 1);
    assert.equal(error.compensationFailures[0].step, 'a');
    return true;
  });
  assert.equal(saga.status, 'failed');
  assert.equal(compensateCalls, 3); // 1 initial + 2 retries
  assert.equal(saga.failedCompensationCount, 1);

  healthy = true;
  const recovered = await saga.resumeCompensation(ctx);
  assert.equal(recovered, 'rolledback');
  assert.equal(saga.status, 'rolledback');
  assert.equal(saga.failedCompensationCount, 0);
  assert.deepEqual(ctx.log, ['execute:a', 'compensate:a']);
});

test('concurrent run() calls share one execution (idempotency)', async () => {
  const ctx: Ctx = { log: [] };
  let executions = 0;
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga.addStep(
    step('a', {
      execute: async () => {
        executions += 1;
        await new Promise((resolve) => setTimeout(resolve, 20));
      },
    }),
  );
  const results = await Promise.all([saga.run(ctx), saga.run(ctx), saga.run(ctx), saga.run(ctx)]);
  assert.deepEqual(results, ['succeeded', 'succeeded', 'succeeded', 'succeeded']);
  assert.equal(executions, 1);
});

test('run() after success is a no-op returning the cached result', async () => {
  const ctx: Ctx = { log: [] };
  let executions = 0;
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga.addStep(
    step('a', {
      execute: () => {
        executions += 1;
      },
    }),
  );
  await saga.run(ctx);
  await saga.run(ctx);
  assert.equal(await saga.run(ctx), 'succeeded');
  assert.equal(executions, 1);
});

test('concurrent runs with a failing step compensate exactly once', async () => {
  const ctx: Ctx = { log: [] };
  let compensations = 0;
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga
    .addStep(
      step('a', {
        execute: async () => {
          await new Promise((resolve) => setTimeout(resolve, 10));
        },
        compensate: async () => {
          compensations += 1;
          await new Promise((resolve) => setTimeout(resolve, 5));
        },
      }),
    )
    .addStep(
      step('b', {
        execute: () => {
          throw new Error('boom');
        },
      }),
    );
  const results = await Promise.all([saga.run(ctx), saga.run(ctx), saga.run(ctx)]);
  assert.deepEqual(results, ['rolledback', 'rolledback', 'rolledback']);
  assert.equal(compensations, 1);
});

test('abort signal between steps triggers rollback of completed work', async () => {
  const ctx: Ctx = { log: [] };
  const controller = new AbortController();
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga
    .addStep(
      step('a', {
        execute: () => {
          ctx.log.push('execute:a');
          controller.abort(new Error('cancelled'));
        },
      }),
    )
    .addStep(step('b'));
  const status = await saga.run(ctx, { signal: controller.signal });
  assert.equal(status, 'rolledback');
  assert.deepEqual(ctx.log, ['execute:a', 'compensate:a']);
});

test('steps cannot be added after the saga starts; duplicates are rejected', async () => {
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga.addStep(step('a'));
  assert.throws(() => saga.addStep(step('a')), /duplicate/);
  await saga.run({ log: [] });
  assert.throws(() => saga.addStep(step('b')), /once the saga has started/);
});

test('no leaks: terminal state releases buffers and listeners', async () => {
  const ctx: Ctx = { log: [] };
  const saga = new Saga<Ctx>({ sleep: noSleep });
  const events: SagaEvent[] = [];
  const unsubscribe = saga.onEvent((event) => events.push(event));
  saga.addStep(step('a')).addStep(step('b'));
  await saga.run(ctx);
  assert.equal(saga.pendingStepCount, 0);
  assert.equal(saga.failedCompensationCount, 0);
  assert.deepEqual(
    events.map((e) => e.type),
    ['step-executed', 'step-executed', 'succeeded'],
  );
  unsubscribe();
  assert.equal(saga.listenerCount, 0);
  saga.onEvent(() => {
    throw new Error('must be cleared by dispose');
  });
  saga.dispose();
  assert.equal(saga.listenerCount, 0);
  assert.equal(saga.pendingStepCount, 0);
});

test('failed saga keeps parked compensations until recovered', async () => {
  const ctx: Ctx = { log: [] };
  const saga = new Saga<Ctx>({ sleep: noSleep, maxCompensationRetries: 0 });
  saga
    .addStep(
      step('a', {
        compensate: () => {
          throw new Error('permanent');
        },
      }),
    )
    .addStep(
      step('b', {
        execute: () => {
          throw new Error('forward');
        },
      }),
    );
  await assert.rejects(saga.run(ctx), SagaError);
  assert.equal(saga.failedCompensationCount, 1);
  // resumeCompensation keeps failing but must not throw away bookkeeping
  const status = await saga.resumeCompensation(ctx);
  assert.equal(status, 'failed');
  assert.equal(saga.failedCompensationCount, 1);
  assert.equal(saga.pendingStepCount, 0);
});

test('event stream reflects the full rollback lifecycle', async () => {
  const ctx: Ctx = { log: [] };
  const events: SagaEvent[] = [];
  const saga = new Saga<Ctx>({ sleep: noSleep });
  saga.onEvent((event) => events.push(event));
  saga
    .addStep(step('a'))
    .addStep(
      step('b', {
        execute: () => {
          throw new Error('nope');
        },
      }),
    );
  await saga.run(ctx);
  assert.deepEqual(
    events.map((e) => `${e.type}:${e.step ?? ''}`),
    ['step-executed:a', 'step-compensated:a', 'rolledback:'],
  );
});
