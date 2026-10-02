/**
 * Demo: dual-clock guardian in action.
 *
 * Scenario: three leases are active while the guardian compares the wall
 * clock against the monotonic clock. Suddenly the system time is set back
 * by one hour. On the very next tick the guardian detects the tear,
 * force-revokes every lease and degrades to safe mode.
 *
 * Run: npm run demo
 */
import { FakeClocks } from '../src/clocks.ts';
import { ClockGuardian } from '../src/clock-guardian.ts';
import { LeaseManager } from '../src/lease-manager.ts';

const TICK_MS = 100;
const clocks = new FakeClocks();
const leaseManager = new LeaseManager(clocks.monotonicNow);
const guardian = new ClockGuardian({ clocks, leaseManager });

guardian.on('trip', (event) => {
  console.log('🚨 CIRCUIT BREAKER TRIPPED');
  console.log(`   reason        : ${event.reason}`);
  console.log(`   detail        : ${event.detail}`);
  console.log(`   wall delta    : ${event.wallDeltaMs}ms`);
  console.log(`   mono delta    : ${event.monoDeltaMs}ms`);
  console.log(`   revoked leases: ${event.revokedLeaseIds.join(', ')}`);
});

guardian.start();

leaseManager.acquire('lease-alpha', 'worker-1', 60_000);
leaseManager.acquire('lease-beta', 'worker-2', 60_000);
leaseManager.acquire('lease-gamma', 'worker-3', 60_000);
console.log(`✅ acquired 3 leases: ${leaseManager.activeLeases().map((l) => l.id).join(', ')}`);

console.log('— healthy ticks (both clocks advance together) —');
for (let i = 0; i < 5; i++) {
  clocks.advance(TICK_MS);
  guardian.tick();
}
console.log(`   mode=${guardian.mode}, active leases=${leaseManager.activeCount()}`);

console.log('— ⚡ system time is suddenly set BACK by 1 hour —');
clocks.stepWall(-3_600_000);
clocks.advanceMonotonicOnly(TICK_MS);
guardian.tick();

console.log(`   mode=${guardian.mode}, active leases=${leaseManager.activeCount()}`);

try {
  leaseManager.acquire('lease-delta', 'worker-4', 60_000);
} catch (err) {
  console.log(`🛡️  new lease rejected in safe mode: ${(err as Error).message}`);
}
