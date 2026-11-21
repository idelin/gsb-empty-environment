/**
 * End-to-end usage of the contract-verification package.
 *
 * Run with: node --experimental-strip-types examples/sanitizer.ts
 */
import {
  type FlowModel,
  invoke,
  neg,
  pos,
  verifyContract,
} from '../src/index.ts';

const declaration: FlowModel = {
  name: 'sanitize',
  entry: 'entry',
  nodes: {
    entry: { kind: 'entry', id: 'entry', input: [pos('input.trusted')], next: 'split' },
    split: {
      kind: 'branch',
      id: 'split',
      condition: pos('io.healthy'),
      thenNext: 'ok',
      elseNext: 'io-error',
    },
    ok: { kind: 'return', id: 'ok', tag: 'ok', requires: [pos('buffer.sanitized')] },
    'io-error': {
      kind: 'throw',
      id: 'io-error',
      label: 'io-error',
      requires: [pos('buffer.closed')],
    },
  },
};

// Implementation whose unhealthy branch purifies too late: the annotation
// is invalidated on the exceptional path and a bare return remains.
const implementation: FlowModel = {
  name: 'sanitize-impl',
  entry: 'impl-entry',
  nodes: {
    'impl-entry': {
      kind: 'entry',
      id: 'impl-entry',
      input: [pos('input.trusted')],
      next: 'impl-purify',
    },
    'impl-purify': {
      kind: 'effect',
      id: 'impl-purify',
      establishes: [pos('buffer.sanitized')],
      next: 'impl-split',
    },
    'impl-split': {
      kind: 'branch',
      id: 'impl-split',
      condition: pos('io.healthy'),
      thenNext: 'impl-ok',
      elseNext: 'impl-invalidate',
    },
    'impl-ok': { kind: 'return', id: 'impl-ok', tag: 'ok', requires: [] },
    'impl-invalidate': {
      kind: 'invalidate',
      id: 'impl-invalidate',
      invalidates: [pos('buffer.sanitized')],
      next: 'impl-close',
    },
    'impl-close': {
      kind: 'effect',
      id: 'impl-close',
      establishes: [pos('buffer.closed')],
      next: 'impl-throw',
    },
    'impl-throw': { kind: 'throw', id: 'impl-throw', label: 'io-error', requires: [] },
  },
};

const result = verifyContract(declaration, implementation);
if (!result.ok) {
  throw new Error(`verification failed: ${JSON.stringify(result.failures)}`);
}
const summary = result.summary;
const returnGuarantee = invoke(summary, [pos('input.trusted')], { kind: 'return', tag: 'ok' });
const throwGuarantee = invoke(summary, { kind: 'throw', label: 'io-error' });

console.log('return "ok" guarantees:', returnGuarantee);
console.log('exception "io-error" guarantees:', throwGuarantee);
console.log('sanitized survives exception?',
  throwGuarantee.some((literal) => literal.atom === 'buffer.sanitized'));
console.log('negative literal helper:', neg('buffer.sanitized'));
