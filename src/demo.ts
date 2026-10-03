import { Network } from './network.ts';
import { buildTopology } from './topology.ts';

function printFib(net: Network, id: string): void {
  const router = net.nodes.get(id);
  if (!router) return;
  const rows = [...router.fib.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([dest, e]) => `    ${id} -> ${dest}  next-hop ${e.nextHop}  cost ${e.cost}`);
  console.log(`  FIB of ${id}:`);
  console.log(rows.join('\n'));
}

const net = new Network();
buildTopology(net);

console.log('=== Phase 1: initial LSA flooding + SPF convergence (8-node multi-ring) ===');
net.runUntilConverged();
console.log(net.log.format().join('\n'));
console.log(`\nConverged at tick ${net.tick}. All-pairs check: ${net.checkAllPairs().length === 0 ? 'OK (no loops, no blackholes)' : 'FAILED'}`);
printFib(net, 'N1');
printFib(net, 'N5');
console.log(`\n  trace N1 -> N5: ${net.traceRoute('N1', 'N5').path.join(' -> ')}`);

console.log('\n=== Phase 2: fiber cut on core backbone link N3-N5 ===');
let mark = net.log.entries.length;
const cutTick = net.tick;
net.cutLink('N3', 'N5');
net.runUntilConverged();
console.log(net.log.format(net.log.since(mark)).join('\n'));
console.log(`\nReconverged at tick ${net.tick} (cut at tick ${cutTick}, took ${net.tick - cutTick} tick(s)).`);
console.log(`All-pairs check after cut: ${net.checkAllPairs().length === 0 ? 'OK (no loops, no blackholes)' : 'FAILED'}`);
printFib(net, 'N1');
console.log(`\n  trace N1 -> N5: ${net.traceRoute('N1', 'N5').path.join(' -> ')}  (rerouted via backup backbone N4-N8)`);

console.log('\n=== Phase 3: metric change on backup backbone N4-N8 (cost 2 -> 10) ===');
mark = net.log.entries.length;
net.setLinkCost('N4', 'N8', 10);
net.runUntilConverged();
console.log(net.log.format(net.log.since(mark)).join('\n'));
console.log(`\n  trace N1 -> N5: ${net.traceRoute('N1', 'N5').path.join(' -> ')}  (now via long-haul N2-N6)`);

console.log('\n=== Phase 4: link flapping N2-N6 -> route oscillation avoidance (SPF backoff) ===');
mark = net.log.entries.length;
for (let i = 0; i < 10; i++) {
  if (i % 2 === 0) net.cutLink('N2', 'N6');
  else net.restoreLink('N2', 'N6');
  net.step();
}
console.log(net.log.format(net.log.since(mark)).join('\n'));
const n2 = net.nodes.get('N2');
console.log(`\nN2 SPF runs during 10-tick flap storm: ${n2 ? n2.spfRuns : '?'} total (throttled by hold-down backoff)`);

console.log('\n=== Phase 5: flapping stops, network stabilizes ===');
mark = net.log.entries.length;
net.runUntilConverged(30);
console.log(net.log.format(net.log.since(mark)).join('\n'));
console.log(`\nFinal state at tick ${net.tick}. All-pairs check: ${net.checkAllPairs().length === 0 ? 'OK (no loops, no blackholes)' : 'FAILED'}`);
