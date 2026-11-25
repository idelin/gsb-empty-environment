import { test } from 'node:test';
import assert from 'node:assert/strict';
import { acceptMerge } from '../src/accept.ts';
import { CANONICAL_SCENARIOS, type ExpectedError } from '../src/truth/scenarios.ts';

function matchesError(actual: { code: string; location: { pointer: string; facet?: string } }, expected: ExpectedError): boolean {
  if (actual.code !== expected.code) return false;
  if (expected.pointer !== undefined && actual.location.pointer !== expected.pointer) return false;
  if (expected.facet !== undefined && actual.location.facet !== expected.facet) return false;
  return true;
}

for (const scenario of CANONICAL_SCENARIOS) {
  test(`${scenario.id} — ${scenario.title}`, () => {
    const result = acceptMerge(scenario.input);

    if (result.ok !== scenario.expect.ok) {
      console.error('Unexpected result for', scenario.id);
      if (!result.ok) console.error(JSON.stringify(result.errors, null, 2));
      else console.error('Expected rejection but got', JSON.stringify(result.tree, null, 2));
    }
    assert.equal(result.ok, scenario.expect.ok, `ok mismatch for ${scenario.id}`);

    if (result.ok && scenario.expect.tree) {
      assert.deepEqual(result.tree, scenario.expect.tree, `tree mismatch for ${scenario.id}`);
    }

    if (!result.ok && scenario.expect.errorIncludes) {
      for (const expected of scenario.expect.errorIncludes) {
        const found = result.errors.some((error) => matchesError(error, expected));
        if (!found) {
          console.error('Missing expected error', expected, 'in', JSON.stringify(result.errors, null, 2));
        }
        assert.ok(found, `expected error ${expected.code} at ${expected.pointer ?? '*'} for ${scenario.id}`);
      }
    }

    if (result.ok && scenario.expect.leafChecks) {
      for (const check of scenario.expect.leafChecks) {
        const leaf = result.leaves.find((item) => item.pointer === check.pointer);
        assert.ok(leaf, `missing leaf ${check.pointer} in ${scenario.id}`);
        if (!leaf) continue;
        const prov = leaf.provenance;
        if ('value' in check && check.value !== undefined) {
          assert.deepEqual(leaf.value, check.value, `value at ${check.pointer}`);
        }
        if (check.branchId) assert.equal(prov.valueDecision.branchId, check.branchId);
        if (check.authorized !== undefined) assert.equal(prov.authorized, check.authorized);
        if (check.viaIncludes) {
          const allVia = [prov.name, prov.valueDecision, prov.parent, prov.existence].flatMap((d) => d.via);
          for (const token of check.viaIncludes) {
            assert.ok(allVia.includes(token), `expected via to include ${token} at ${check.pointer}; got ${allVia.join(',')}`);
          }
        }
        if (check.exposed) assert.ok(prov.exposures.length > 0, `expected exposure at ${check.pointer}`);
      }
    }
  });
}
