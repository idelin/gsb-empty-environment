import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ApprovalPlan, PlanError, RevisionRejected } from '../src/approval-plan.ts';

// ------------------------------------------------------------ 前后依赖 + 会签

test('前后依赖：会签节点全员通过后，下一批节点才可执行', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'all', approvers: ['u1', 'u2'] },
      { id: 'B', mode: 'any', approvers: ['u3'], dependsOn: ['A'] },
      { id: 'C', mode: 'any', approvers: ['u4'], dependsOn: ['A'] },
    ],
  });

  assert.deepEqual(plan.getNextExecutable().map((n) => n.id), ['A']);

  plan.approve('A', 'u1');
  assert.equal(plan.getNode('A').status, 'in_progress'); // 会签未集齐
  assert.deepEqual(plan.getNextExecutable().map((n) => n.id), []);

  plan.approve('A', 'u2');
  assert.equal(plan.getNode('A').status, 'approved');
  // 下一批可执行节点为并行的 B、C
  assert.deepEqual(plan.getNextExecutable().map((n) => n.id), ['B', 'C']);
});

test('依赖未满足时不可启动审批', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'any', approvers: ['u1'] },
      { id: 'B', mode: 'any', approvers: ['u2'], dependsOn: ['A'] },
    ],
  });
  assert.throws(() => plan.approve('B', 'u2'), PlanError);
});

// ------------------------------------------------------------ 或签

test('或签：一人通过即完成，其余审批人待办清空', () => {
  const plan = new ApprovalPlan({
    nodes: [{ id: 'A', mode: 'any', approvers: ['u1', 'u2', 'u3'] }],
  });
  assert.deepEqual(plan.getNode('A').pendingApprovers, ['u1', 'u2', 'u3']);
  plan.approve('A', 'u2');
  const node = plan.getNode('A');
  assert.equal(node.status, 'approved');
  assert.deepEqual(node.pendingApprovers, []);
  assert.throws(() => plan.approve('A', 'u1'), PlanError); // 终态不可再审批
});

test('任一审批人拒绝则节点拒绝', () => {
  const plan = new ApprovalPlan({
    nodes: [{ id: 'A', mode: 'all', approvers: ['u1', 'u2'] }],
  });
  plan.approve('A', 'u1');
  plan.reject('A', 'u2');
  assert.equal(plan.getNode('A').status, 'rejected');
});

// ------------------------------------------------------------ 或签组（分支）

test('或签组：任一分支通过后未开始分支取消，外部依赖不受影响', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'root', mode: 'any', approvers: ['u0'] },
      { id: 'B1', mode: 'any', approvers: ['u1'], dependsOn: ['root'], group: 'G' },
      { id: 'B2', mode: 'all', approvers: ['u2', 'u3'], dependsOn: ['root'], group: 'G' },
      // 组外节点依赖或签组 G 本身
      { id: 'C', mode: 'any', approvers: ['u4'], dependsOn: ['G'] },
    ],
  });

  plan.approve('root', 'u0');
  assert.deepEqual(plan.getNextExecutable().map((n) => n.id).sort(), ['B1', 'B2']);

  plan.approve('B1', 'u1'); // 分支 B1 通过 → 组 G 通过
  assert.equal(plan.isGroupApproved('G'), true);

  // 未开始分支 B2 被取消
  const b2 = plan.getNode('B2');
  assert.equal(b2.status, 'cancelled');
  assert.match(b2.cancelReason!, /或签组 G 已由分支 B1 通过/);

  // 关键验证：外部依赖 C 不受 B2 取消影响，出现在下一批可执行节点中
  const next = plan.getNextExecutable().map((n) => n.id);
  assert.deepEqual(next, ['C']);
  plan.approve('C', 'u4');
  assert.equal(plan.getNode('C').status, 'approved');
});

test('或签组：进行中的分支不被强制取消', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'B1', mode: 'any', approvers: ['u1'], group: 'G' },
      { id: 'B2', mode: 'all', approvers: ['u2', 'u3'], group: 'G' },
    ],
  });
  plan.approve('B2', 'u2'); // B2 进入 in_progress
  plan.approve('B1', 'u1'); // B1 通过，组通过
  assert.equal(plan.getNode('B2').status, 'in_progress'); // 已开始的分支保留
});

// ------------------------------------------------------------ 运行中加签

test('运行中 insertAfter 加签：重挂未开始的后续节点', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'any', approvers: ['u1'] },
      { id: 'B', mode: 'any', approvers: ['u2'], dependsOn: ['A'] },
    ],
  });
  plan.approve('A', 'u1');
  plan.insertAfter('A', { id: 'A+', mode: 'any', approvers: ['boss'] });

  assert.deepEqual(plan.getNode('A+').dependsOn, ['A']);
  assert.deepEqual(plan.getNode('B').dependsOn, ['A+']);
  // B 需等待加签节点通过
  assert.deepEqual(plan.getNextExecutable().map((n) => n.id), ['A+']);
  plan.approve('A+', 'boss');
  assert.deepEqual(plan.getNextExecutable().map((n) => n.id), ['B']);
});

test('加签：后续节点已开始则拒绝插入并解释', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'any', approvers: ['u1'] },
      { id: 'B', mode: 'all', approvers: ['u2', 'u3'], dependsOn: ['A'] },
    ],
  });
  plan.approve('A', 'u1');
  plan.approve('B', 'u2'); // B 已开始
  assert.throws(
    () => plan.insertAfter('A', { id: 'A+', mode: 'any', approvers: ['boss'] }),
    (err: Error) => {
      assert.ok(err instanceof RevisionRejected);
      assert.match(err.message, /拒绝加签/);
      assert.match(err.message, /改变既有执行顺序/);
      return true;
    },
  );
});

test('节点内加签：会签中追加审批人，新人也须通过', () => {
  const plan = new ApprovalPlan({
    nodes: [{ id: 'A', mode: 'all', approvers: ['u1', 'u2'] }],
  });
  plan.approve('A', 'u1');
  plan.addApprover('A', 'u3');
  plan.approve('A', 'u2');
  assert.equal(plan.getNode('A').status, 'in_progress'); // 还缺 u3
  plan.approve('A', 'u3');
  assert.equal(plan.getNode('A').status, 'approved');
});

test('已完成节点不可加签审批人', () => {
  const plan = new ApprovalPlan({
    nodes: [{ id: 'A', mode: 'any', approvers: ['u1'] }],
  });
  plan.approve('A', 'u1');
  assert.throws(() => plan.addApprover('A', 'u2'), RevisionRejected);
});

// ------------------------------------------------------------ 计划修订

test('修订：已完成节点不可撤回/移除，拒绝并解释', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'any', approvers: ['u1'] },
      { id: 'B', mode: 'any', approvers: ['u2'], dependsOn: ['A'] },
    ],
  });
  plan.approve('A', 'u1');

  assert.throws(() => plan.withdraw('A'), /已完成.*不可撤回/);

  const report = plan.revise({ removeNodes: ['A', 'B'] });
  assert.deepEqual(report.applied, ['removeNode(B)']); // B 未开始，可移除
  assert.equal(report.rejected.length, 1);
  assert.match(report.rejected[0].reason, /已完成\(approved\)，不可撤回/);
  assert.equal(plan.getNode('A').status, 'approved'); // A 保留
});

test('修订：新增依赖若要求改变既有顺序则拒绝并解释', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'any', approvers: ['u1'] },
      { id: 'B', mode: 'any', approvers: ['u2'] },
      { id: 'C', mode: 'any', approvers: ['u3'] },
    ],
  });
  plan.approve('A', 'u1'); // A 先完成
  plan.approve('B', 'u2'); // B 后完成

  // 新增 A <- B（B 须先于 A）：A 早已完成，改变既有顺序 → 拒绝
  const report = plan.revise({ addDependencies: [{ from: 'A', to: 'B' }] });
  assert.deepEqual(report.applied, []);
  assert.equal(report.rejected.length, 1);
  assert.match(report.rejected[0].reason, /拒绝新增依赖 A <- B/);
  assert.match(report.rejected[0].reason, /改变既有执行顺序/);
  assert.deepEqual(plan.getNode('A').dependsOn, []); // 未生效

  // C 尚未开始：C <- B 合法（B 已完成），接受
  const ok = plan.revise({ addDependencies: [{ from: 'C', to: 'B' }] });
  assert.deepEqual(ok.applied, ['addDependency(C <- B)']);
  assert.deepEqual(plan.getNextExecutable().map((n) => n.id), ['C']);
});

test('修订：新增依赖成环则拒绝并解释', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'any', approvers: ['u1'] },
      { id: 'B', mode: 'any', approvers: ['u2'], dependsOn: ['A'] },
      { id: 'C', mode: 'any', approvers: ['u3'], dependsOn: ['B'] },
    ],
  });
  const report = plan.revise({ addDependencies: [{ from: 'A', to: 'C' }] });
  assert.equal(report.rejected.length, 1);
  assert.match(report.rejected[0].reason, /依赖环/);
});

test('修订：新增节点与移除依赖', () => {
  const plan = new ApprovalPlan({
    nodes: [
      { id: 'A', mode: 'any', approvers: ['u1'] },
      { id: 'B', mode: 'any', approvers: ['u2'], dependsOn: ['A'] },
    ],
  });
  const report = plan.revise({
    addNodes: [{ id: 'D', mode: 'any', approvers: ['u4'], dependsOn: ['A'] }],
    removeDependencies: [{ from: 'B', to: 'A' }],
  });
  assert.deepEqual(report.applied.sort(), [
    'addNode(D)',
    'removeDependency(B <- A)',
  ]);
  assert.deepEqual(plan.getNextExecutable().map((n) => n.id).sort(), ['A', 'B']);
});

test('建计划时拒绝环与悬空依赖', () => {
  assert.throws(
    () =>
      new ApprovalPlan({
        nodes: [
          { id: 'A', mode: 'any', approvers: ['u1'], dependsOn: ['B'] },
          { id: 'B', mode: 'any', approvers: ['u2'], dependsOn: ['A'] },
        ],
      }),
    /依赖环|环/,
  );
  assert.throws(
    () =>
      new ApprovalPlan({
        nodes: [{ id: 'A', mode: 'any', approvers: ['u1'], dependsOn: ['ghost'] }],
      }),
    /不存在/,
  );
});
