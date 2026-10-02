import { Cluster } from '../src/cluster.ts';
import { GossipNode } from '../src/node.ts';

const N = 100;
const cluster = new Cluster(N, 42);
const fresh = new GossipNode('node-100', N + 1);
cluster.join(fresh, 0);

console.log(`cluster=${N} nodes, new member '${fresh.id}' joins via seed node-0`);
console.log(`log2(N) = ${Math.ceil(Math.log2(N))} periods budget\n`);
console.log(`period 0: aware=${cluster.countAware('node-100')}/${N}`);

for (let p = 1; p <= Math.ceil(Math.log2(N)); p++) {
  cluster.runPeriod();
  const aware = cluster.countAware('node-100');
  console.log(`period ${p}: aware=${aware}/${N}`);
  if (aware === N) {
    console.log(`\nconverged in ${p} periods (<= log2(N)=${Math.ceil(Math.log2(N))})`);
    break;
  }
}
