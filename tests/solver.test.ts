import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { solveGrant } from '../src/index.ts';
import type {
  GrantRoleLimitExceeded,
  GrantSuccess,
  GrantUncoverable,
} from '../src/index.ts';

const perms = (prefix: string, from: number, to: number): string[] =>
  Array.from({ length: to - from + 1 }, (_, i) => `${prefix}${from + i}`);

describe('exact optimality (no greedy approximation)', () => {
  it('beats greedy max-coverage on role count', () => {
    // Greedy max-coverage picks Trap1 (7 perms), then Trap2 (4 new),
    // then Trap3 (1 new) = 3 roles. Optimal is Left + Right = 2 roles.
    const result = solveGrant({
      roles: [
        { name: 'Trap1', permissions: [...perms('p', 1, 4), ...perms('p', 7, 9)] },
        { name: 'Trap2', permissions: ['p5', 'p6', 'p10', 'p11'] },
        { name: 'Trap3', permissions: ['p12'] },
        { name: 'Left', permissions: perms('p', 1, 6) },
        { name: 'Right', permissions: perms('p', 7, 12) },
      ],
      required: perms('p', 1, 12),
    });
    assert.equal(result.ok, true);
    const success = result as GrantSuccess;
    assert.equal(success.roleCount, 2);
    assert.deepEqual(success.roles, ['Left', 'Right']);
    assert.deepEqual(success.excessPermissions, []);
  });

  it('prefers fewer roles even with more excess permissions', () => {
    const result = solveGrant({
      roles: [
        { name: 'Broad', permissions: ['a', 'b', 'extra1', 'extra2'] },
        { name: 'OnlyA', permissions: ['a'] },
        { name: 'OnlyB', permissions: ['b'] },
      ],
      required: ['a', 'b'],
    });
    assert.equal(result.ok, true);
    const success = result as GrantSuccess;
    assert.equal(success.roleCount, 1);
    assert.deepEqual(success.roles, ['Broad']);
    assert.deepEqual(success.excessPermissions, ['extra1', 'extra2']);
  });

  it('breaks role-count ties by fewest excess permissions', () => {
    const result = solveGrant({
      roles: [
        { name: 'Noisy', permissions: ['a', 'b', 'noise'] },
        { name: 'Exact', permissions: ['a', 'b'] },
      ],
      required: ['a', 'b'],
    });
    assert.equal(result.ok, true);
    const success = result as GrantSuccess;
    assert.deepEqual(success.roles, ['Exact']);
    assert.deepEqual(success.excessPermissions, []);
  });

  it('handles an empty requirement with zero roles', () => {
    const result = solveGrant({
      roles: [{ name: 'A', permissions: ['a'] }],
      required: [],
    });
    assert.equal(result.ok, true);
    const success = result as GrantSuccess;
    assert.equal(success.roleCount, 0);
    assert.deepEqual(success.roles, []);
  });
});

describe('sensitive permissions', () => {
  it('never grants roles carrying sensitive permissions', () => {
    const result = solveGrant({
      roles: [
        { name: 'Tainted', permissions: ['a', 'b', 'secret'] },
        { name: 'CleanA', permissions: ['a'] },
        { name: 'CleanB', permissions: ['b'] },
      ],
      required: ['a', 'b'],
      sensitive: ['secret'],
    });
    assert.equal(result.ok, true);
    const success = result as GrantSuccess;
    assert.deepEqual(success.roles, ['CleanA', 'CleanB']);
  });

  it('reports forced-sensitive witnesses when every grantor is tainted', () => {
    const result = solveGrant({
      roles: [
        { name: 'Admin', permissions: ['deploy', 'audit'] },
        { name: 'Ops', permissions: ['deploy', 'root'] },
        { name: 'Viewer', permissions: ['read'] },
      ],
      required: ['read', 'deploy'],
      sensitive: ['audit', 'root'],
    });
    assert.equal(result.ok, false);
    const failure = result as GrantUncoverable;
    assert.equal(failure.reason, 'uncoverable');
    assert.deepEqual(failure.witnesses, [
      {
        kind: 'forced-sensitive',
        permission: 'deploy',
        blockedBy: [
          { role: 'Admin', sensitivePermissions: ['audit'] },
          { role: 'Ops', sensitivePermissions: ['root'] },
        ],
      },
    ]);
  });

  it('reports missing permissions no role can grant', () => {
    const result = solveGrant({
      roles: [{ name: 'Viewer', permissions: ['read'] }],
      required: ['read', 'write'],
    });
    assert.equal(result.ok, false);
    const failure = result as GrantUncoverable;
    assert.deepEqual(failure.witnesses, [
      { kind: 'missing', permission: 'write' },
    ]);
  });

  it('reports required permissions that are themselves sensitive', () => {
    const result = solveGrant({
      roles: [{ name: 'Admin', permissions: ['read', 'delete'] }],
      required: ['read', 'delete'],
      sensitive: ['delete'],
    });
    assert.equal(result.ok, false);
    const failure = result as GrantUncoverable;
    assert.deepEqual(failure.witnesses, [
      { kind: 'sensitive-conflict', permission: 'delete' },
      {
        kind: 'forced-sensitive',
        permission: 'read',
        blockedBy: [{ role: 'Admin', sensitivePermissions: ['delete'] }],
      },
    ]);
  });
});

describe('role inheritance', () => {
  it('grants inherited permissions transitively', () => {
    const result = solveGrant({
      roles: [
        { name: 'Base', permissions: ['read'] },
        { name: 'Editor', permissions: ['write'], inherits: ['Base'] },
        { name: 'Admin', permissions: ['delete'], inherits: ['Editor'] },
      ],
      required: ['read', 'write', 'delete'],
    });
    assert.equal(result.ok, true);
    const success = result as GrantSuccess;
    assert.deepEqual(success.roles, ['Admin']);
    assert.deepEqual(success.excessPermissions, []);
  });

  it('treats inherited sensitive permissions as tainting', () => {
    const result = solveGrant({
      roles: [
        { name: 'Root', permissions: ['secret'] },
        { name: 'Admin', permissions: ['deploy'], inherits: ['Root'] },
      ],
      required: ['deploy'],
      sensitive: ['secret'],
    });
    assert.equal(result.ok, false);
    const failure = result as GrantUncoverable;
    assert.deepEqual(failure.witnesses, [
      {
        kind: 'forced-sensitive',
        permission: 'deploy',
        blockedBy: [{ role: 'Admin', sensitivePermissions: ['secret'] }],
      },
    ]);
  });

  it('resolves inheritance cycles to the union and warns', () => {
    const result = solveGrant({
      roles: [
        { name: 'A', permissions: ['a'], inherits: ['B'] },
        { name: 'B', permissions: ['b'], inherits: ['A'] },
      ],
      required: ['a', 'b'],
    });
    assert.equal(result.ok, true);
    const success = result as GrantSuccess;
    assert.equal(success.roleCount, 1);
    assert.equal(success.warnings.length, 1);
    assert.match(success.warnings[0], /cycle/);
  });
});

describe('limits and validation', () => {
  it('reports when the optimum exceeds maxRoles', () => {
    const result = solveGrant({
      roles: [
        { name: 'A', permissions: ['a'] },
        { name: 'B', permissions: ['b'] },
      ],
      required: ['a', 'b'],
      maxRoles: 1,
    });
    assert.equal(result.ok, false);
    const failure = result as GrantRoleLimitExceeded;
    assert.equal(failure.reason, 'role-limit-exceeded');
    assert.equal(failure.minimumRolesNeeded, 2);
    assert.deepEqual(failure.optimalCombination, ['A', 'B']);
  });

  it('rejects catalogs larger than 25 roles', () => {
    const result = solveGrant({
      roles: Array.from({ length: 26 }, (_, i) => ({
        name: `R${i}`,
        permissions: [`p${i}`],
      })),
      required: ['p0'],
    });
    assert.equal(result.ok, false);
    assert.equal((result as { reason: string }).reason, 'invalid');
  });

  it('solves a 25-role catalog exactly', () => {
    const result = solveGrant({
      roles: Array.from({ length: 25 }, (_, i) => ({
        name: `R${i}`,
        permissions: [`p${i}`, 'shared'],
      })),
      required: Array.from({ length: 25 }, (_, i) => `p${i}`),
    });
    assert.equal(result.ok, true);
    assert.equal((result as GrantSuccess).roleCount, 25);
  });

  it('rejects unknown inherited roles and duplicate names', () => {
    const unknown = solveGrant({
      roles: [{ name: 'A', permissions: ['a'], inherits: ['Ghost'] }],
      required: ['a'],
    });
    assert.equal(unknown.ok, false);
    assert.equal((unknown as { reason: string }).reason, 'invalid');

    const duplicate = solveGrant({
      roles: [
        { name: 'A', permissions: ['a'] },
        { name: 'A', permissions: ['b'] },
      ],
      required: ['a'],
    });
    assert.equal(duplicate.ok, false);
    assert.equal((duplicate as { reason: string }).reason, 'invalid');
  });
});
