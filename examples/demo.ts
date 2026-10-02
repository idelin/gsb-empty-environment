/**
 * 运行样例：4 节点集群，A<->B 直连链路丢包（弱网），
 * A 每个周期通过代理 C/D 间接探测 B，B 始终保持 Alive。
 * 运行：node --experimental-strip-types examples/demo.ts  （Node >= 22.6）
 */
import { SystemClock } from '../src/clock.ts';
import { SimulatedNetwork } from '../src/transport.ts';
import { SwimNode } from '../src/swim.ts';

const clock = new SystemClock();
const network = new SimulatedNetwork(clock, 30);

// 弱网场景：A 与 B 之间直连双向丢包，其余链路正常。
network.dropBidirectional('A', 'B');

// 可复现的伪随机源（LCG）。
function lcg(seed: number): () => number {
  let state = seed;
  return () => {
    state = (state * 1103515245 + 12345) % 2147483648;
    return state / 2147483648;
  };
}

const IDS = ['A', 'B', 'C', 'D'];
const startedAt = Date.now();

const nodes = IDS.map((id, index) => {
  // A 固定选第一个存活成员（即 B）作为探测目标，确保演示间接探测路径。
  const random = id === 'A' ? () => 0 : lcg(42 + index);
  return new SwimNode({
    id,
    peers: IDS,
    clock,
    transport: network.attach(id),
    periodMs: 1000,
    pingTimeoutMs: 300,
    indirectTimeoutMs: 500,
    k: 2,
    random,
    onEvent: (event) => {
      const at = String(Date.now() - startedAt).padStart(4);
      console.log(`[+${at}ms]`, JSON.stringify(event));
    },
  });
});

console.log('SWIM demo: A<->B 直连丢包，A 通过代理 C/D 间接探测 B\n');
for (const node of nodes) node.start();

setTimeout(() => {
  for (const node of nodes) node.stop();
  console.log('\n最终成员视图：');
  for (const node of nodes) {
    const view = IDS.filter((id) => id !== node.id)
      .map((id) => `${id}=${node.statusOf(id)}`)
      .join(' ');
    console.log(`  节点 ${node.id}: ${view}`);
  }
  process.exit(0);
}, 3600);
