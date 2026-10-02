import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TemporalDelegationKernel, INF } from '../src/kernel.ts';

/**
 * 搭建验收场景：
 *  - A 持有角色 R（直接授权 [0, ∞)，知悉于 0）
 *  - A 委托 R 给 B，业务 [10, 50)，知悉于 10
 *  - B 在 20 把 R 委托给 C，业务 [20, 60)，知悉于 20
 *  - A 在知悉 30 时登记：委托于业务 25 提前撤回
 */
function acceptanceKernel(): TemporalDelegationKernel {
  const k = new TemporalDelegationKernel();
  k.defineRole('R', ['doc:read', 'doc:write']);
  k.grantRole('A', 'R', 0, INF, 0);
  k.delegate('A', 'B', 'R', 10, 50, 10);
  k.delegate('B', 'C', 'R', 20, 60, 20);
  k.revokeDelegation('A', 'B', 'R', 25, 30);
  return k;
}

test('验收边界：知悉35查询C在业务28的权限，判定已失效', () => {
  const k = acceptanceKernel();
  assert.equal(k.hasRole('C', 'R', 28, 35), false);
  assert.deepEqual([...k.getEffectivePermissions('C', 28, 35)], []);
});

test('同一业务时间28，知悉30之前（撤回未登记）C 仍有效', () => {
  const k = acceptanceKernel();
  assert.equal(k.hasRole('C', 'R', 28, 25), true);
  assert.deepEqual([...k.getEffectivePermissions('C', 28, 25)].sort(), ['doc:read', 'doc:write']);
});

test('知悉35时，业务24（撤回生效点之前）C 仍有效', () => {
  const k = acceptanceKernel();
  assert.equal(k.hasRole('C', 'R', 24, 35), true);
});

test('B 的视角：业务28 在知悉25有效、知悉35失效', () => {
  const k = acceptanceKernel();
  assert.equal(k.hasRole('B', 'R', 28, 25), true);
  assert.equal(k.hasRole('B', 'R', 28, 35), false);
});

test('中间环节过期：业务55 时 B 的委托已到50终止，C 随之失效', () => {
  const k = acceptanceKernel();
  assert.equal(k.hasRole('C', 'R', 55, 25), false);
});

test('半开区间边界：委托在 from 生效、在 to 失效', () => {
  const k = acceptanceKernel();
  assert.equal(k.hasRole('B', 'R', 10, 15), true);
  assert.equal(k.hasRole('B', 'R', 9, 15), false);
  assert.equal(k.hasRole('C', 'R', 20, 25), true);
  assert.equal(k.hasRole('C', 'R', 19, 25), false);
});

test('多层委托链：四级链整体解析', () => {
  const k = new TemporalDelegationKernel();
  k.defineRole('R', ['p1']);
  k.grantRole('A', 'R', 0, 100, 0);
  k.delegate('A', 'B', 'R', 10, 90, 10);
  k.delegate('B', 'C', 'R', 20, 80, 20);
  k.delegate('C', 'D', 'R', 30, 70, 30);
  assert.equal(k.hasRole('D', 'R', 40, 40), true);
  // 中间 C->D 段在 70 到期
  assert.equal(k.hasRole('D', 'R', 75, 40), false);
  // 根部 A->B 在业务 60 提前撤回（知悉 65）
  k.revokeDelegation('A', 'B', 'R', 60, 65);
  assert.equal(k.hasRole('D', 'R', 40, 70), true);  // 撤回点之前不受影响
  assert.equal(k.hasRole('D', 'R', 61, 70), false);  // 链根被截断，全链失效
});

test('委托环不会导致死循环', () => {
  const k = new TemporalDelegationKernel();
  k.defineRole('R', ['p1']);
  k.delegate('A', 'B', 'R', 0, 100, 0);
  k.delegate('B', 'A', 'R', 0, 100, 0);
  assert.equal(k.hasRole('A', 'R', 10, 10), false);
  assert.equal(k.hasRole('B', 'R', 10, 10), false);
});

test('直接授权过期与提前收回', () => {
  const k = new TemporalDelegationKernel();
  k.defineRole('R', ['p1']);
  k.grantRole('A', 'R', 0, 50, 0);
  assert.equal(k.hasRole('A', 'R', 49, 10), true);
  assert.equal(k.hasRole('A', 'R', 50, 10), false);
  k.revokeRoleGrant('A', 'R', 20, 30);
  assert.equal(k.hasRole('A', 'R', 19, 40), true);
  assert.equal(k.hasRole('A', 'R', 25, 40), false);
  // 知悉 30 之前仍认为 [0,50) 有效
  assert.equal(k.hasRole('A', 'R', 25, 29), true);
});

test('对不存在的委托执行收回会抛错', () => {
  const k = new TemporalDelegationKernel();
  k.defineRole('R', ['p1']);
  assert.throws(() => k.revokeDelegation('A', 'B', 'R', 5, 10), /no active delegation/);
});

test('getEffectiveRoles / getEffectivePermissions 汇总多角色', () => {
  const k = new TemporalDelegationKernel();
  k.defineRole('R1', ['a', 'b']);
  k.defineRole('R2', ['c']);
  k.grantRole('U', 'R1', 0, 100, 0);
  k.grantRole('V', 'R2', 0, 100, 0);
  k.delegate('V', 'U', 'R2', 10, 20, 5);
  assert.deepEqual(k.getEffectiveRoles('U', 15, 10), ['R1', 'R2']);
  assert.deepEqual([...k.getEffectivePermissions('U', 15, 10)].sort(), ['a', 'b', 'c']);
  assert.deepEqual(k.getEffectiveRoles('U', 25, 10), ['R1']);
});
