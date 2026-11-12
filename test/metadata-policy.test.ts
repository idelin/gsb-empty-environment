import test from 'node:test';
import assert from 'node:assert/strict';
import {
  combineMetadataPolicies,
  isSubsetOf,
} from '../src/metadata-policy.ts';
import { TrustChainError } from '../src/errors.ts';
import type { MetadataPolicy } from '../src/types.ts';

test('isSubsetOf handles primitives and structural values', () => {
  assert.equal(isSubsetOf([1, 2], [1, 2, 3]), true);
  assert.equal(isSubsetOf([1, 4], [1, 2, 3]), false);
  assert.equal(isSubsetOf([{ a: 1 }], [{ a: 1 }, { b: 2 }]), true);
  assert.equal(isSubsetOf([], [1]), true);
});

test('combines anchor-to-leaf policies by intersection', () => {
  const anchor: MetadataPolicy = {
    rp: { scope: { subset_of: ['openid', 'email', 'profile'] } },
  };
  const intermediate: MetadataPolicy = {
    rp: { scope: { subset_of: ['openid', 'email'] } },
  };
  // Input order is leaf-side first; combination must apply anchor first.
  const combined = combineMetadataPolicies([intermediate, anchor]);
  assert.deepEqual(combined.rp.scope.subset_of, ['openid', 'email']);
});

test('throws when a subordinate reintroduces a forbidden value', () => {
  const anchor: MetadataPolicy = {
    rp: { scope: { subset_of: ['openid'] } },
  };
  const loosened: MetadataPolicy = {
    rp: { scope: { subset_of: ['openid', 'profile'] } },
  };
  assert.throws(
    () => combineMetadataPolicies([loosened, anchor]),
    (error: unknown) => {
      assert.ok(error instanceof TrustChainError);
      assert.equal(error.code, 'POLICY_LOOSENED');
      return true;
    },
  );
});

test('a first-level subset_of is accepted without a superior restriction', () => {
  const only: MetadataPolicy = {
    rp: { scope: { subset_of: ['openid'] } },
  };
  const combined = combineMetadataPolicies([only]);
  assert.deepEqual(combined.rp.scope.subset_of, ['openid']);
});

test('keeps metadata types independent when merging policies', () => {
  const anchor: MetadataPolicy = {
    rp: { scope: { subset_of: ['x', 'y'] } },
    op: { grant: { subset_of: [1, 2] } },
  };
  const subordinate: MetadataPolicy = {
    rp: { scope: { subset_of: ['x'] } },
  };
  const combined = combineMetadataPolicies([subordinate, anchor]);
  assert.deepEqual(combined.rp.scope.subset_of, ['x']);
  assert.deepEqual(combined.op.grant.subset_of, [1, 2]);
});

test('rejects add operator introducing a forbidden member', () => {
  const anchor: MetadataPolicy = {
    rp: { scope: { subset_of: ['openid', 'email'] } },
  };
  const loosened: MetadataPolicy = {
    rp: { scope: { add: ['offline_access'] } },
  };
  assert.throws(
    () => combineMetadataPolicies([loosened, anchor]),
    (error: unknown) => {
      assert.ok(error instanceof TrustChainError);
      assert.equal(error.code, 'POLICY_LOOSENED');
      return true;
    },
  );
});
