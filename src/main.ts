/**
 * CLI 入口。
 *
 * 用法：
 *   node src/main.ts [--buggy] [--net-cap N] [--max-steps K] [--max-states N]
 *
 *   --buggy        注入故障：允许节点在同一任期重复投票（演示反例）
 *   --net-cap N    在途消息数上限，默认 3
 *   --max-steps K  只探索 K 步以内的执行（结论为 bounded，非证明）
 *   --max-states N 状态数资源上限（结论为 bounded，非证明）
 */

import { check } from './checker.ts';
import type { State } from './model.ts';

function argNum(args: string[], name: string): number | undefined {
  const i = args.indexOf(name);
  if (i < 0) return undefined;
  const v = Number(args[i + 1]);
  if (!Number.isInteger(v) || v < 0) {
    console.error(`参数 ${name} 需要非负整数，收到: ${args[i + 1]}`);
    process.exit(2);
  }
  return v;
}

function renderState(s: State): string {
  const nodes = s.nodes
    .map((n, i) => {
      const role = { follower: 'F', candidate: 'C', leader: 'L' }[n.role];
      const vote = n.votedTerm < 0 ? '-' : `n${n.votedFor}@t${n.votedTerm}`;
      const extra = n.role === 'leader' ? ` 当选@t${n.leaderTerm}` : '';
      return `n${i}:${role} t=${n.term} 票=${vote}${extra}`;
    })
    .join(' | ');
  const net =
    s.net.length === 0
      ? '[]'
      : s.net
          .map((m) => `${m.kind === 'req' ? 'req' : 'resp'}(n${m.from}→n${m.to},t${m.term})`)
          .join(' ');
  return `${nodes}\n           网络: ${net}`;
}

const args = process.argv.slice(2);
const buggy = args.includes('--buggy');
const netCap = argNum(args, '--net-cap') ?? 3;
const maxSteps = argNum(args, '--max-steps');
const maxStates = argNum(args, '--max-states');

console.log(
  `配置: 节点=3 多数派=2 网络容量=${netCap} 模式=${buggy ? 'buggy(允许同任期重复投票)' : '正确(每任期一票)'}` +
    (maxSteps !== undefined ? ` 步数界=${maxSteps}` : '') +
    (maxStates !== undefined ? ` 状态数界=${maxStates}` : ''),
);

const t0 = Date.now();
const result = check({ netCap, buggy }, { maxSteps, maxStates });
const ms = Date.now() - t0;

switch (result.type) {
  case 'counterexample': {
    console.log(`\n结果: COUNTEREXAMPLE — 发现反例（最短轨迹 ${result.trace.length - 1} 步）`);
    console.log(`违反性质: ${result.violation}`);
    console.log(`已探索状态数: ${result.explored}，耗时 ${ms}ms\n`);
    result.trace.forEach((e, i) => {
      console.log(`  步骤 ${i}: ${e.label}`);
      console.log(`           ${renderState(e.state)}`);
    });
    process.exitCode = 1;
    break;
  }
  case 'proof': {
    console.log(`\n结果: PROOF — 状态空间已穷尽，不存在反例`);
    console.log(`已探索状态数: ${result.explored}，最大深度: ${result.maxDepth}，耗时 ${ms}ms`);
    console.log(
      `结论: 对任意步数、任意任期取值的执行（消息可任意延迟/重排/丢弃，在途消息 ≤ ${netCap}），`,
    );
    console.log(`      同一任期不可能出现两个多数派领导者。（基于任期截断编码的精确互模拟）`);
    break;
  }
  case 'bounded': {
    console.log(`\n结果: BOUNDED — 界内未发现反例（非证明）`);
    console.log(`原因: ${result.reason}`);
    console.log(`已探索状态数: ${result.explored}，耗时 ${ms}ms`);
    console.log(`说明: 该结论只对所探索的有限前缀成立，不能排除更深处的反例。`);
    break;
  }
}
