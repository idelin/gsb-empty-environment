import { pathToFileURL } from 'node:url';
import { Simulation } from './simulation.ts';

/**
 * 验收场景：
 * - tick 100：A 发起租约；tick 105：多数派 {A,B} 确认，A 成为主，租约 [105, 150)。
 * - tick 106：注入网络黑洞，隔离 A <-> B、A <-> C，A 的心跳/续期全部丢失。
 * - tick 150：A 租约到期且无多数派续期，自动降级为候选者；B 同时发起竞选。
 * - tick 155：B 获多数派 {B,C} 选为新主，租约 [155, 200)。
 * - tick 157：黑洞恢复后 A 的旧请求到达，被新主 B 与 C 拒绝；A 降级为 follower。
 */
export function runAcceptanceScenario(): Simulation {
  const sim = new Simulation();
  sim.addNode('A');
  sim.addNode('B');
  sim.addNode('C');
  const nodeA = sim.nodes.get('A')!;

  sim.schedule(100, () => nodeA.campaign());
  sim.schedule(106, (s) =>
    s.injectBlackhole(106, 153, [
      ['A', 'B'],
      ['A', 'C'],
    ]),
  );
  sim.schedule(147, (s) => void s.write('A', 'x=1'));
  sim.schedule(149, (s) => void s.write('A', 'x=2'));
  sim.schedule(156, (s) => {
    void s.write('A', 'x=3');
    void s.write('B', 'y=1');
  });
  sim.run(165);
  return sim;
}

/** 对照场景：无黑洞，验证多数派心跳应答驱动租约续期 */
export function runRenewalScenario(): Simulation {
  const sim = new Simulation();
  sim.addNode('A');
  sim.addNode('B');
  sim.addNode('C');
  const nodeA = sim.nodes.get('A')!;
  sim.schedule(100, () => nodeA.campaign());
  sim.run(165);
  return sim;
}

function printEvents(sim: Simulation, title: string): void {
  console.log(`\n=== ${title} ===`);
  console.log('tick  node  event               detail');
  console.log('----  ----  ------------------  ------------------------------------------');
  for (const e of sim.events) {
    console.log(
      `${String(e.tick).padEnd(5)} ${e.node.padEnd(5)} ${e.event.padEnd(19)} ${e.detail}`,
    );
  }
  const violations = sim.writeCapableTimeline.filter(
    (t) => t.primaries.length > 1,
  );
  console.log(
    `\nsplit-brain check: ${violations.length === 0 ? 'OK (每个 tick 至多一个可写主节点)' : `VIOLATED at ${violations.map((v) => v.tick).join(',')}`}`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  printEvents(runAcceptanceScenario(), '验收场景：黑洞注入与主备切换');
  printEvents(runRenewalScenario(), '对照场景：多数派心跳续期');
}
