import { ObservableValue } from "../src/observable-value";

declare const process: { exit(code?: number): never };
declare const console: {
  log(...args: unknown[]): void;
  error(...args: unknown[]): void;
};

const assert = {
  deepStrictEqual(actual: unknown, expected: unknown): void {
    const a = JSON.stringify(actual);
    const e = JSON.stringify(expected);
    if (a !== e) {
      throw new Error(`deepStrictEqual failed\n  actual:   ${a}\n  expected: ${e}`);
    }
  },
  strictEqual(actual: unknown, expected: unknown): void {
    if (!Object.is(actual, expected)) {
      throw new Error(`strictEqual failed: ${String(actual)} !== ${String(expected)}`);
    }
  },
  ok(value: unknown, message?: string): void {
    if (!value) throw new Error(message ?? `expected truthy, got ${String(value)}`);
  },
};

function assertErrors(caught: unknown, expected: unknown[]): void {
  assert.ok(caught instanceof AggregateError, "expected an AggregateError");
  const errors = (caught as AggregateError).errors;
  assert.strictEqual(errors.length, expected.length);
  errors.forEach((error: unknown, index: number) => {
    assert.ok(Object.is(error, expected[index]), `error[${index}] mismatch`);
  });
}

type Log = string[];

function recorder<T>(log: Log, name: string) {
  return (value: T, previous: T) => {
    log.push(`${name}:${JSON.stringify(previous)}->${JSON.stringify(value)}`);
  };
}

const tests: Array<[string, () => void]> = [];
function test(name: string, fn: () => void): void {
  tests.push([name, fn]);
}

test("priority order, ties broken by registration order", () => {
  const log: Log = [];
  const obs = new ObservableValue(0);
  obs.subscribe(recorder(log, "low-1"), { priority: 1 });
  obs.subscribe(recorder(log, "mid-a"), { priority: 5 });
  obs.subscribe(recorder(log, "mid-b"), { priority: 5 });
  obs.subscribe(recorder(log, "high"), { priority: 10 });
  obs.subscribe(recorder(log, "low-2")); // default priority 0

  obs.set(1);

  assert.deepStrictEqual(log, [
    "high:0->1",
    "mid-a:0->1",
    "mid-b:0->1",
    "low-1:0->1",
    "low-2:0->1",
  ]);
});

test("each round uses a fixed snapshot; listeners added mid-round join the next round", () => {
  const log: Log = [];
  const obs = new ObservableValue(0);
  let added = false;

  obs.subscribe((value, previous) => {
    log.push(`adder:${previous}->${value}`);
    if (!added) {
      added = true;
      obs.subscribe(recorder(log, "late"));
      obs.set(2); // nested update -> merged into the next round
    }
  });
  obs.subscribe(recorder(log, "plain"));

  obs.set(1);

  // Round 1 notifies (1) with the original two listeners only.
  // The nested set(2) triggers round 2 with a fresh snapshot including "late".
  assert.deepStrictEqual(log, [
    "adder:0->1",
    "plain:0->1",
    "adder:1->2",
    "plain:1->2",
    "late:1->2",
  ]);
});

test("unsubscribe immediately blocks callbacks not yet executed", () => {
  const log: Log = [];
  const obs = new ObservableValue(0);

  const unsubB = obs.subscribe(recorder(log, "B"));
  const unsubA = obs.subscribe(recorder(log, "A"), { priority: 1 }); // runs before B
  // A unsubscribes B during the round, before B's turn.
  obs.subscribe((value, previous) => {
    log.push(`killer:${previous}->${value}`);
    unsubB();
  }, { priority: 2 });

  obs.set(1);
  assert.deepStrictEqual(log, ["killer:0->1", "A:0->1"]);

  // B is fully removed; later notifications skip it too.
  obs.set(2);
  assert.deepStrictEqual(log, ["killer:0->1", "A:0->1", "killer:1->2", "A:1->2"]);

  // Unsubscribing A works as well; double-unsubscribe is a no-op.
  unsubA();
  unsubA();
  unsubB();
  obs.set(3);
  assert.deepStrictEqual(log, [
    "killer:0->1", "A:0->1",
    "killer:1->2", "A:1->2",
    "killer:2->3",
  ]);
});

test("reentrant set chains merge into sequential rounds", () => {
  const log: Log = [];
  const obs = new ObservableValue(0);

  obs.subscribe((value, previous) => {
    log.push(`stepper:${previous}->${value}`);
    if (value < 3) obs.set(value + 1); // reentrant nested update
  });
  obs.subscribe(recorder(log, "tail"));

  obs.set(1);

  // Each round completes fully before the merged nested update fires.
  assert.deepStrictEqual(log, [
    "stepper:0->1", "tail:0->1",
    "stepper:1->2", "tail:1->2",
    "stepper:2->3", "tail:2->3",
  ]);
  assert.strictEqual(obs.value, 3);
});

test("value returning to the notified value cancels the pending round", () => {
  const log: Log = [];
  const obs = new ObservableValue(0);

  obs.subscribe((value, previous) => {
    log.push(`flipper:${previous}->${value}`);
    if (value === 1) {
      obs.set(99); // move away...
      obs.set(1);  // ...and back before the round ends
    }
  });
  obs.subscribe(recorder(log, "tail"));

  obs.set(1);

  // No extra round: the value settled back to what was just notified.
  assert.deepStrictEqual(log, ["flipper:0->1", "tail:0->1"]);
  assert.strictEqual(obs.value, 1);

  // Setting the same value is a no-op.
  obs.set(1);
  assert.deepStrictEqual(log, ["flipper:0->1", "tail:0->1"]);
});

test("exceptions are collected, the round continues, and set throws AggregateError", () => {
  const log: Log = [];
  const obs = new ObservableValue(0);
  const boom1 = new Error("boom-1");
  const boom2 = new Error("boom-2");

  obs.subscribe((value) => {
    log.push("thrower-1");
    if (value === 1) throw boom1;
  }, { priority: 3 });
  obs.subscribe(recorder(log, "middle"), { priority: 2 });
  obs.subscribe((value) => {
    log.push("thrower-2");
    if (value === 1) throw boom2;
  }, { priority: 1 });
  obs.subscribe(recorder(log, "last"));

  let caught: unknown;
  try {
    obs.set(1);
  } catch (error) {
    caught = error;
  }

  // Every listener still ran despite the exceptions.
  assert.deepStrictEqual(log, ["thrower-1", "middle:0->1", "thrower-2", "last:0->1"]);
  assertErrors(caught, [boom1, boom2]);

  // Errors are cleared after being reported; a clean round does not throw.
  obs.set(2);
  assert.deepStrictEqual(log, [
    "thrower-1", "middle:0->1", "thrower-2", "last:0->1",
    "thrower-1", "middle:1->2", "thrower-2", "last:1->2",
  ]);
});

test("exceptions in a nested round are reported together", () => {
  const log: Log = [];
  const obs = new ObservableValue(0);
  const boom = new Error("nested-boom");

  obs.subscribe((value, previous) => {
    log.push(`nester:${previous}->${value}`);
    if (value === 1) obs.set(2);
  }, { priority: 1 });
  obs.subscribe((value) => {
    log.push(`fragile:${value}`);
    if (value === 2) throw boom;
  });

  let caught: unknown;
  try {
    obs.set(1);
  } catch (error) {
    caught = error;
  }

  assert.deepStrictEqual(log, [
    "nester:0->1", "fragile:1",
    "nester:1->2", "fragile:2",
  ]);
  assertErrors(caught, [boom]);
});

let failures = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (error) {
    failures++;
    console.error(`FAIL - ${name}`);
    console.error(error);
  }
}
if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  process.exit(1);
}
console.log(`${tests.length} tests passed`);
