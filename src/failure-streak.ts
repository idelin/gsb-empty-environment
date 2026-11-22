/**
 * 账号连续失败跟踪：仅跟踪「存在的账号」。
 * 成功登录会清零；达到阈值由调用方决定是否锁定。
 * 不对不存在的账号登记，避免为枚举行为建立可区分的状态。
 */
export class FailureStreakTracker {
  private readonly streaks = new Map<string, number>();

  recordFailure(account: string, threshold: number): { streak: number; thresholdReached: boolean } {
    const streak = (this.streaks.get(account) ?? 0) + 1;
    this.streaks.set(account, streak);
    return { streak, thresholdReached: streak >= threshold };
  }

  reset(account: string): void {
    this.streaks.delete(account);
  }

  current(account: string): number {
    return this.streaks.get(account) ?? 0;
  }
}
