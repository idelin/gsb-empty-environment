import { greedyPlan } from './greedy';
import { optimalPlan } from './planner';
import { PlanSummary, PlannerInput, ResourceVector } from './types';

// 场景：预算 energy=10, parts=6。
// G1 离起点最近（2 步），但达成它会吃掉 buildG2 必需的 parts；
// 贪心朝最近目标走会卡在 lab：G2 只差 1 步却再也造不起。
// 最优解放弃 G1，直取 G2+G3（收益 55）。
const input: PlannerInput = {
  initialState: 'start',
  initialResources: { energy: 10, parts: 6 },
  goals: [
    { id: 'G1', profit: 10 },
    { id: 'G2', profit: 30 },
    { id: 'G3', profit: 25 },
  ],
  actions: [
    { name: 'toWorkshop', cost: 1, consumes: { energy: 1 }, from: ['start'], to: 'workshop' },
    { name: 'toLab', cost: 1, consumes: { energy: 1 }, from: ['workshop'], to: 'lab' },
    { name: 'backToWorkshop', cost: 1, consumes: { energy: 1 }, from: ['lab'], to: 'workshop' },
    { name: 'assembleG1', cost: 2, consumes: { energy: 2, parts: 4 }, from: ['workshop'], to: 'workshop', achieves: ['G1'] },
    { name: 'buildG2', cost: 3, consumes: { energy: 3, parts: 4 }, from: ['lab'], to: 'lab', achieves: ['G2'] },
    { name: 'toVault', cost: 1, consumes: { energy: 1 }, from: ['lab'], to: 'vault' },
    { name: 'unlockG3', cost: 2, consumes: { energy: 2, parts: 2 }, from: ['vault'], to: 'vault', requiresGoals: ['G2'], achieves: ['G3'] },
  ],
  maxSteps: 12,
  nearDistance: 2,
};

const resourceNames = Object.keys(input.initialResources);

function fmtVec(vec: ResourceVector, keys: string[]): string {
  return '{' + keys.map(key => key + '=' + vec[key]).join(', ') + '}';
}

function printPlan(title: string, result: PlanSummary): void {
  console.log('=== ' + title + ' ===');
  result.steps.forEach((step, idx) => {
    const gain = step.achievedGoals.length > 0 ? '  达成[' + step.achievedGoals.join(',') + ']' : '';
    console.log(
      '步骤' + (idx + 1) + ': ' + step.action +
      '  成本' + step.cost +
      '  消耗' + fmtVec(step.consumes, Object.keys(step.consumes)) +
      '  余额' + fmtVec(step.balances, resourceNames) +
      '  状态→' + step.state + gain
    );
  });
  console.log('总收益=' + result.profit + '  总成本=' + result.cost + '  目标=[' + result.achievedGoals.join(', ') + ']  终态=' + result.finalState);
}

const optimal = optimalPlan(input);
printPlan('最优计划（主目标收益最大，次目标成本最小）', optimal);
console.log('扩展结点数=' + optimal.explored);
console.log('');

console.log('=== 死端识别（看似接近目标，实则关键资源已被耗尽）===');
for (const deadEnd of optimal.deadEnds) {
  console.log('前缀[' + deadEnd.prefix.join(' → ') + '] @' + deadEnd.state +
    ' 余额' + fmtVec(deadEnd.remaining, resourceNames) +
    ' (收益' + deadEnd.profit + ', 成本' + deadEnd.cost + ')');
  for (const blk of deadEnd.blockedGoals) {
    const near = blk.distance !== -1 && blk.distance <= (input.nearDistance ?? 2) ? '  ← 近距陷阱' : '';
    console.log('  目标' + blk.goal + '(收益' + blk.profit + ') 距' + blk.distance + '步 缺口' + fmtVec(blk.missing, Object.keys(blk.missing)) + near);
  }
}
console.log('');

const greedy = greedyPlan(input);
printPlan('距离贪心基线（每步朝最近目标移动）', greedy);
console.log('');
console.log('对比: 最优收益 ' + optimal.profit + ' / 成本 ' + optimal.cost + '  vs  贪心收益 ' + greedy.profit + ' / 成本 ' + greedy.cost);

if (optimal.profit !== 55 || optimal.cost !== 8) throw new Error('断言失败: 最优计划应为 收益55/成本8');
if (greedy.profit !== 10) throw new Error('断言失败: 贪心应陷入 G1 陷阱, 收益10');
const trap = optimal.deadEnds.some(deadEnd =>
  deadEnd.blockedGoals.some(blk => blk.goal === 'G2' && blk.distance !== -1 && blk.distance <= (input.nearDistance ?? 2) && blk.missing.parts === 2));
if (!trap) throw new Error('断言失败: 应识别出 G2 近距死端(缺口 parts=2)');
console.log('断言全部通过 ✓');
