import { buildMultiRingTopology } from './topology.ts';
import type { Network } from './network.ts';

function printRoutes(net: Network, id: string): void {
  const router = net.routers.get(id)!;
  const rows = [...router.routes.values()]
    .sort((a, b) => a.destination.localeCompare(b.destination))
    .map((r) => `    ${id} -> ${r.destination}  下一跳=${r.nextHop}  代价=${r.cost}`)
    .join('\n');
  console.log(`  ${id} 转发表:\n${rows}`);
}

function printNewLogs(net: Network, from: number): void {
  for (const line of net.logs.slice(from)) console.log(`  ${line}`);
}

function check(net: Network, label: string): void {
  const { loops, blackholes } = net.verifyForwarding();
  const ok = loops.length === 0 && blackholes.length === 0;
  console.log(
    `  [校验] ${label}: ${ok ? '无环路、无黑洞 ✔' : `异常 ✘ loops=${loops} blackholes=${blackholes}`}`,
  );
  if (!ok) process.exitCode = 1;
}

const net = buildMultiRingTopology();

console.log('=== 阶段 1: 初始收敛（8 节点多环拓扑） ===');
let mark = net.logs.length;
const t0 = net.runUntilConverged();
printNewLogs(net, mark);
console.log(`  初始收敛耗时 ${t0} tick`);
printRoutes(net, 'R1');
check(net, '初始收敛后转发一致性');

console.log('\n=== 阶段 2: 切断核心主干链路 R3-R5（断纤） ===');
mark = net.logs.length;
net.cutLink('R3', 'R5');
let ticks = 0;
while (!net.converged && ticks < 10) {
  net.tick();
  ticks += 1;
  check(net, `断纤后 tick ${net.tickCount}`);
}
printNewLogs(net, mark);
console.log(`  断纤后重收敛耗时 ${ticks} tick`);
printRoutes(net, 'R1');

console.log('\n=== 阶段 3: 权值变更（R4-R8 备用主干 3 -> 1，流量回切） ===');
mark = net.logs.length;
net.setCost('R4', 'R8', 1);
ticks = 0;
while (!net.converged && ticks < 10) {
  net.tick();
  ticks += 1;
  check(net, `权值变更后 tick ${net.tickCount}`);
}
printNewLogs(net, mark);
printRoutes(net, 'R1');

console.log('\n=== 阶段 4: 链路反复抖动，触发路由震荡抑制 ===');
mark = net.logs.length;
net.setCost('R4', 'R8', 3);
net.runUntilConverged();
for (let i = 0; i < 3; i += 1) {
  net.cutLink('R3', 'R5');
  net.tick();
  net.restoreLink('R3', 'R5');
  net.tick();
}
printNewLogs(net, mark);
const r3 = net.routers.get('R3')!;
console.log(`  R3-R5 当前是否被 R3 抑制: ${r3.isSuppressed('R5')}`);

console.log('\n=== 阶段 5: 惩罚衰减后自动解除抑制，全网恢复 ===');
mark = net.logs.length;
const t5 = net.runUntilConverged(15);
printNewLogs(net, mark);
console.log(`  恢复收敛耗时 ${t5} tick, R3-R5 抑制状态: ${r3.isSuppressed('R5')}`);
printRoutes(net, 'R1');
check(net, '最终转发一致性');

console.log('\n演示完成。');
