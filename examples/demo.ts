/**
 * Running sample: single-machine simulation of the lease protocol.
 *
 * Scenario A — normal timeline, ttl=10s, max drift=500ms:
 *   the primary writes happily until the 9.5s safe boundary, then fences.
 *
 * Scenario B — a sudden +1s wall-clock jump at t=3s:
 *   the drift self-check triggers an emergency demotion, preventing
 *   split-brain before the old lease could be handed to another node.
 */
import { ManualClock } from '../src/clock.ts';
import { LeaseManager } from '../src/lease.ts';

function fmt(monoMs: number): string {
  return `t=${(monoMs / 1000).toFixed(1)}s`;
}

function scenarioA(): void {
  console.log('=== Scenario A: normal timeline (ttl=10s, maxDrift=500ms) ===');
  const clock = new ManualClock();
  const lease = new LeaseManager(clock, { ttlMs: 10_000, maxClockDriftMs: 500 });
  lease.onEvent((e) => {
    const detail = e.command ?? e.reason ?? '';
    console.log(`  [${fmt(e.atMonoMs)}] ${e.type} (term ${e.term}) ${detail}`);
  });

  lease.acquire(1);
  for (let t = 1_000; t <= 11_000; t += 1_000) {
    clock.advance(1_000);
    try {
      lease.write(`write@${t / 1000}s`);
    } catch {
      /* fenced — rejection already logged via event */
    }
  }
  console.log(`  -> primary after 9.5s? ${lease.isPrimary()}\n`);
}

function scenarioB(): void {
  console.log('=== Scenario B: +1s wall-clock jump at t=3s ===');
  const clock = new ManualClock();
  const lease = new LeaseManager(clock, { ttlMs: 10_000, maxClockDriftMs: 500 });
  lease.onEvent((e) => {
    const detail = e.command ?? e.reason ?? '';
    console.log(`  [${fmt(e.atMonoMs)}] ${e.type} (term ${e.term}) ${detail}`);
  });

  lease.acquire(1);
  clock.advance(3_000);
  lease.write('write@3s');
  console.log('  -- inject: wall clock jumps forward 1000ms --');
  clock.jumpWall(1_000);
  try {
    lease.write('write-after-jump');
  } catch {
    /* fenced */
  }
  console.log(`  -> primary after jump? ${lease.isPrimary()} (split-brain prevented)`);
}

scenarioA();
scenarioB();
