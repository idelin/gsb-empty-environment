import { test } from 'node:test';
import assert from 'node:assert/strict';
import { VirtualClock } from '../src/clock.ts';
import { PeriodicScheduler, type RunContext } from '../src/scheduler.ts';

/** Let queued microtasks (promise continuations) run. */
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));

function deferred() {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test('fires on a fixed grid anchored at startAt', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  new PeriodicScheduler(
    clock,
    (ctx) => {
      runs.push(ctx);
    },
    { periodMs: 100 },
  );

  clock.advanceBy(350);
  await flush();

  assert.deepEqual(
    runs.map((r) => r.scheduledAt),
    [100, 200, 300],
  );
  assert.ok(runs.every((r) => r.kind === 'tick' && r.startedAt === r.scheduledAt));
});

test('long task: missed triggers coalesce into one catch-up, then grid resumes', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  const blocker = deferred();
  let calls = 0;
  new PeriodicScheduler(
    clock,
    async (ctx) => {
      runs.push(ctx);
      if (calls++ === 0) await blocker.promise; // first run overruns
    },
    { periodMs: 100 },
  );

  clock.advanceBy(100); // tick@100 starts and blocks
  assert.equal(runs.length, 1);
  assert.equal(runs[0].kind, 'tick');

  clock.advanceBy(150); // now 250: grid point 200 passes while busy
  blocker.resolve();
  await flush();

  // Exactly one catch-up run, immediately at 250, covering grid point 200.
  assert.equal(runs.length, 2);
  assert.equal(runs[1].kind, 'catch-up');
  assert.equal(runs[1].missedCount, 1);
  assert.equal(runs[1].startedAt, 250);
  assert.equal(runs[1].scheduledAt, 200);

  // Scheduling rejoins the original grid: next tick at 300, not 350.
  clock.advanceBy(50);
  await flush();
  assert.equal(runs.length, 3);
  assert.equal(runs[2].kind, 'tick');
  assert.equal(runs[2].scheduledAt, 300);
  assert.equal(runs[2].startedAt, 300);

  clock.advanceBy(200); // ticks at 400 and 500, still on the original grid
  await flush();
  assert.deepEqual(
    runs.map((r) => r.scheduledAt),
    [100, 200, 300, 400, 500],
  );
});

test('multiple missed grid points coalesce into a single catch-up', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  const blocker = deferred();
  let calls = 0;
  new PeriodicScheduler(
    clock,
    async (ctx) => {
      runs.push(ctx);
      if (calls++ === 0) await blocker.promise;
    },
    { periodMs: 100 },
  );

  clock.advanceBy(100); // tick@100 blocks
  clock.advanceBy(450); // now 550: grid points 200..500 pass while busy
  blocker.resolve();
  await flush();

  assert.equal(runs.length, 2);
  assert.equal(runs[1].kind, 'catch-up');
  assert.equal(runs[1].missedCount, 4);
  assert.equal(runs[1].scheduledAt, 500);
  assert.equal(runs[1].startedAt, 550);

  clock.advanceBy(50); // next tick back on grid at 600
  await flush();
  assert.equal(runs.length, 3);
  assert.deepEqual(runs[2], { kind: 'tick', scheduledAt: 600, startedAt: 600, missedCount: 0 });
});

test('pause/resume: nothing fires while paused and nothing accumulates', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  const scheduler = new PeriodicScheduler(
    clock,
    (ctx) => {
      runs.push(ctx);
    },
    { periodMs: 100 },
  );

  clock.advanceBy(150); // tick@100 ran; now 150
  await flush();
  assert.equal(runs.length, 1);

  scheduler.pause();
  assert.equal(scheduler.isPaused, true);
  assert.equal(clock.pendingCount, 0); // pending wakeup was cancelled

  clock.advanceBy(250); // now 400: grid points 200, 300, 400 pass while paused
  await flush();
  assert.equal(runs.length, 1); // nothing fired during pause

  scheduler.resume();
  await flush();
  assert.equal(runs.length, 1); // no burst of accumulated triggers after resume
  assert.equal(scheduler.nextRunAt, 500); // simply waits for the next grid point

  clock.advanceBy(100); // now 500
  await flush();
  assert.equal(runs.length, 2);
  assert.deepEqual(runs[1], { kind: 'tick', scheduledAt: 500, startedAt: 500, missedCount: 0 });
});

test('pause while a callback is in flight: settle does not schedule, resume drops elapsed grid points', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  const blocker = deferred();
  const scheduler = new PeriodicScheduler(
    clock,
    async (ctx) => {
      runs.push(ctx);
      if (runs.length === 1) await blocker.promise;
    },
    { periodMs: 100 },
  );

  clock.advanceBy(100); // tick@100 starts and blocks
  scheduler.pause();
  clock.advanceBy(300); // now 400, still blocked and paused
  blocker.resolve();
  await flush();
  assert.equal(runs.length, 1); // settling while paused schedules nothing

  clock.advanceBy(50); // now 450
  scheduler.resume(); // grid points up to 400 are dropped
  clock.advanceBy(50); // now 500
  await flush();
  assert.equal(runs.length, 2);
  assert.deepEqual(runs[1], { kind: 'tick', scheduledAt: 500, startedAt: 500, missedCount: 0 });
});

test('long task failure: error is reported once, one catch-up runs, grid continues', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  const errors: unknown[] = [];
  const blocker = deferred();
  let calls = 0;
  const failure = new Error('boom');
  new PeriodicScheduler(
    clock,
    async (ctx) => {
      runs.push(ctx);
      if (calls++ === 0) {
        await blocker.promise;
        throw failure; // long-running task fails after overrunning
      }
    },
    { periodMs: 100, onError: (error) => errors.push(error) },
  );

  clock.advanceBy(100); // tick@100 starts and blocks
  clock.advanceBy(250); // now 350: grid points 200, 300 pass while busy
  blocker.resolve();
  await flush();

  assert.deepEqual(errors, [failure]);
  assert.equal(runs.length, 2); // failure does not stop the coalesced catch-up
  assert.equal(runs[1].kind, 'catch-up');
  assert.equal(runs[1].missedCount, 2);
  assert.equal(runs[1].startedAt, 350);

  clock.advanceBy(50); // back on grid at 400
  await flush();
  assert.equal(runs.length, 3);
  assert.deepEqual(runs[2], { kind: 'tick', scheduledAt: 400, startedAt: 400, missedCount: 0 });
});

test('synchronously throwing task reports the error and keeps the schedule', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  const errors: unknown[] = [];
  let calls = 0;
  new PeriodicScheduler(
    clock,
    (ctx) => {
      runs.push(ctx);
      if (calls++ === 0) throw new Error('sync boom');
    },
    { periodMs: 100, onError: (error) => errors.push(error) },
  );

  clock.advanceBy(100);
  await flush();
  assert.equal(errors.length, 1);
  assert.equal((errors[0] as Error).message, 'sync boom');

  clock.advanceBy(100);
  await flush();
  assert.equal(runs.length, 2);
  assert.equal(runs[1].scheduledAt, 200);
});

test('stop while a callback is executing: callback finishes, nothing more fires', async () => {
  const clock = new VirtualClock();
  const runs: RunContext[] = [];
  const blocker = deferred();
  let completed = false;
  const scheduler = new PeriodicScheduler(
    clock,
    async (ctx) => {
      runs.push(ctx);
      await blocker.promise;
      completed = true;
    },
    { periodMs: 100 },
  );

  clock.advanceBy(100); // tick@100 starts and blocks
  assert.equal(scheduler.isRunning, true);

  scheduler.stop();
  assert.equal(scheduler.isStopped, true);
  assert.equal(scheduler.isRunning, true); // still executing
  assert.equal(clock.pendingCount, 0); // no wakeup left behind

  clock.advanceBy(1000); // grid points pass; nothing new fires
  await flush();
  assert.equal(runs.length, 1);

  blocker.resolve();
  await scheduler.idle(); // in-flight callback runs to completion
  assert.equal(completed, true);
  assert.equal(scheduler.isRunning, false);

  clock.advanceBy(1000);
  await flush();
  assert.equal(runs.length, 1); // stopped scheduler stays silent
});

test('idle() resolves immediately when nothing is in flight', async () => {
  const clock = new VirtualClock();
  const scheduler = new PeriodicScheduler(clock, () => {}, { periodMs: 100 });
  await scheduler.idle();
  clock.advanceBy(100);
  await scheduler.idle();
});
