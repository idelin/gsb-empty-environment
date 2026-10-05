import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CancellationError,
  TaskScope,
  TaskScopeError,
  runTaskScope,
} from '../src/task-scope.ts';

const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));
const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

function waitForAbort(signal: AbortSignal): Promise<never> {
  return new Promise((_, reject) => {
    const reason = (): unknown =>
      signal.reason instanceof Error ? signal.reason : new CancellationError();
    if (signal.aborted) {
      reject(reason());
      return;
    }
    signal.addEventListener('abort', () => reject(reason()), { once: true });
  });
}

test('returns the body result and runs cleanups on success', async () => {
  const events: string[] = [];
  const result = await runTaskScope(async (scope) => {
    const a = scope.spawn(async (ctx) => {
      ctx.onCleanup(() => {
        events.push('cleanup-a');
      });
      return 1;
    });
    const b = scope.spawn(async (ctx) => {
      ctx.onCleanup(() => {
        events.push('cleanup-b1');
      });
      ctx.onCleanup(() => {
        events.push('cleanup-b2');
      });
      await sleep(1);
      return 2;
    });
    assert.equal(await a, 1);
    assert.equal(await b, 2);
    return 'done';
  });
  assert.equal(result, 'done');
  assert.deepEqual(events, ['cleanup-a', 'cleanup-b2', 'cleanup-b1']);
});

test('parent failure cancels descendants and waits for their cleanup', async () => {
  const root = new Error('parent blew up');
  const events: string[] = [];
  let caught: unknown;
  try {
    await runTaskScope(async (scope) => {
      scope.spawn(async (ctx) => {
        ctx.onCleanup(async () => {
          await sleep(10);
          events.push('child-cleanup-done');
        });
        await waitForAbort(ctx.signal);
      });
      await tick();
      throw root;
    });
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof TaskScopeError);
  assert.equal(caught.rootCause, root);
  assert.equal(caught.cause, root);
  // Cleanup finished before the scope settled.
  assert.deepEqual(events, ['child-cleanup-done']);
  // The child's CancellationError is not aggregated.
  assert.equal(caught.errors.length, 1);
  assert.deepEqual(caught.errors[0], { seq: 0, phase: 'body', error: root });
});

test('child failure cancels its siblings and remains the root cause', async () => {
  const boom = new Error('child boom');
  const events: string[] = [];
  let caught: unknown;
  try {
    await runTaskScope(async (scope) => {
      scope.spawn(async () => {
        await tick();
        throw boom;
      });
      scope.spawn(async (ctx) => {
        ctx.onCleanup(() => {
          events.push('sibling-cleanup');
        });
        await waitForAbort(ctx.signal);
      });
      await waitForAbort(scope.signal);
    });
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof TaskScopeError);
  assert.equal(caught.rootCause, boom);
  assert.deepEqual(events, ['sibling-cleanup']);
  assert.equal(caught.errors.length, 1);
  assert.equal(caught.errors[0].error, boom);
});

test('aggregates errors in creation order, root cause is chronological first', async () => {
  const cleanupError = new Error('cleanup-of-task-1');
  const bodyError = new Error('body-of-task-3');
  let caught: unknown;
  try {
    await runTaskScope(async (scope) => {
      scope.spawn(async (ctx) => {
        ctx.onCleanup(async () => {
          await sleep(15);
          throw cleanupError;
        });
        await waitForAbort(ctx.signal);
      });
      scope.spawn(async (ctx) => {
        await waitForAbort(ctx.signal);
      });
      scope.spawn(async () => {
        await sleep(1);
        throw bodyError;
      });
      await waitForAbort(scope.signal);
    });
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof TaskScopeError);
  // Task 3 fails first chronologically -> root cause, even though task 1's
  // cleanup error sorts earlier by creation sequence number.
  assert.equal(caught.rootCause, bodyError);
  assert.deepEqual(
    caught.errors.map((entry) => [entry.seq, entry.phase]),
    [
      [1, 'cleanup'],
      [3, 'body'],
    ],
  );
  assert.equal(caught.errors[0].error, cleanupError);
  assert.equal(caught.errors[1].error, bodyError);
});

test('cancellation errors from cleanup never override the root cause', async () => {
  const root = new Error('root');
  let caught: unknown;
  try {
    await runTaskScope(async (scope) => {
      scope.spawn(async (ctx) => {
        ctx.onCleanup(() => {
          throw new CancellationError();
        });
        await waitForAbort(ctx.signal);
      });
      scope.spawn(async (ctx) => {
        ctx.onCleanup(() => {
          const abortLike = new Error('aborted');
          abortLike.name = 'AbortError';
          throw abortLike;
        });
        await waitForAbort(ctx.signal);
      });
      await tick();
      throw root;
    });
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof TaskScopeError);
  assert.equal(caught.rootCause, root);
  assert.equal(caught.errors.length, 1);
  assert.equal(caught.errors[0].error, root);
});

test('child keeps spawning descendants while the scope is closing', async () => {
  const failure = new Error('parent blew up');
  const grandchildFailure = new Error('grandchild failed during close');
  const events: string[] = [];
  let caught: unknown;
  const run = runTaskScope(async (scope) => {
    scope.spawn(async (ctx) => {
      ctx.onCleanup(async () => {
        events.push('child-cleanup-start');
        // The scope is already closing, but spawning must still work.
        scope.spawn(async (grand) => {
          events.push('grandchild-body');
          assert.equal(grand.signal.aborted, true);
          grand.onCleanup(async () => {
            await sleep(10);
            events.push('grandchild-cleanup');
            // One more level, spawned while closing.
            scope.spawn(async (great) => {
              great.onCleanup(() => {
                events.push('great-grandchild-cleanup');
              });
              await waitForAbort(great.signal).catch(() => {});
            });
          });
        });
        // A descendant spawned during close can still fail for real.
        scope.spawn(async () => {
          await sleep(1);
          throw grandchildFailure;
        });
        await sleep(5);
        events.push('child-cleanup-end');
      });
      await waitForAbort(ctx.signal);
    });
    throw failure;
  });
  try {
    await run;
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof TaskScopeError);
  assert.equal(caught.rootCause, failure);
  // The scope waited for every descendant spawned while closing.
  assert.deepEqual(events, [
    'child-cleanup-start',
    'grandchild-body',
    'child-cleanup-end',
    'grandchild-cleanup',
    'great-grandchild-cleanup',
  ]);
  // The real error from the grandchild is aggregated, ordered by seq.
  assert.equal(caught.errors.length, 2);
  assert.deepEqual(caught.errors[0], { seq: 0, phase: 'body', error: failure });
  assert.equal(caught.errors[1].error, grandchildFailure);
  assert.equal(caught.errors[1].phase, 'body');
  assert.ok(caught.errors[1].seq > 1);
});

test('close waits for in-flight tasks that ignore the abort signal', async () => {
  const events: string[] = [];
  await runTaskScope(async (scope) => {
    scope.spawn(async (ctx) => {
      ctx.onCleanup(async () => {
        await sleep(5);
        events.push('cleanup-done');
      });
      await sleep(10);
      events.push('body-done');
    });
    // Body returns immediately; close begins while the child still runs.
  });
  assert.deepEqual(events, ['body-done', 'cleanup-done']);
});

test('handle.cancel cancels a single task without failing the scope', async () => {
  const events: string[] = [];
  const result = await runTaskScope(async (scope) => {
    const handle = scope.spawn(async (ctx) => {
      ctx.onCleanup(() => {
        events.push('cancelled-cleanup');
      });
      await waitForAbort(ctx.signal);
    });
    await tick();
    handle.cancel();
    await handle.settled;
    return 'ok';
  });
  assert.equal(result, 'ok');
  assert.deepEqual(events, ['cancelled-cleanup']);
});

test('external cancel aborts descendants and close rejects with CancellationError', async () => {
  const scope = new TaskScope();
  const events: string[] = [];
  scope.spawn(async (ctx) => {
    ctx.onCleanup(() => {
      events.push('cleaned');
    });
    await waitForAbort(ctx.signal);
  });
  await tick();
  scope.cancel();
  await assert.rejects(scope.close(), (error: unknown) => {
    assert.ok(error instanceof CancellationError);
    return true;
  });
  assert.deepEqual(events, ['cleaned']);
  assert.equal(scope.isClosed, true);
});

test('spawn after the scope has closed throws', async () => {
  let scopeRef: TaskScope | undefined;
  await runTaskScope(async (scope) => {
    scopeRef = scope;
  });
  assert.ok(scopeRef);
  assert.throws(() => scopeRef.spawn(async () => {}), /already closed/);
});

test('TaskScopeError exposes a readable name and message', async () => {
  const root = new Error('kaput');
  let caught: unknown;
  try {
    await runTaskScope(async () => {
      throw root;
    });
  } catch (error) {
    caught = error;
  }
  assert.ok(caught instanceof TaskScopeError);
  assert.equal(caught.name, 'TaskScopeError');
  assert.match(caught.message, /task #0 \(body\): Error: kaput/);
});
