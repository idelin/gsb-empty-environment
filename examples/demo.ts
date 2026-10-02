import { LeaseRouterGateway } from '../src/gateway.ts';
import { StorageNode } from '../src/node.ts';

const gateway = new LeaseRouterGateway(1000);
gateway.addNode(new StorageNode('node-1', 80));
gateway.addNode(new StorageNode('node-2', 80));

console.log('== bootstrap: single partition [A-Z) leased to node-1 ==');
gateway.bootstrap({ start: 'A', end: 'Z' }, 'node-1');
console.table(gateway.table());

console.log('== [A-Z) grows too large -> split at M, leases to node-1 / node-2 ==');
gateway.splitAt('M', 'node-1', 'node-2');
console.table(gateway.table());
console.log('split event:', JSON.stringify(gateway.splitLog[0], null, 2));

console.log("== concurrent queries: 'Apple' and 'Zoo' ==");
const started = Date.now();
const [apple, zoo] = await Promise.all([gateway.query('Apple'), gateway.query('Zoo')]);
const elapsed = Date.now() - started;
console.log(`Apple -> ${apple.nodeId}: ${apple.value}`);
console.log(`Zoo   -> ${zoo.nodeId}: ${zoo.value}`);
console.log(`both queries finished in ${elapsed}ms (each node latency 80ms -> ran concurrently, non-blocking)`);
