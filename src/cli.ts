import { readFileSync } from 'node:fs';
import type { Problem } from './types.ts';
import { solve } from './solver.ts';
import { buildFlows } from './flow.ts';
import { sampleProblem } from './sample.ts';

function loadProblem(): { problem: Problem; source: string } {
  const file = process.argv[2];
  if (!file) return { problem: sampleProblem, source: '内置样例' };
  const text = readFileSync(file, 'utf8');
  return { problem: JSON.parse(text) as Problem, source: file };
}

function pad(s: string, w: number): string {
  let width = 0;
  for (const ch of s) width += ch.charCodeAt(0) > 0xff ? 2 : 1;
  return s + ' '.repeat(Math.max(0, w - width));
}

function main(): void {
  const { problem, source } = loadProblem();
  const started = Date.now();
  const result = solve(problem);
  const elapsed = Date.now() - started;
  const { flows, balance } = buildFlows(problem, result);

  const nameOf = (id: string): string =>
    problem.devices.find((d) => d.id === id)?.name ?? id;
  const list = (ids: string[]): string =>
    ids.length ? ids.map(nameOf).join(' ') : '（无）';

  console.log(`================ 维修规划方案（${source}） ================`);
  console.log(`求解状态  : ${result.status === 'optimal' ? '已证最优' : '已达上限（当前最优）'}，探索节点 ${result.nodes}，耗时 ${elapsed}ms`);
  console.log(`目标值    : ${result.objective} = 恢复总价值 ${result.valueRestored} - 残值损失 ${result.residualLost}`);
  console.log(`预算使用  : ${result.budgetUsed} / ${problem.budget}`);
  console.log(`修复设备(${result.repaired.length}): ${list(result.repaired)}`);
  console.log(`拆解设备(${result.dismantled.length}): ${list(result.dismantled)}`);
  console.log(`闲置设备(${result.idle.length}): ${list(result.idle)}`);

  console.log('\n================ 备件平衡 ================');
  console.log(`${pad('备件', 8)}${pad('库存', 6)}${pad('回收', 6)}${pad('损耗', 6)}${pad('消耗', 6)}剩余`);
  for (const b of balance) {
    console.log(
      `${pad(b.part, 8)}${pad(String(b.stock), 6)}${pad(String(b.salvaged), 6)}` +
      `${pad(String(b.lost), 6)}${pad(String(b.consumed), 6)}${b.leftover}`,
    );
  }

  console.log('\n================ 备件流水（来源 -> 用途） ================');
  if (flows.length === 0) {
    console.log('（无备件流动）');
  } else {
    for (const f of flows) {
      const from = f.from === '库存' ? '库存' : `${nameOf(f.from)}(拆解)`;
      console.log(`${f.part}: ${from} -> ${nameOf(f.to)}(修复) ×${f.qty}`);
    }
  }
}

main();
