import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  OwnershipEngine,
  replayAll,
  replayPermutations,
  assertConvergent,
  canonicalize,
  generateSigningKey,
  signMessage,
  verifySignature,
} from './index.ts';

test('公共入口可导入且端到端裁决可用', () => {
  const engine = new OwnershipEngine();
  assert.equal(engine.currentOwner('obj:missing'), null);
  assert.equal(canonicalize({ b: 1, a: 2 }), '{"a":2,"b":1}');

  const key = generateSigningKey('k#1');
  const signature = signMessage(key.privateKeyPkcs8, 'demo', { v: 1 });
  assert.equal(verifySignature(key.publicKeySpki, 'demo', { v: 1 }, signature), true);
  assert.equal(verifySignature(key.publicKeySpki, 'demo', { v: 2 }, signature), false);

  const reports = replayPermutations([]);
  assertConvergent(reports);
  assert.equal(reports[0].stateHash, replayAll([]).stateHash);
});
