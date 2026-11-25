import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as api from '../src/index.ts';

test('barrel exposes the importable acceptance API and aliases', () => {
  for (const name of [
    'acceptMerge',
    'publishAccepted',
    'acceptConfigMerge',
    'verifyMerge',
    'evaluateMerge',
    'publishMerge',
    'truthOracle',
    'exhaustiveStateTruth',
    'CANONICAL_SCENARIOS',
    'doc',
    'obj',
    'leaf',
    'nil',
    'tomb',
    'ref',
    'Authority',
    'MergeEngine',
    'FACETS',
  ]) {
    assert.ok(name in api, `expected barrel export ${name}`);
  }
  assert.equal(api.acceptMerge, api.acceptConfigMerge);
  assert.equal(api.acceptMerge, api.verifyMerge);
  assert.ok(api.CANONICAL_SCENARIOS.length >= 6);
});
