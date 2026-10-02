import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  RuleSandbox,
  BudgetExceeded,
  MemoryExceeded,
  EvaluationTimeoutError,
  UnauthorizedFunctionError,
  UnknownIdentifierError,
  ValidationError,
  EvaluationError,
} from '../src/index.ts';

const sandbox = new RuleSandbox();

function capture(fn: () => unknown): Error {
  try {
    fn();
  } catch (err) {
    return err as Error;
  }
  throw new Error('expected the function to throw, but it returned');
}

test('evaluates arithmetic with correct precedence', () => {
  assert.equal(sandbox.evaluate('1 + 2 * 3'), 7);
  assert.equal(sandbox.evaluate('(1 + 2) * 3'), 9);
  assert.equal(sandbox.evaluate('10 % 3 + 2 * 4 - 6 / 2'), 6);
  assert.equal(sandbox.evaluate('-5 + abs(-3)'), -2);
});

test('evaluates boolean logic with short-circuiting', () => {
  assert.equal(sandbox.evaluate('true && false || true'), true);
  assert.equal(sandbox.evaluate('1 < 2 && 3 >= 3 && 4 != 5'), true);
  assert.equal(sandbox.evaluate('false && abs("boom")'), false); // right side never runs
  assert.equal(sandbox.evaluate('true || abs("boom")'), true);
  assert.equal(sandbox.evaluate('!(1 == 2)'), true);
});

test('evaluates against tenant-supplied variables', () => {
  const vars = { balance: 120, limit: 100, tier: 'gold' };
  assert.equal(sandbox.evaluate('balance > limit && tier == "gold"', vars), true);
  assert.equal(sandbox.evaluate('balance - limit', vars), 20);
});

test('supports whitelisted string builtins', () => {
  assert.equal(sandbox.evaluate('concat("foo", "bar")'), 'foobar');
  assert.equal(sandbox.evaluate('len(repeat("ab", 4))'), 8);
  assert.equal(sandbox.evaluate('max(1, 9, 4) + min(2, 5)'), 11);
});

test('rejects calls to unauthorized / host functions', () => {
  assert.throws(() => sandbox.evaluate('exit(1)'), UnauthorizedFunctionError);
  assert.throws(() => sandbox.evaluate('require("fs")'), UnauthorizedFunctionError);
  assert.throws(() => sandbox.evaluate('setTimeout("x", 0)'), UnauthorizedFunctionError);
  const err = capture(() => sandbox.evaluate('process(1)'));
  assert.ok(err instanceof UnauthorizedFunctionError);
  assert.equal((err as { functionName: string }).functionName, 'process');
});

test('member access / assignment syntax cannot even be tokenized', () => {
  assert.throws(() => sandbox.evaluate('process.exit(1)'), ValidationError);
  assert.throws(() => sandbox.evaluate('a.b'), ValidationError);
  assert.throws(() => sandbox.evaluate('x = 1'), ValidationError);
  assert.throws(() => sandbox.evaluate('globalThis["process"]'), ValidationError);
});

test('rejects unknown identifiers', () => {
  assert.throws(() => sandbox.evaluate('secret + 1'), UnknownIdentifierError);
});

test('rejects pathologically deep nesting without crashing the host', () => {
  const bomb = '('.repeat(10_000) + '1' + ')'.repeat(10_000);
  assert.throws(() => sandbox.evaluate(bomb), ValidationError);
});

test('ACCEPTANCE: eval(self) is force-aborted at the 1000-step budget', () => {
  const guarded = new RuleSandbox({ maxSteps: 1000, timeoutMs: 10_000 });
  const started = Date.now();
  const err = capture(() => guarded.evaluate('eval(self)'));
  const elapsed = Date.now() - started;

  assert.ok(err instanceof BudgetExceeded, `expected BudgetExceeded, got ${err}`);
  assert.match(err.message, /BudgetExceeded/);
  assert.equal((err as BudgetExceeded).limit, 1000);
  assert.equal((err as { stepsUsed: number }).stepsUsed, 1001, 'aborted exactly one step past the 1000-step budget');
  // Host stayed healthy: no deadlock, no runaway memory, prompt abort.
  assert.ok(elapsed < 2000, `abort took too long (${elapsed}ms)`);
  const { heapUsed } = process.memoryUsage();
  assert.ok(heapUsed < 512 * 1024 * 1024, `host heap suspiciously large: ${heapUsed}`);
});

test('host remains fully functional after the malicious evaluation', () => {
  const guarded = new RuleSandbox({ maxSteps: 1000 });
  for (let i = 0; i < 5; i++) {
    assert.throws(() => guarded.evaluate('eval(self)'), BudgetExceeded);
  }
  assert.equal(sandbox.evaluate('40 + 2'), 42);
});

test('memory budget aborts oversized allocations before they happen', () => {
  const tight = new RuleSandbox({ maxMemoryBytes: 1024 });
  assert.throws(() => tight.evaluate('repeat("ab", 100000)'), MemoryExceeded);
  assert.throws(() => tight.evaluate('concat(repeat("x", 900), repeat("y", 900))'), MemoryExceeded);
});

test('wall-clock timeout aborts long-running evaluations', () => {
  const impatient = new RuleSandbox({ maxSteps: 1_000_000, timeoutMs: 0 });
  const longExpr = Array.from({ length: 500 }, () => '1').join('+');
  assert.throws(() => impatient.evaluate(longExpr), EvaluationTimeoutError);
});

test('custom whitelisted functions run inside the budget', () => {
  const custom = new RuleSandbox({
    functions: {
      double: (args) => {
        const [n] = args;
        if (typeof n !== 'number') throw new EvaluationError('double expects a number');
        return n * 2;
      },
    },
  });
  assert.equal(custom.evaluate('double(21) + 1'), 43);
});

test('step budget is exact and deterministic', () => {
  const tiny = new RuleSandbox({ maxSteps: 3 });
  assert.equal(tiny.evaluate('1 + 2'), 3); // 2 nodes: binary + ... fits
  assert.throws(() => tiny.evaluate('1 + 2 + 3 + 4'), BudgetExceeded);
});
