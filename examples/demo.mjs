import { ApprovalEngine, revise } from '../build/index.js';

function show(engine, title) {
  console.log(`\n== ${title} ==`);
  console.log('下一批可执行节点：', engine.nextBatch().join('、') || '（无）');
}

const engine = ApprovalEngine.fromDefs([
  { id: 'apply', label: '提交申请', mode: 'any', assignees: ['alice'], deps: [] },
  { id: 'review', label: '主管审批（或签）', mode: 'any', assignees: ['bob', 'carol'], deps: ['apply'] },
  { id: 'audit', label: '财务合规（会签）', mode: 'all', assignees: ['dave', 'erin'], deps: ['review'] },
  { id: 'gift', label: '附加福利分支', mode: 'any', assignees: ['frank'], deps: ['apply'] },
  { id: 'archive', label: '归档（外部依赖）', mode: 'any', assignees: ['gina'], deps: ['review'] },
]);

show(engine, '初始');
engine.sign('apply', 'alice', 'approve');
show(engine, '申请提交后');

engine.sign('review', 'bob', 'approve');
console.log('\nbob 或签通过，carol 的审批任务自动取消：', engine.getState('review').cancelledAssignees.join('、'));

engine.cancelNode('gift', '或签已通过，附加福利分支作废');
console.log('未开始分支 gift 已取消，归档节点不受影响');
show(engine, '或签通过并取消分支后');

console.log('\n运行中加签：给财务合规追加 heidi');
engine.addAssignees('audit', ['heidi']);
engine.sign('audit', 'dave', 'approve');
engine.sign('audit', 'erin', 'approve');
console.log('dave、erin 已签，audit 状态：', engine.getState('audit').status);
engine.sign('audit', 'heidi', 'approve');
console.log('heidi 加签完成，audit 状态：', engine.getState('audit').status);
show(engine, '会签完成后');

console.log('\n尝试撤回已完成节点 review：');
const res = revise(engine, [{ kind: 'withdraw', id: 'review' }]);
for (const err of res.errors) {
  console.log(`拒绝[${err.op}]：${err.reason}`);
}

console.log('\n合法修订：新增未开始的节点 party');
const okRes = revise(engine, [{ kind: 'addNode', node: { id: 'party', label: '团建', mode: 'any', assignees: ['ivan'], deps: [] } }]);
console.log('修订结果：', okRes.ok ? '通过' : '拒绝');

console.log('\n尝试新增依赖 party -> review（review 已完成，改变既有顺序）：');
const res2 = revise(engine, [{ kind: 'addDependency', from: 'party', to: 'review' }]);
for (const err of res2.errors) {
  console.log(`拒绝[${err.op}]：${err.reason}`);
}

engine.sign('archive', 'gina', 'approve');
show(engine, '全部完成');
