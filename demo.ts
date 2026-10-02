/**
 * Runnable HLC demo: node demo.ts
 *
 * Uses a scriptable fake physical clock so the acceptance scenarios
 * (stall, backward jump, forward jump, drift bound, causal exchange)
 * are reproduced deterministically in a single run.
 */
import {
  HybridLogicalClock,
  ClockDriftError,
  toString,
  type HLCTimestamp,
} from './src/hlc.ts';

class FakeClock {
  t: number;
  constructor(start: number) {
    this.t = start;
  }
  now = (): number => this.t;
  advance(ms: number): void {
    this.t += ms;
  }
  set(ms: number): void {
    this.t = ms;
  }
}

function show(label: string, ts: HLCTimestamp, clock: FakeClock): void {
  const drift = ts.wallTime - clock.t;
  console.log(
    `  ${label.padEnd(28)} hlc=${toString(ts)}  ` +
      `(physical=${clock.t}, drift=${drift >= 0 ? '+' : ''}${drift}ms)`,
  );
}

const EPSILON = 500;
const clock = new FakeClock(Date.now());
const hlc = new HybridLogicalClock({ now: clock.now, epsilon: EPSILON });

console.log(`=== HLC demo (epsilon = ${EPSILON}ms) ===\n`);

console.log('1) Normal operation — timestamps follow the physical clock:');
for (let i = 0; i < 3; i++) {
  clock.advance(1);
  show(`tick ${i + 1}`, hlc.now(), clock);
}

console.log('\n2) Physical clock STALLS — logical counter keeps output monotonic:');
clock.advance(5);
show('before stall', hlc.now(), clock);
for (let i = 0; i < 3; i++) show(`stalled tick ${i + 1}`, hlc.now(), clock);

console.log('\n3) Physical clock jumps BACKWARD 50ms — output never regresses:');
clock.set(clock.t - 50);
for (let i = 0; i < 3; i++) show(`after backward jump ${i + 1}`, hlc.now(), clock);

console.log('\n4) Physical clock jumps FORWARD 60s — HLC smoothly catches up:');
clock.advance(60_000);
show('after forward jump', hlc.now(), clock);
clock.advance(1);
show('next tick', hlc.now(), clock);

console.log('\n5) Causal exchange between two nodes:');
const clockB = new FakeClock(clock.t + 20);
const nodeB = new HybridLogicalClock({ now: clockB.now, epsilon: EPSILON });
const sent = hlc.now();
show('A sends message', sent, clock);
const delivered = nodeB.update(sent);
show('B receives (update)', delivered, clockB);
const reply = nodeB.now();
show('B replies', reply, clockB);
const merged = hlc.update(reply);
show('A merges reply', merged, clock);
console.log(`  causal chain holds: ${toString(sent)} < ${toString(delivered)} < ${toString(reply)} < ${toString(merged)}`);

console.log('\n6) Drift bound — physical clock falls more than epsilon behind:');
clock.set(hlc.last().wallTime - (EPSILON + 1));
try {
  hlc.now();
  console.log('  ERROR: expected ClockDriftError');
} catch (err) {
  if (err instanceof ClockDriftError) {
    console.log(`  refused to emit timestamp: ${err.message}`);
  } else {
    throw err;
  }
}

console.log('\nDemo complete.');
