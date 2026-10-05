import { strict as assert } from 'assert';
import { demoScenario } from './demo';
import { buildTree, resolveRef } from './model';
import { planMigration } from './solver';
import { Scenario } from './types';

let passed = 0;
function check(name: string, fn: () => void): void {
  fn();
  passed += 1;
  console.log(`ok - ${name}`);
}

check('简单目标移动，无环，例外数正确', () => {
  const scenario: Scenario = {
    nodes: [
      { id: 'root', parent: null, fixed: true, grants: ['read'] },
      { id: 'a', parent: 'root', grants: ['x'] },
      { id: 'b', parent: 'root', grants: ['y'] },
      { id: 'item', parent: 'a', targetParent: 'b' },
    ],
  };
  const plan = planMigration(scenario);
  assert.equal(plan.feasible, true);
  assert.equal(plan.moveCount, 1);
  assert.deepEqual(plan.moves[0], { id: 'item', from: 'a', to: 'b', reason: 'target' });
  // 失去继承 x（补授），获得继承 y（回收）
  assert.equal(plan.exceptionCount, 2);
  assert.deepEqual(
    plan.exceptions.map((e) => `${e.kind}:${e.permission}`).sort(),
    ['deny:y', 'grant:x'],
  );
});

check('已在目标位置的节点不计移动', () => {
  const scenario: Scenario = {
    nodes: [
      { id: 'root', parent: null, fixed: true },
      { id: 'g', parent: 'root' },
      { id: 'item', parent: 'g', targetParent: 'g' },
    ],
  };
  const plan = planMigration(scenario);
  assert.equal(plan.feasible, true);
  assert.equal(plan.moveCount, 0);
  assert.equal(plan.exceptionCount, 0);
});

check('目标形成环时需一次破环移动，移动数最小', () => {
  const scenario: Scenario = {
    nodes: [
      { id: 'root', parent: null, fixed: true },
      { id: 'proj', parent: 'root', targetParent: 'team' },
      { id: 'team', parent: 'proj' },
    ],
  };
  const plan = planMigration(scenario);
  assert.equal(plan.feasible, true);
  assert.equal(plan.moveCount, 2); // proj 强制移动 + team 破环移动
  const breaker = plan.moves.find((m) => m.reason === 'cycle-break');
  assert.equal(breaker?.id, 'team');
  assert.equal(plan.identityPreserved, true);
});

check('固定节点目标冲突 -> 不可行', () => {
  const scenario: Scenario = {
    nodes: [
      { id: 'root', parent: null, fixed: true },
      { id: 'g', parent: 'root' },
      { id: 'item', parent: 'root', fixed: true, targetParent: 'g' },
    ],
  };
  const plan = planMigration(scenario);
  assert.equal(plan.feasible, false);
  assert.ok(plan.problems.some((p) => p.includes('fixed')));
});

check('双向目标成环且无可动节点 -> 不可行', () => {
  const scenario: Scenario = {
    nodes: [
      { id: 'root', parent: null, fixed: true },
      { id: 'a', parent: 'root', targetParent: 'b' },
      { id: 'b', parent: 'root', targetParent: 'a' },
    ],
  };
  const plan = planMigration(scenario);
  assert.equal(plan.feasible, false);
  assert.ok(plan.problems.some((p) => p.includes('unbreakable cycle')));
});

check('引用解析：相对/绝对/.. 语义', () => {
  const problems: string[] = [];
  const tree = buildTree(
    [
      { id: 'root', parent: null, fixed: true },
      { id: 'a', parent: 'root' },
      { id: 'b', parent: 'a' },
      { id: 'c', parent: 'root' },
    ],
    problems,
  )!;
  assert.equal(problems.length, 0);
  assert.equal(resolveRef(tree, 'b', '../c'), 'c');
  assert.equal(resolveRef(tree, 'b', '/a/b'), 'b');
  assert.equal(resolveRef(tree, 'b', '../../a'), 'a');
  assert.equal(resolveRef(tree, 'b', '../nope'), null);
  assert.equal(resolveRef(tree, 'root', '..'), 'root');
});

check('演示场景：4 次移动、3 条例外、引用断裂被报告', () => {
  const plan = planMigration(demoScenario);
  assert.equal(plan.feasible, true);
  assert.equal(plan.moveCount, 4);
  assert.equal(plan.exceptionCount, 3);
  assert.equal(plan.identityPreserved, true);
  const byOwnerRef = new Map(plan.refResolutions.map((r) => [`${r.owner}|${r.ref}`, r]));
  assert.equal(byOwnerRef.get('app|/shared/lib')?.status, 'unchanged');
  assert.equal(byOwnerRef.get('app|eng-tools')?.status, 'broken');
  assert.equal(byOwnerRef.get('widget|../../shared/lib')?.status, 'unchanged');
  assert.equal(byOwnerRef.get('batch|../eng/app')?.status, 'broken');
  assert.equal(byOwnerRef.get('legacy|/eng/app')?.status, 'broken');
  assert.equal(byOwnerRef.get('team-b|../eng')?.status, 'unchanged');
});

check('次目标：破环目的地选择最小权限例外', () => {
  // team 破环后可去 root（无额外权限）或 sec（会引入 secret 继承，需回收）。
  const scenario: Scenario = {
    nodes: [
      { id: 'root', parent: null, fixed: true },
      { id: 'sec', parent: 'root', grants: ['secret'] },
      { id: 'proj', parent: 'root', targetParent: 'team' },
      { id: 'team', parent: 'proj' },
    ],
  };
  const plan = planMigration(scenario);
  assert.equal(plan.feasible, true);
  const breaker = plan.moves.find((m) => m.reason === 'cycle-break');
  assert.equal(breaker?.to, 'root');
  assert.equal(plan.exceptionCount, 0);
});

console.log(`\n${passed} 项自测全部通过`);
