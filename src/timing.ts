import type { Clock } from './clock.ts';

/**
 * 以固定墙钟耗时执行 work，无论 work 实际快慢或走哪条分支，
 * 都等到 baselineMs 之后再返回，从而消除基于耗时的侧信道。
 *
 * 若 work 本身超过基线，则不额外等待（取 max）。
 */
export async function runInConstantTime<T>(
  clock: Clock,
  baselineMs: number,
  work: () => Promise<T>,
): Promise<T> {
  const startedAt = clock.now();
  try {
    return await work();
  } finally {
    const elapsed = clock.now() - startedAt;
    const remaining = Math.max(0, baselineMs - elapsed);
    if (remaining > 0) {
      await clock.sleep(remaining);
    }
  }
}

/** 账号归一化：去除空白并转小写，作为限速与存储键。 */
export function normalizeAccount(account: string): string {
  return account.trim().toLowerCase();
}
