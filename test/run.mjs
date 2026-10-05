import assert from 'node:assert/strict';
import { ApprovalPlan, ApprovalEngine, revise } from '../build/index.js';

function node(id, deps, assignees, mode) {
  return { id, label: id, mode: mode || 'any', assignees, deps };
}

const tests = [];
function test(name, fn) {
  tests.push({ name, fn });
}

test('顺序依赖：下一批可执行节点随签署推进', () => {
  const engine = ApprovalEngine.fromDefs([
    node('A', [], ['a1']),
    node('B', ['A'], ['b1']),
    node('C', ['B'], ['c1']),
    node('D', [], ['d1']),
  ]);
  assert.deepEqual(engine.nextBatch().sort(), ['A', 'D']);
  assert.equal(engine.sign('A', 'a1', 'approve').ok, true);
  assert.deepEqual(engine.nextBatch().sort(), ['B', 'D']);
  assert.equal(engine.sign('D', 'd1', 'approve').ok, true);
  assert.equal(engine.sign('B', 'b1', 'approve').ok, true);
  assert.deepEqual(engine.nextBatch(), ['C']);
  assert.equal(engine.sign('C', 'c1', 'approve').ok, true);
  assert.deepEqual(engine.nextBatch(), []);
});

test('会签：全员同意才通过，任一拒绝即否决', () => {
  const engine = ApprovalEngine.fromDefs([
    node('M', [], ['x', 'y', 'z'], 'all'),
    node('N', ['M'], ['n1']),
  ]);
  engine.sign('M', 'x', 'approve');
  engine.sign('M', 'y', 'approve');
  assert.equal(engine.getState('M').status, 'ready');
  assert.deepEqual(engine.nextBatch(), ['M']);
  engine.sign('M', 'z', 'approve');
  assert.equal(engine.getState('M').status, 'approved');
  assert.deepEqual(engine.nextBatch(), ['N']);

  const veto = ApprovalEngine.fromDefs([node('M', [], ['x', 'y'], 'all'), node('N', ['M'], ['n1'])]);
  veto.sign('M', 'x', 'approve');
  veto.sign('M', 'y', 'reject');
  assert.equal(veto.getState('M').status, 'rejected');
  assert.deepEqual(veto.nextBatch(), []);
});

test('或签：一人通过即完成，其余审批任务取消，取消未开始分支不影响外部依赖', () => {
  const engine = ApprovalEngine.fromDefs([
    node('S', [], ['s1']),
    node('O', ['S'], ['u1', 'u2']),
    node('B', ['S'], ['b1']),
    node('D', ['O'], ['d1']),
  ]);
  engine.sign('S', 's1', 'approve');
  assert.deepEqual(engine.nextBatch().sort(), ['B', 'O']);

  engine.sign('O', 'u1', 'approve');
  const st = engine.getState('O');
  assert.equal(st.status, 'approved');
  assert.deepEqual(st.cancelledAssignees, ['u2']);
  assert.equal(engine.sign('O', 'u2', 'approve').ok, false);

  const cancel = engine.cancelNode('B', '或签已通过，分支作废');
  assert.equal(cancel.ok, true);
  assert.equal(engine.getState('B').status, 'cancelled');

  assert.deepEqual(engine.nextBatch(), ['D']);
  assert.equal(engine.sign('D', 'd1', 'approve').ok, true);
  assert.equal(engine.getState('D').status, 'approved');
});

test('或签：所有人拒绝才否决', () => {
  const engine = ApprovalEngine.fromDefs([node('O', [], ['u1', 'u2'])]);
  engine.sign('O', 'u1', 'reject');
  assert.equal(engine.getState('O').status, 'ready');
  engine.sign('O', 'u2', 'reject');
  assert.equal(engine.getState('O').status, 'rejected');
});

test('运行中加签：会签追加审批人，或签新审批人可直接通过', () => {
  const engine = ApprovalEngine.fromDefs([node('M', [], ['a'], 'all'), node('N', ['M'], ['n1'])]);
  assert.equal(engine.addAssignees('M', ['b']).ok, true);
  engine.sign('M', 'a', 'approve');
  assert.equal(engine.getState('M').status, 'ready');
  engine.sign('M', 'b', 'approve');
  assert.equal(engine.getState('M').status, 'approved');
  assert.equal(engine.addAssignees('M', ['c']).ok, false);

  const orEngine = ApprovalEngine.fromDefs([node('O', [], ['a'])]);
  assert.equal(orEngine.addAssignees('O', ['b']).ok, true);
  orEngine.sign('O', 'b', 'approve');
  assert.equal(orEngine.getState('O').status, 'approved');
});

test('签署校验：未就绪、非审批人、重复签署均被拒绝', () => {
  const engine = ApprovalEngine.fromDefs([node('A', [], ['a1']), node('B', ['A'], ['b1'])]);
  assert.equal(engine.sign('B', 'b1', 'approve').ok, false);
  assert.equal(engine.sign('A', 'stranger', 'approve').ok, false);
  assert.equal(engine.sign('X', 'a1', 'approve').ok, false);
  engine.sign('A', 'a1', 'approve');
  assert.equal(engine.sign('A', 'a1', 'approve').ok, false);
});

test('取消限制：已开始或已完成的节点不可取消', () => {
  const engine = ApprovalEngine.fromDefs([
    node('A', [], ['a1']),
    node('B', [], ['b1', 'b2'], 'all'),
    node('C', ['A'], ['c1']),
  ]);
  engine.sign('B', 'b1', 'approve');
  assert.equal(engine.cancelNode('B', 'try').ok, false);
  engine.sign('A', 'a1', 'approve');
  assert.equal(engine.cancelNode('A', 'try').ok, false);
  assert.equal(engine.cancelNode('C', '未开始，允许取消').ok, true);
});

test('修订：已完成节点不可撤回', () => {
  const engine = ApprovalEngine.fromDefs([node('A', [], ['a1']), node('B', ['A'], ['b1'])]);
  engine.sign('A', 'a1', 'approve');
  const res = revise(engine, [{ kind: 'withdraw', id: 'A' }]);
  assert.equal(res.ok, false);
  assert.match(res.errors[0].reason, /不可撤回/);
  assert.equal(engine.getState('A').status, 'approved');
});

test('修订：新增依赖形成环路须拒绝并解释', () => {
  const engine = ApprovalEngine.fromDefs([
    node('A', [], ['a1']),
    node('B', ['A'], ['b1']),
    node('C', ['B'], ['c1']),
  ]);
  const res = revise(engine, [{ kind: 'addDependency', from: 'C', to: 'A' }]);
  assert.equal(res.ok, false);
  assert.match(res.errors[0].reason, /环路/);
  assert.match(res.errors[0].reason, /A -> B -> C -> A/);
  assert.deepEqual(engine.nextBatch(), ['A']);
});

test('修订：新增依赖改变既有顺序须拒绝并解释', () => {
  const engine = ApprovalEngine.fromDefs([
    node('A', [], ['a1']),
    node('B', [], ['b1', 'b2'], 'all'),
  ]);
  engine.sign('A', 'a1', 'approve');
  const done = revise(engine, [{ kind: 'addDependency', from: 'B', to: 'A' }]);
  assert.equal(done.ok, false);
  assert.match(done.errors[0].reason, /既有执行顺序/);

  engine.sign('B', 'b1', 'approve');
  const started = revise(engine, [{ kind: 'addDependency', from: 'A', to: 'B' }]);
  assert.equal(started.ok, false);
  assert.match(started.errors[0].reason, /既有执行顺序/);
});

test('修订：合法变更加入新节点与新依赖', () => {
  const engine = ApprovalEngine.fromDefs([node('A', [], ['a1']), node('B', ['A'], ['b1'])]);
  const res = revise(engine, [
    { kind: 'addNode', node: node('X', ['A'], ['x1']) },
    { kind: 'addDependency', from: 'X', to: 'B' },
  ]);
  assert.equal(res.ok, true);
  engine.sign('A', 'a1', 'approve');
  assert.deepEqual(engine.nextBatch(), ['X']);
  engine.sign('X', 'x1', 'approve');
  assert.deepEqual(engine.nextBatch(), ['B']);
});

test('修订：运行中通过修订加签', () => {
  const engine = ApprovalEngine.fromDefs([node('M', [], ['a'], 'all')]);
  const res = revise(engine, [{ kind: 'addAssignees', id: 'M', users: ['b'] }]);
  assert.equal(res.ok, true);
  engine.sign('M', 'a', 'approve');
  assert.equal(engine.getState('M').status, 'ready');
  engine.sign('M', 'b', 'approve');
  assert.equal(engine.getState('M').status, 'approved');
});

test('修订：移除节点受完成状态与依赖关系约束', () => {
  const engine = ApprovalEngine.fromDefs([
    node('A', [], ['a1']),
    node('B', ['A'], ['b1']),
    node('C', [], ['c1']),
  ]);
  engine.sign('A', 'a1', 'approve');
  const done = revise(engine, [{ kind: 'removeNode', id: 'A' }]);
  assert.equal(done.ok, false);
  assert.match(done.errors[0].reason, /已完成/);
  const used = revise(engine, [{ kind: 'removeNode', id: 'B' }]);
  assert.equal(used.ok, true);
  const free = revise(engine, [{ kind: 'removeNode', id: 'C' }]);
  assert.equal(free.ok, true);
  assert.equal(engine.has('C'), false);
});

test('修订：整批操作原子生效，任一非法则全部拒绝', () => {
  const engine = ApprovalEngine.fromDefs([node('A', [], ['a1'])]);
  engine.sign('A', 'a1', 'approve');
  const res = revise(engine, [
    { kind: 'addNode', node: node('X', [], ['x1']) },
    { kind: 'withdraw', id: 'A' },
  ]);
  assert.equal(res.ok, false);
  assert.equal(engine.has('X'), false);
});

test('计划校验：重复节点、缺失依赖、环路', () => {
  assert.throws(() => ApprovalEngine.fromDefs([node('A', [], ['a']), node('A', [], ['b'])]), /重复/);
  assert.throws(() => ApprovalEngine.fromDefs([node('A', ['ghost'], ['a'])]), /不存在/);
  assert.throws(
    () => ApprovalEngine.fromDefs([node('A', ['B'], ['a']), node('B', ['A'], ['b'])]),
    /环路/,
  );
});

let failed = 0;
for (const { name, fn } of tests) {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL - ${name}`);
    console.error(err && err.message ? err.message : err);
  }
}
console.log(`${tests.length - failed}/${tests.length} 通过`);
process.exit(failed === 0 ? 0 : 1);
