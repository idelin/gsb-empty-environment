import { VirtualClock } from "../src/clock";
import { Scheduler, TaskInfo } from "../src/scheduler";

// ---------- tiny async test runner ----------

type TestFn = () => void | Promise<void>;
const tests: Array<{ name: string; fn: TestFn }> = [];
function test(name: string, fn: TestFn): void {
  tests.push({ name, fn });
}

function assert(cond: boolean, message: string): void {
  if (!cond) throw new Error("assertion failed: " + message);
}

function assertEqual<T>(actual: T, expected: T, what: string): void {
  if (actual !== expected) {
    throw new Error(
      what + ": expected " + JSON.stringify(expected) + ", got " + JSON.stringify(actual)
    );
  }
}

function assertDeepEqual<T>(actual: T, expected: T, what: string): void {
  const a = JSON.stringify(actual);
  const e = JSON.stringify(expected);
  if (a !== e) throw new Error(what + ": expected " + e + ", got " + a);
}

/** Let pending promise continuations (task completions) run. */
function flush(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

interface Deferred {
  promise: Promise<void>;
  resolve: () => void;
  reject: (err: unknown) => void;
}

function deferred(): Deferred {
  let resolve!: () => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

interface RunRecord {
  kind: string;
  at: number;
}

function recorder(): { runs: RunRecord[]; task: (info: TaskInfo) => void } {
  const runs: RunRecord[] = [];
  return {
    runs,
    task: (info: TaskInfo) => {
      runs.push({ kind: info.kind, at: info.startedAt });
    },
  };
}

// ---------- tests ----------

test("fires on a fixed grid anchored at schedule time", () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const rec = recorder();
  scheduler.schedule(rec.task, 100);

  clock.advance(1000);
  assertDeepEqual(
    rec.runs,
    [
      { kind: "grid", at: 100 },
      { kind: "grid", at: 200 },
      { kind: "grid", at: 300 },
      { kind: "grid", at: 400 },
      { kind: "grid", at: 500 },
      { kind: "grid", at: 600 },
      { kind: "grid", at: 700 },
      { kind: "grid", at: 800 },
      { kind: "grid", at: 900 },
      { kind: "grid", at: 1000 },
    ],
    "grid runs"
  );
});

test("fixed start: quick tasks never drift from the grid", async () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: number[] = [];
  const gates: Deferred[] = [];
  scheduler.schedule(() => {
    runs.push(clock.now());
    const gate = deferred();
    gates.push(gate);
    return gate.promise;
  }, 100);

  clock.advance(100); // t=100: first run starts, still pending
  clock.advance(30); // t=130
  gates[0].resolve();
  await flush();
  // Next fire must be at the grid point 200, not 130+100=230.
  clock.advance(100); // t=230
  assertDeepEqual(runs, [100, 200], "run times");
  assert(gates.length === 2, "second run started");
});

test("overrun: multiple missed triggers coalesce into one catch-up, then back on grid", async () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: RunRecord[] = [];
  const gates: Deferred[] = [];
  scheduler.schedule((info) => {
    runs.push({ kind: info.kind, at: info.startedAt });
    const gate = deferred();
    gates.push(gate);
    return gate.promise;
  }, 100);

  clock.advance(100); // t=100: grid run starts (pending)
  clock.advance(250); // t=350: grid points 200 and 300 pass while running
  assertEqual(runs.length, 1, "still one run while task is pending");

  gates[0].resolve();
  await flush();
  // Exactly one coalesced catch-up, immediately at completion time.
  assertDeepEqual(
    runs,
    [
      { kind: "grid", at: 100 },
      { kind: "catch-up", at: 350 },
    ],
    "coalesced catch-up"
  );

  gates[1].resolve();
  await flush();
  // After the catch-up the schedule returns to the original grid (next: 400).
  clock.advance(100); // t=450
  assertDeepEqual(
    runs,
    [
      { kind: "grid", at: 100 },
      { kind: "catch-up", at: 350 },
      { kind: "grid", at: 400 },
    ],
    "back on grid"
  );
});

test("catch-up does not chain: grid points passed during catch-up are skipped", async () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: RunRecord[] = [];
  const gates: Deferred[] = [];
  scheduler.schedule((info) => {
    runs.push({ kind: info.kind, at: info.startedAt });
    const gate = deferred();
    gates.push(gate);
    return gate.promise;
  }, 100);

  clock.advance(100); // grid run at 100
  clock.advance(150); // t=250, grid point 200 missed
  gates[0].resolve();
  await flush(); // catch-up starts at 250
  clock.advance(150); // t=400, grid point 300 passes during catch-up
  gates[1].resolve();
  await flush();
  // No second catch-up; next run is the next grid point after 400 -> 500.
  clock.advance(150); // t=550
  assertDeepEqual(
    runs,
    [
      { kind: "grid", at: 100 },
      { kind: "catch-up", at: 250 },
      { kind: "grid", at: 500 },
    ],
    "no chained catch-up"
  );
});

test("long task failure: rejection is reported, one catch-up runs, grid continues", async () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: RunRecord[] = [];
  const errors: unknown[] = [];
  const gates: Deferred[] = [];
  scheduler.schedule(
    (info) => {
      runs.push({ kind: info.kind, at: info.startedAt });
      const gate = deferred();
      gates.push(gate);
      return gate.promise;
    },
    100,
    { onError: (err) => errors.push(err) }
  );

  clock.advance(100); // long task starts at 100
  clock.advance(300); // t=400: grid points 200, 300, 400 missed
  const boom = new Error("boom");
  gates[0].reject(boom);
  await flush();

  assertEqual(errors.length, 1, "one error reported");
  assertEqual(errors[0], boom, "error object");
  assertDeepEqual(
    runs,
    [
      { kind: "grid", at: 100 },
      { kind: "catch-up", at: 400 },
    ],
    "catch-up after failure"
  );

  gates[1].resolve();
  await flush();
  clock.advance(150); // t=550 -> grid fire at 500
  assertDeepEqual(
    runs,
    [
      { kind: "grid", at: 100 },
      { kind: "catch-up", at: 400 },
      { kind: "grid", at: 500 },
    ],
    "grid continues after failure"
  );
});

test("sync throw is reported and scheduling continues on grid", () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: number[] = [];
  const errors: unknown[] = [];
  scheduler.schedule(
    () => {
      runs.push(clock.now());
      if (runs.length === 1) throw new Error("sync boom");
    },
    100,
    { onError: (err) => errors.push(err) }
  );

  clock.advance(300);
  assertDeepEqual(runs, [100, 200, 300], "runs continue after throw");
  assertEqual(errors.length, 1, "one error");
});

test("pause/resume: paused time does not accumulate, phase preserved", () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const rec = recorder();
  const handle = scheduler.schedule(rec.task, 100);

  clock.advance(150); // t=150: fired at 100
  assertEqual(rec.runs.length, 1, "one run before pause");
  handle.pause();
  assertEqual(handle.state, "paused", "state paused");

  clock.advance(500); // t=650: grid points 200..600 pass while paused
  assertEqual(rec.runs.length, 1, "no runs while paused");

  handle.resume();
  assertEqual(handle.state, "active", "state active");
  clock.advance(49); // t=699
  assertEqual(rec.runs.length, 1, "no catch-up burst after resume");
  clock.advance(1); // t=700: grid shifted by 500 -> next point is 700
  assertEqual(rec.runs.length, 2, "run at shifted grid point");
  assertEqual(rec.runs[1].at, 700, "run time 700");

  clock.advance(300); // t=1000 -> fires at 800, 900, 1000
  assertDeepEqual(
    rec.runs.map((r) => r.at),
    [100, 700, 800, 900, 1000],
    "grid after resume"
  );
});

test("pause while executing: completion during pause triggers nothing until resume", async () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: RunRecord[] = [];
  const gates: Deferred[] = [];
  const handle = scheduler.schedule((info) => {
    runs.push({ kind: info.kind, at: info.startedAt });
    const gate = deferred();
    gates.push(gate);
    return gate.promise;
  }, 100);

  clock.advance(100); // task starts at 100
  clock.advance(50); // t=150
  handle.pause();
  clock.advance(100); // t=250: grid point 200 passes while paused
  gates[0].resolve(); // task completes during pause
  await flush();
  assertEqual(runs.length, 1, "no catch-up while paused");

  handle.resume(); // t=250, anchor shifts by 100 -> grid 300, 400, ...
  clock.advance(100); // t=350
  assertDeepEqual(
    runs,
    [
      { kind: "grid", at: 100 },
      { kind: "grid", at: 300 },
    ],
    "resumed on shifted grid"
  );
});

test("missed trigger before pause still yields one catch-up on resume", async () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: RunRecord[] = [];
  const gates: Deferred[] = [];
  const handle = scheduler.schedule((info) => {
    runs.push({ kind: info.kind, at: info.startedAt });
    const gate = deferred();
    gates.push(gate);
    return gate.promise;
  }, 100);

  clock.advance(100); // task starts at 100
  clock.advance(150); // t=250: grid point 200 missed while running
  handle.pause();
  clock.advance(300); // t=550
  gates[0].resolve(); // completes while paused
  await flush();
  assertEqual(runs.length, 1, "nothing fires while paused");

  handle.resume(); // pending miss surfaces as a single catch-up now
  await flush();
  assertDeepEqual(
    runs,
    [
      { kind: "grid", at: 100 },
      { kind: "catch-up", at: 550 },
    ],
    "one catch-up on resume"
  );
});

test("stop while callback is executing: it finishes, no catch-up, no more fires", async () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const runs: RunRecord[] = [];
  const gates: Deferred[] = [];
  const handle = scheduler.schedule((info) => {
    runs.push({ kind: info.kind, at: info.startedAt });
    const gate = deferred();
    gates.push(gate);
    return gate.promise;
  }, 100);

  clock.advance(100); // task starts at 100
  clock.advance(150); // t=250: grid point 200 missed while running
  assert(handle.executing, "executing");
  handle.stop();
  assertEqual(handle.state, "stopped", "state stopped");

  gates[0].resolve(); // in-flight callback completes
  await flush();
  assertDeepEqual(runs, [{ kind: "grid", at: 100 }], "no catch-up after stop");

  clock.advance(1000); // t=1250
  assertDeepEqual(runs, [{ kind: "grid", at: 100 }], "no further runs after stop");
  assert(!handle.executing, "not executing anymore");
});

test("stop is idempotent and prevents future grid fires", () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const rec = recorder();
  const handle = scheduler.schedule(rec.task, 100);

  clock.advance(100);
  handle.stop();
  handle.stop();
  handle.pause();
  handle.resume();
  clock.advance(500);
  assertEqual(rec.runs.length, 1, "only the pre-stop run");
  assertEqual(handle.state, "stopped", "still stopped");
});

test("pause/resume are no-ops in the wrong state", () => {
  const clock = new VirtualClock();
  const scheduler = new Scheduler(clock);
  const rec = recorder();
  const handle = scheduler.schedule(rec.task, 100);

  handle.resume(); // not paused: no-op
  handle.pause();
  handle.pause(); // already paused: no-op
  clock.advance(250); // t=250, paused the whole time
  assertEqual(rec.runs.length, 0, "nothing fired");
  handle.resume();
  clock.advance(100); // t=350, anchor shifted to 250 -> fire at 350
  assertDeepEqual(
    rec.runs.map((r) => r.at),
    [350],
    "single run on shifted grid"
  );
});

// ---------- runner ----------

async function main(): Promise<void> {
  let failed = 0;
  for (const t of tests) {
    try {
      await t.fn();
      process.stdout.write("PASS " + t.name + "\n");
    } catch (err) {
      failed++;
      process.stdout.write("FAIL " + t.name + "\n  " + String(err) + "\n");
    }
  }
  process.stdout.write(
    "\n" + (tests.length - failed) + "/" + tests.length + " tests passed\n"
  );
  if (failed > 0) process.exit(1);
}

main();
