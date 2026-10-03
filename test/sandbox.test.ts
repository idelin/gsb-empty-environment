import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  BudgetExceeded,
  evaluate,
  MemoryLimitExceeded,
  ParseError,
  TimeoutExceeded,
  ValidationError,
} from '../src/sandbox.ts';

describe('expression semantics', () => {
  it('evaluates arithmetic with correct precedence', () => {
    assert.equal(evaluate('1 + 2 * 3').value, 7);
    assert.equal(evaluate('(1 + 2) * 3').value, 9);
    assert.equal(evaluate('10 % 3 + 1').value, 2);
    assert.equal(evaluate('-4 + 10 / 2').value, 1);
  });

  it('evaluates boolean tenant rules against variables', () => {
    const variables = { age: 20, country: 'CN', vip: true };
    assert.equal(
      evaluate('age >= 18 && (country == "CN" || vip)', { variables }).value,
      true,
    );
    assert.equal(evaluate('age < 18 || !vip', { variables }).value, false);
  });

  it('supports whitelisted string built-ins', () => {
    const variables = { name: 'Alice' };
    assert.equal(
      evaluate('startsWith(name, "A") && len(name) > 2', { variables }).value,
      true,
    );
    assert.equal(evaluate('upper("ab") + lower("CD")').value, 'ABcd');
  });

  it('short-circuits logical operators', () => {
    assert.equal(evaluate('false && (1 / 0 > 0)').value, false);
    assert.equal(evaluate('true || (1 / 0 > 0)').value, true);
  });

  it('eval() re-evaluates a string expression inside the sandbox', () => {
    assert.equal(evaluate('eval("1 + 2 * 3")').value, 7);
  });
});

describe('AST security validation', () => {
  it('rejects calls to unauthorized functions', () => {
    assert.throws(() => evaluate('system("id")'), ValidationError);
    assert.throws(() => evaluate('exec("rm -rf /")'), /unauthorized function 'exec'/);
  });

  it('offers no member-access syntax to reach host objects', () => {
    assert.throws(() => evaluate('process.exit(1)'), ParseError);
    assert.throws(() => evaluate('globalThis.constructor'), ParseError);
  });

  it('rejects unknown identifiers', () => {
    assert.throws(() => evaluate('constructor'), /Unknown identifier 'constructor'/);
    assert.throws(() => evaluate('secret + 1'), ValidationError);
  });

  it('rejects structurally abusive nesting bombs at parse time', () => {
    const bomb = '('.repeat(500) + '1' + ')'.repeat(500);
    assert.throws(() => evaluate(bomb), ParseError);
  });

  it('rejects oversized expressions', () => {
    const wide = Array(400).fill('1').join('+');
    assert.throws(() => evaluate(wide), ValidationError);
  });
});

describe('execution-limit guard', () => {
  it('ACCEPTANCE: eval(self) is force-stopped at the 1000-step budget', () => {
    let caught: unknown;
    try {
      evaluate('eval(self)'); // DEFAULT_LIMITS.maxSteps === 1000
    } catch (err) {
      caught = err;
    }
    assert.ok(caught instanceof BudgetExceeded, `expected BudgetExceeded, got ${caught}`);
    assert.equal(caught.maxSteps, 1000);
    assert.ok(caught.stepsUsed > 1000);

    // Host process is healthy afterwards: no deadlock, no corrupted state.
    assert.equal(evaluate('1 + 1').value, 2);
  });

  it('enforces custom per-tenant step budgets', () => {
    const chain = Array(100).fill('1').join('+');
    assert.throws(
      () => evaluate(chain, { limits: { maxSteps: 100 } }),
      BudgetExceeded,
    );
    assert.equal(evaluate(chain, { limits: { maxSteps: 1000 } }).value, 100);
  });

  it('enforces the memory-allocation budget on string growth', () => {
    const chunk = '"' + 'x'.repeat(50) + '"';
    const source = Array(20).fill(chunk).join(' + ');
    assert.throws(
      () => evaluate(source, { limits: { maxMemory: 500 } }),
      MemoryLimitExceeded,
    );
  });

  it('enforces the wall-clock timeout', () => {
    assert.throws(
      () => evaluate('1 + 1', { limits: { timeoutMs: 0 } }),
      TimeoutExceeded,
    );
  });

  it('reports resource usage on successful runs', () => {
    const result = evaluate('1 + 2 * 3');
    assert.ok(result.stepsUsed > 0 && result.stepsUsed <= 1000);
    assert.ok(result.memoryUsed > 0);
  });
});

describe('multi-tenant isolation', () => {
  it('does not leak variables between evaluations', () => {
    assert.equal(evaluate('x + 1', { variables: { x: 41 } }).value, 42);
    assert.throws(() => evaluate('x + 1'), /Unknown identifier 'x'/);
  });

  it('gives each evaluation an independent budget', () => {
    const chain = Array(100).fill('1').join('+');
    assert.throws(() => evaluate(chain, { limits: { maxSteps: 50 } }), BudgetExceeded);
    // A later tenant with a fresh budget is unaffected by the previous failure.
    assert.equal(evaluate(chain, { limits: { maxSteps: 1000 } }).value, 100);
  });
});
