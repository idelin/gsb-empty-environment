import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CancelledError,
  TaskScope,
  TaskScopeError,
  runTaskScope,
} from '../src/task-scope.ts';

const delay = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));

const waitCancelled = (scope: TaskScope): Promise<void> =>
  new Promise((resolve) => {
    if (scope.signal.aborted) {
      resolve();
      return;
    }
    scope.signal.addEventListener('abort', () => resolve(), { once: true });
  });

async function capture(fn: () => Promise<unknown>): Promise<unknown> {
  try {
    await fn();
  } catch (error) {
    return error;
  }
  throw new Error('Expected the scope to reject, but it resolved');
}

test('resolves with the body result after all children finish', async () => {
  const events: string[] = [];
  const value = await runTaskScope(async (scope) => {
    scope.spawn(async () => {
      await delay(15);
      events.push('slow child');
    });
    scope.spawn(async () => {
      await delay(5);
      events.push('fast child');
    });
    events.push('body done');
    return 42;
  });
  assert.equal(value, 42);
  assert.deepEqual(events, ['body done', 'fast child', 'slow child']);
});

test('a failing parent cancels descendants and waits for their cleanup', async () => {
  const events: string[] = [];
  const rootError = new Error('root failure');
  const error = await capture(() =>
    runTaskScope(async (scope) => {
      scope.spawn(async (child) => {
        try {
          while (!child.signal.aborted) {
            await delay(5);
          }
          events.push('child observed cancellation');
        } finally {
          await delay(20);
          events.push('child cleanup finished');
        }
      });
      await delay(10);
      throw rootError;
    }),
  );
  assert.ok(error instanceof TaskScopeError);
  assert.equal(error.cause, rootError);
  assert.equal(error.rootCause, rootError);
  assert.equal(error.failures.length, 1);
  assert.equal(error.failures[0].phase, 'run');
  // Cleanup finished before the scope settled.
  assert.deepEqual(events, ['child observed cancellation', 'child cleanup finished']);
});

test('aggregates errors stably by creation sequence; cancellation never becomes the root cause', async () => {
  const boom = new Error('boom-2');
  const cleanup3 = new Error('cleanup-3');
  const error = await capture(() =>
    runTaskScope(async (scope) => {
      // seq 1: cancelled, then its cleanup throws a *cancellation* error.
      scope.spawn(async (child) => {
        child.onCleanup(() => {
          throw new CancelledError('cancelled during cleanup');
        });
        await waitCancelled(child);
      });
      // seq 2: fails for real after 10ms, triggering the shutdown.
      scope.spawn(async () => {
        await delay(10);
        throw boom;
      });
      // seq 3: cancelled, then its cleanup throws a real error.
      scope.spawn(async (child) => {
        child.onCleanup(async () => {
          await delay(5);
          throw cleanup3;
        });
        await waitCancelled(child);
      });
      await waitCancelled(scope);
    }),
  );
  assert.ok(error instanceof TaskScopeError);
  assert.equal(error.failures.length, 3);
  // Stable ordering by creation sequence, not by completion order.
  assert.deepEqual(
    error.failures.map((f) => f.seq),
    [1, 2, 3],
  );
  assert.deepEqual(
    error.failures.map((f) => f.phase),
    ['cleanup', 'run', 'cleanup'],
  );
  assert.ok(error.failures[0].error instanceof CancelledError);
  assert.equal(error.failures[1].error, boom);
  assert.equal(error.failures[2].error, cleanup3);
  // The cancellation error has the smallest seq, yet the root cause is the
  // first real error in creation order.
  assert.equal(error.cause, boom);
});

test('cancellation of descendants is not reported as a failure', async () => {
  const real = new Error('real failure');
  const error = await capture(() =>
    runTaskScope(async (scope) => {
      scope.spawn(async (child) => {
        await waitCancelled(child);
        child.throwIfCancelled();
      });
      scope.spawn(async () => {
        await delay(10);
        throw real;
      });
      await waitCancelled(scope);
    }),
  );
  assert.ok(error instanceof TaskScopeError);
  assert.equal(error.failures.length, 1);
  assert.equal(error.failures[0].error, real);
  assert.equal(error.cause, real);
});

test('descendants spawned while the parent is closing are still awaited', async () => {
  const events: string[] = [];
  const trigger = new Error('trigger');
  const error = await capture(() =>
    runTaskScope(async (scope) => {
      // seq 1: while the whole tree is closing, its cleanup spawns a child.
      scope.spawn(async (child) => {
        child.onCleanup(() => {
          events.push('child cleanup: spawn grandchild');
          child.spawn(async (grand) => {
            events.push(
              grand.signal.aborted
                ? 'grandchild runs while cancelled'
                : 'grandchild runs',
            );
            grand.onCleanup(async () => {
              await delay(5);
              events.push('grandchild cleanup: spawn more');
              // Great-grandchild into the still-closing child scope.
              child.spawn(async () => {
                events.push('great-grandchild ran');
              });
              // Late task into the closing root scope.
              scope.spawn(async () => {
                events.push('late root task ran');
              });
            });
            await delay(5);
            events.push('grandchild body done');
          });
        });
        await waitCancelled(child);
        events.push('child body ended');
      });
      // seq 2: fails and starts the shutdown.
      scope.spawn(async () => {
        await delay(10);
        throw trigger;
      });
      await waitCancelled(scope);
    }),
  );
  assert.ok(error instanceof TaskScopeError);
  assert.equal(error.cause, trigger);
  assert.equal(error.failures.length, 1);
  // Every late-spawned descendant ran to completion before the scope settled.
  assert.deepEqual(events, [
    'child body ended',
    'child cleanup: spawn grandchild',
    'grandchild runs while cancelled',
    'grandchild body done',
    'grandchild cleanup: spawn more',
    'great-grandchild ran',
    'late root task ran',
  ]);
});

test('task handles reject with the child failure', async () => {
  const boom = new Error('child boom');
  let observed: unknown;
  const error = await capture(() =>
    runTaskScope(async (scope) => {
      const handle = scope.spawn(async () => {
        await delay(5);
        throw boom;
      });
      observed = await handle.result.catch((e: unknown) => e);
    }),
  );
  assert.ok(observed instanceof TaskScopeError);
  assert.equal((observed as TaskScopeError).cause, boom);
  assert.ok(error instanceof TaskScopeError);
  assert.equal((error as TaskScopeError).cause, boom);
});

test('spawning into a closed scope throws', async () => {
  let captured: TaskScope | null = null;
  await runTaskScope(async (scope) => {
    captured = scope;
  });
  const closed = captured as TaskScope;
  assert.throws(() => {
    closed.spawn(async () => undefined);
  }, /closed/);
});
