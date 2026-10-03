import { VirtualClock } from '../src/clock.ts';
import { SimulatedNetwork, type Message } from '../src/network.ts';
import { SwimNode } from '../src/swim.ts';

function describe(m: Message): string {
  switch (m.type) {
    case 'ping':
      return `${m.from} -> ${m.to}  PING seq=${m.seq}` +
        (m.onBehalfOf ? ` (on behalf of ${m.onBehalfOf})` : '');
    case 'ack':
      return `${m.from} -> ${m.to}  ACK  seq=${m.seq}` +
        (m.target ? ` (target=${m.target})` : '') +
        (m.onBehalfOf ? ` (relayed for ${m.onBehalfOf})` : '');
    case 'ping-req':
      return `${m.from} -> ${m.to}  PING-REQ target=${m.target} seq=${m.seq}`;
  }
}

// Scenario: weak network where the direct A<->B link drops all packets,
// but the proxy paths through C and D are healthy. K = 2.
const clock = new VirtualClock();
const net = new SimulatedNetwork(clock, 5);
const ids = ['A', 'B', 'C', 'D'];
const nodes = new Map<string, SwimNode>();

for (const id of ids) {
  const node = new SwimNode(id, net, clock, {
    periodMs: 1000,
    ackTimeoutMs: 300,
    indirectK: 2,
    rng: () => 0, // deterministic demo: A probes B, proxies C then D
  });
  node.join(ids);
  net.register(node);
  nodes.set(id, node);
}

net.dropLink('A', 'B');
console.log('Scenario: direct link A<->B drops all packets; proxies C/D healthy.\n');

nodes.get('A')!.start();
clock.advance(2500);

console.log('Message timeline:');
for (const entry of net.log) {
  const mark = entry.delivered ? '   ' : ' X ';
  console.log(`  [t=${String(entry.at).padStart(4)}ms] ${mark}${describe(entry.message)}`);
}

console.log('\nA\'s membership view after the run:');
for (const [peer, state] of nodes.get('A')!.members) {
  console.log(`  ${peer}: ${state}`);
}
