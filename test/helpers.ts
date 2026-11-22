import { AuthGuard } from '../src/guard.ts';
import { ManualClock } from '../src/clock.ts';
import { FastHasher } from '../src/hasher.ts';
import { InMemoryUserStore } from '../src/store.ts';
import type { GuardConfig } from '../src/types.ts';

/** 测试统一使用的固定耗时基线，便于断言耗时完全确定。 */
export const FIXED_DURATION = 50;

/** 构造使用手动时钟、快速哈希和高限速默认值的测试实例。 */
export async function buildTestGuard(config: Partial<GuardConfig> = {}) {
  const clock = new ManualClock(1_000);
  const hasher = new FastHasher();
  const store = new InMemoryUserStore(hasher);
  const guard = new AuthGuard({
    clock,
    hasher,
    store,
    config: { fixedAuthDurationMs: FIXED_DURATION, ...config },
  });
  await store.addUser('alice', 'correct-horse');
  await store.addUser('bob', 'battery-staple');
  return { guard, clock, store };
}

/** 等待恒定耗时的微任务兑现，并确认没有遗留未结算的 sleep。 */
export async function settle(clock: ManualClock): Promise<void> {
  await Promise.resolve();
  if (clock.hasPending()) {
    throw new Error('存在未兑现的恒定耗时 sleep');
  }
}
