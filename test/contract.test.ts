import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  AbstractState,
  type FlowModel,
  PreconditionViolationError,
  UnknownExitError,
  UnverifiedContractError,
  type VerificationResult,
  type VerificationSuccess,
  compileDeclaration,
  enumeratePaths,
  invoke,
  neg,
  pos,
  verifyContract,
} from '../src/index.ts';

function requireSuccess(result: VerificationResult): VerificationSuccess {
  if (!result.ok) {
    assert.fail(result.failures.map((failure) => failure.reason).join('; '));
  }
  return result;
}

const SANITIZED = pos('buffer.sanitized');
const CLOSED = pos('buffer.closed');
const TRUSTED = pos('input.trusted');
const HEALTHY = pos('io.healthy');
const FLUSH_OK = pos('io.flushOk');

/**
 * Declaration of a sanitizer:
 *   - input constraint: the input is trusted;
 *   - return tag "ok":   the buffer must be sanitized;
 *   - exception "io-error": the buffer is closed, and the declaration makes
 *     NO sanitization promise on the exceptional path.
 */
const declaration: FlowModel = {
  name: 'sanitize',
  entry: 'd-entry',
  nodes: {
    'd-entry': { kind: 'entry', id: 'd-entry', input: [TRUSTED], next: 'd-split' },
    'd-split': {
      kind: 'branch',
      id: 'd-split',
      condition: HEALTHY,
      thenNext: 'd-ok',
      elseNext: 'd-io-error',
    },
    'd-ok': { kind: 'return', id: 'd-ok', tag: 'ok', requires: [SANITIZED] },
    'd-io-error': { kind: 'throw', id: 'd-io-error', label: 'io-error', requires: [CLOSED] },
  },
};

/**
 * Buggy implementation: when IO is unhealthy it still takes a return-"ok"
 * path that never establishes sanitization. A second branch covers the
 * exceptional exit, where a previously established annotation is
 * invalidated before the throw.
 */
const buggyImplementation: FlowModel = {
  name: 'sanitize-impl',
  entry: 'i-entry',
  nodes: {
    'i-entry': { kind: 'entry', id: 'i-entry', input: [TRUSTED], next: 'i-health' },
    'i-health': {
      kind: 'branch',
      id: 'i-health',
      condition: HEALTHY,
      thenNext: 'i-purify',
      elseNext: 'i-buggy-ok',
    },
    'i-purify': { kind: 'effect', id: 'i-purify', establishes: [SANITIZED], next: 'i-flush' },
    'i-flush': {
      kind: 'branch',
      id: 'i-flush',
      condition: FLUSH_OK,
      thenNext: 'i-ok',
      elseNext: 'i-fail',
    },
    'i-ok': { kind: 'return', id: 'i-ok', tag: 'ok', requires: [] },
    'i-fail': {
      kind: 'invalidate',
      id: 'i-fail',
      invalidates: [SANITIZED],
      next: 'i-close',
    },
    'i-close': { kind: 'effect', id: 'i-close', establishes: [CLOSED], next: 'i-throw' },
    'i-throw': { kind: 'throw', id: 'i-throw', label: 'io-error', requires: [] },
    'i-buggy-ok': { kind: 'return', id: 'i-buggy-ok', tag: 'ok', requires: [] },
  },
};

/** Fixed implementation: the unhealthy branch purifies before returning. */
const fixedImplementation: FlowModel = {
  ...buggyImplementation,
  nodes: {
    ...buggyImplementation.nodes,
    'i-health': {
      kind: 'branch',
      id: 'i-health',
      condition: HEALTHY,
      thenNext: 'i-purify',
      elseNext: 'i-repair',
    },
    'i-repair': { kind: 'effect', id: 'i-repair', establishes: [SANITIZED], next: 'i-buggy-ok' },
  },
};

test('declaration compiles to precondition, return and throw obligations', () => {
  const contract = compileDeclaration(declaration);
  assert.deepEqual(contract.precondition, [TRUSTED]);
  assert.deepEqual(contract.returns.ok, [SANITIZED]);
  assert.deepEqual(contract.throws['io-error'], [CLOSED]);
});

test('finite control flow is enumerated per structural path', () => {
  const paths = enumeratePaths(buggyImplementation);
  assert.equal(paths.length, 3);
  assert.ok(paths.every((path) => {
    const last = path.nodes[path.nodes.length - 1];
    return last.kind === 'return' || last.kind === 'throw';
  }));
});

test('buggy implementation is rejected and yields no verified summary', () => {
  const result = verifyContract(declaration, buggyImplementation);
  assert.equal(result.ok, false);
  if (result.ok) {
    return;
  }
  assert.equal(result.summary, null);
  assert.equal(result.failures.length, 1);
  const failure = result.failures[0];
  assert.equal(failure.reason, 'return-unsatisfied');
  assert.ok(failure.path.includes('i-buggy-ok'));
  assert.deepEqual(failure.missing, [SANITIZED]);
});

test('fixed implementation is verified path-by-path and mints a summary', () => {
  const result = requireSuccess(verifyContract(declaration, fixedImplementation));
  assert.equal(result.pathCount, 3);
  assert.equal(result.feasiblePathCount, 3);
  assert.equal(result.summary.contract, 'sanitize');
  assert.deepEqual(result.summary.returnTags, ['ok']);
  assert.ok(
    result.summary.returns.ok.some(
      (literal) => literal.atom === 'buffer.sanitized' && literal.polarity,
    ),
  );
});

test('invalidated annotations do not propagate through the exception exit', () => {
  const result = requireSuccess(verifyContract(declaration, fixedImplementation));
  const postException = result.summary.throws['io-error'];
  assert.ok(postException.some((literal) => literal.atom === 'buffer.closed' && literal.polarity));
  assert.ok(!postException.some((literal) => literal.atom === 'buffer.sanitized'));
});

test('callers may only consume a verified summary (runtime brand gate)', () => {
  assert.throws(
    () => invoke({} as never, [TRUSTED], { kind: 'return', tag: 'ok' }),
    UnverifiedContractError,
  );
  assert.throws(
    () => invoke(declaration as never, { kind: 'return', tag: 'ok' }),
    UnverifiedContractError,
  );
});

test('verified summary enforces the precondition on the caller', () => {
  const { summary } = requireSuccess(verifyContract(declaration, fixedImplementation));
  assert.throws(
    () => invoke(summary, [], { kind: 'return', tag: 'ok' }),
    PreconditionViolationError,
  );

  // Three-argument overload: caller supplies the input constraint.
  const guaranteed = invoke(summary, [TRUSTED], { kind: 'return', tag: 'ok' });
  assert.ok(guaranteed.some((literal) => literal.atom === 'buffer.sanitized' && literal.polarity));

  // Two-argument overload: caller relies on the precondition being assumed.
  const exceptional = invoke(summary, { kind: 'throw', label: 'io-error' });
  assert.ok(exceptional.some((literal) => literal.atom === 'buffer.closed' && literal.polarity));
  assert.ok(!exceptional.some((literal) => literal.atom === 'buffer.sanitized'));
});

test('unknown exits are refused even with a verified summary', () => {
  const { summary } = requireSuccess(verifyContract(declaration, fixedImplementation));
  assert.throws(
    () => invoke(summary, [TRUSTED], { kind: 'return', tag: 'partial' }),
    UnknownExitError,
  );
});

test('infeasible paths are excluded instead of being counted as witnesses', () => {
  const returnOnlyDeclaration: FlowModel = {
    name: 'sanitize-return-only',
    entry: 'r-entry',
    nodes: {
      'r-entry': { kind: 'entry', id: 'r-entry', input: [TRUSTED], next: 'r-ok' },
      'r-ok': { kind: 'return', id: 'r-ok', tag: 'ok', requires: [SANITIZED] },
    },
  };
  const implementation: FlowModel = {
    name: 'guarded',
    entry: 'g-entry',
    nodes: {
      'g-entry': { kind: 'entry', id: 'g-entry', input: [TRUSTED], next: 'g-assume' },
      // The assumption contradicts the negative branch, making it infeasible.
      'g-assume': { kind: 'assume', id: 'g-assume', literals: [HEALTHY], next: 'g-branch' },
      'g-branch': {
        kind: 'branch',
        id: 'g-branch',
        condition: HEALTHY,
        thenNext: 'g-effect',
        elseNext: 'g-dead',
      },
      'g-effect': { kind: 'effect', id: 'g-effect', establishes: [SANITIZED], next: 'g-ok' },
      'g-ok': { kind: 'return', id: 'g-ok', tag: 'ok', requires: [] },
      'g-dead': { kind: 'return', id: 'g-dead', tag: 'ok', requires: [] },
    },
  };
  const result = requireSuccess(verifyContract(returnOnlyDeclaration, implementation));
  assert.equal(result.pathCount, 2);
  assert.equal(result.feasiblePathCount, 1);
});

test('cyclic models are rejected instead of being enumerated forever', () => {
  const cyclic: FlowModel = {
    name: 'cyclic',
    entry: 'c-entry',
    nodes: {
      'c-entry': { kind: 'entry', id: 'c-entry', input: [], next: 'c-loop' },
      'c-loop': { kind: 'assume', id: 'c-loop', literals: [HEALTHY], next: 'c-loop' },
    },
  };
  assert.throws(() => enumeratePaths(cyclic), /non-finite control flow/);
});

test('mutually recursive formula/state modules share one interpretation', () => {
  const state = AbstractState.empty().effect(SANITIZED);
  assert.equal(state.entails(SANITIZED), true);
  assert.equal(state.entails(neg('buffer.sanitized')), false);

  const invalidated = state.invalidate([SANITIZED]);
  assert.equal(invalidated.entails(SANITIZED), false);
  assert.equal(invalidated.entails(neg('buffer.sanitized')), false);

  assert.equal(AbstractState.empty().assume(SANITIZED)?.assume(neg('buffer.sanitized')), null);
});
