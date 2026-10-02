import { HierarchicalGateway } from '../src/gateway.ts';

const gateway = new HierarchicalGateway();

gateway.addCluster({ id: 'cluster-a' });
gateway.addCluster({ id: 'cluster-b' });
gateway.addCluster({ id: 'cluster-c' });

gateway.provisionTenant(100, [
  { id: 't100-node-1' },
  { id: 't100-node-2' },
  { id: 't100-node-3' },
]);
gateway.provisionTenant(200, [
  { id: 't200-node-1' },
  { id: 't200-node-2' },
  { id: 't200-node-3' },
]);

console.log('=== 1. Acceptance route: Tenant=100, Device=ABC ===');
const route = gateway.route({ tenant: 100, device: 'ABC' });
for (const hop of route.hops) {
  console.log(`  layer ${hop.layer}: hash(${hop.key}) -> ${hop.target}`);
}
console.log(`  final: tenant=${route.tenant} device=${route.device} cluster=${route.cluster} node=${route.node}`);

console.log('\n=== 2. Determinism: same request routed 3 times ===');
for (let i = 0; i < 3; i += 1) {
  const again = gateway.route({ tenant: 100, device: 'ABC' });
  console.log(`  attempt ${i + 1}: cluster=${again.cluster} node=${again.node}`);
}

console.log('\n=== 3. Single-tenant scale-out: tenant 100 gains 3 nodes ===');
const probe200 = Array.from({ length: 8 }, (_, i) => `mac-200-${i}`);
const before200 = probe200.map((d) => gateway.nodeOf(200, d));
const probe100 = Array.from({ length: 2000 }, (_, i) => `mac-100-${i}`);
const before100 = probe100.map((d) => gateway.nodeOf(100, d));
gateway.addNode(100, { id: 't100-node-4' });
gateway.addNode(100, { id: 't100-node-5' });
gateway.addNode(100, { id: 't100-node-6' });
const after200 = probe200.map((d) => gateway.nodeOf(200, d));
const after100 = probe100.map((d) => gateway.nodeOf(100, d));
const unchanged200 = before200.every((v, i) => v === after200[i]);
const moved100 = after100.filter((v, i) => v !== before100[i]).length;
console.log('  tenant 100 scaled: t100-node-1..3 -> t100-node-1..6');
console.log(`  tenant 200 mapping unchanged: ${unchanged200}`);
console.log(`  tenant 100 remapped keys: ${moved100}/${probe100.length} (~1/2 expected for 3 -> 6 nodes)`);

console.log('\n=== 4. Node failover: kill the node serving Device=ABC ===');
gateway.setNodeHealth(100, route.node, false);
const rerouted = gateway.route({ tenant: 100, device: 'ABC' });
console.log(`  ${route.node} down -> rerouted to ${rerouted.node} (cluster still ${rerouted.cluster})`);
gateway.setNodeHealth(100, route.node, true);
console.log(`  ${route.node} recovered -> ${gateway.route({ tenant: 100, device: 'ABC' }).node}`);

console.log('\n=== 5. Cluster failover: kill the cluster serving Tenant=100 ===');
gateway.setClusterHealth(route.cluster, false);
const bypassed = gateway.route({ tenant: 100, device: 'ABC' });
console.log(`  ${route.cluster} down -> layer 1 bypasses to ${bypassed.cluster}, node still ${bypassed.node}`);
