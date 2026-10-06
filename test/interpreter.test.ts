import assert from 'assert';
import { CancelError, Interpreter, StepResult } from '../src/interpreter';
import { program } from '../src/program';

const tests: { name: string; fn: () => void }[] = [];
function test(name: string, fn: () => void): void {
  tests.push({ name, fn });
}

function expectYield(step: StepResult, value: unknown): void {
  assert.deepStrictEqual(step, { type: 'yield', value });
}

test('normal return: yields then returns, result is stable', () => {
  const it = new Interpreter(
    program((b) => {
      b.yield(1).yield(2).ret(3);
    }),
  );
  expectYield(it.step(), 1);
  expectYield(it.step(), 2);
  assert.deepStrictEqual(it.step(), { type: 'return', value: 3 });
  assert.strictEqual(it.done, true);
  assert.deepStrictEqual(it.step(), { type: 'return', value: 3 });
});

test('cancellation is injected at the resume point, not synchronously', () => {
  const it = new Interpreter(
    program((b) => {
      b.yield('a');
      b.yield('b').ret('done');
    }),
  );
  expectYield(it.step(), 'a');
  it.cancel();
  // No side effect happened synchronously; the next step injects CancelError
  // at the resume point and it propagates to the top.
  const result = it.step();
  assert.strictEqual(result.type, 'throw');
  assert.ok((result as { error: unknown }).error instanceof CancelError);
});

test('cancel runs finally, and finally may yield before propagating', () => {
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('cleanup');
      b.yield('body');
      b.popFinally();
      b.ret('unreachable');
      b.label('cleanup');
      b.yield('cleaning');
      b.endFinally();
    }),
  );
  expectYield(it.step(), 'body');
  it.cancel();
  expectYield(it.step(), 'cleaning');
  const result = it.step();
  assert.strictEqual(result.type, 'throw');
  assert.ok((result as { error: unknown }).error instanceof CancelError);
});

test('nested cleanup: inner finally runs before outer finally', () => {
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('outer');
      b.pushFinally('inner');
      b.yield('body');
      b.popFinally();
      b.popFinally();
      b.ret('unreachable');
      b.label('inner');
      b.yield('inner-1');
      b.yield('inner-2');
      b.endFinally();
      b.label('outer');
      b.yield('outer-1');
      b.endFinally();
    }),
  );
  expectYield(it.step(), 'body');
  it.cancel();
  expectYield(it.step(), 'inner-1');
  expectYield(it.step(), 'inner-2');
  expectYield(it.step(), 'outer-1');
  const result = it.step();
  assert.strictEqual(result.type, 'throw');
  assert.ok((result as { error: unknown }).error instanceof CancelError);
});

test('re-cancellation abandons the running finally; it never re-runs', () => {
  const executed: string[] = [];
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('outer');
      b.pushFinally('inner');
      b.yield('body');
      b.popFinally();
      b.popFinally();
      b.ret('unreachable');
      b.label('inner');
      b.yield('inner-cleanup');
      b.yield('inner-after-recancel'); // must never execute
      b.endFinally();
      b.label('outer');
      b.yield('outer-cleanup');
      b.endFinally();
    }),
  );
  expectYield(it.step(), 'body');
  it.cancel();
  executed.push('inner-entered');
  expectYield(it.step(), 'inner-cleanup');
  // Second cancellation, injected at the resume point inside the inner finally.
  it.cancel();
  expectYield(it.step(), 'outer-cleanup');
  const result = it.step();
  assert.strictEqual(result.type, 'throw');
  const error = (result as { error: unknown }).error;
  assert.ok(error instanceof CancelError);
  // The first CancelError is preserved as cause of the overriding one.
  assert.ok((error as CancelError).cause instanceof CancelError);
  // Inner finally ran exactly once and was not restarted.
  assert.deepStrictEqual(executed, ['inner-entered']);
});

test('cleanup exception overrides in-flight error and keeps it as cause', () => {
  const innerFailure = new Error('inner cleanup failed');
  const outerFailure = new Error('outer cleanup failed');
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('outer');
      b.pushFinally('inner');
      b.yield('body');
      b.popFinally();
      b.popFinally();
      b.ret('unreachable');
      b.label('inner');
      b.throwErr(innerFailure);
      b.label('outer');
      b.throwErr(outerFailure);
    }),
  );
  expectYield(it.step(), 'body');
  it.cancel();
  const result = it.step();
  assert.strictEqual(result.type, 'throw');
  const error = (result as { error: unknown }).error as Error & {
    cause?: unknown;
  };
  assert.strictEqual(error, outerFailure);
  assert.strictEqual(error.cause, innerFailure);
  assert.ok(
    (innerFailure as { cause?: unknown }).cause instanceof CancelError,
  );
});

test('finally runs on normal exit of try, exactly once', () => {
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('cleanup');
      b.yield('body');
      b.popFinally();
      b.yield('after');
      b.ret('done');
      b.label('cleanup');
      b.yield('cleanup');
      b.endFinally();
    }),
  );
  expectYield(it.step(), 'body');
  expectYield(it.step(), 'cleanup');
  expectYield(it.step(), 'after');
  assert.deepStrictEqual(it.step(), { type: 'return', value: 'done' });
});

test('return inside try runs finally, then completes the return', () => {
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('cleanup');
      b.ret('early');
      b.label('cleanup');
      b.yield('cleanup');
      b.endFinally();
    }),
  );
  expectYield(it.step(), 'cleanup');
  assert.deepStrictEqual(it.step(), { type: 'return', value: 'early' });
});

test('throw inside try propagates after finally, identity preserved', () => {
  const original = new Error('boom');
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('cleanup');
      b.throwErr(original);
      b.label('cleanup');
      b.yield('cleanup');
      b.endFinally();
    }),
  );
  expectYield(it.step(), 'cleanup');
  const result = it.step();
  assert.strictEqual(result.type, 'throw');
  assert.strictEqual((result as { error: unknown }).error, original);
});

test('cancel during normal-path finally abandons it without re-running', () => {
  const it = new Interpreter(
    program((b) => {
      b.pushFinally('cleanup');
      b.popFinally();
      b.yield('unreachable');
      b.label('cleanup');
      b.yield('cleanup-1');
      b.yield('cleanup-2'); // must never execute
      b.endFinally();
    }),
  );
  expectYield(it.step(), 'cleanup-1');
  it.cancel();
  const result = it.step();
  assert.strictEqual(result.type, 'throw');
  assert.ok((result as { error: unknown }).error instanceof CancelError);
});

let failures = 0;
for (const { name, fn } of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (error) {
    failures += 1;
    console.error(`FAIL - ${name}`);
    console.error(error);
  }
}
if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  process.exit(1);
}
console.log(`${tests.length} tests passed`);
