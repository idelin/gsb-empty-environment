export interface TenantConfig {
  readonly id: string;
  readonly weight?: number;
  readonly quotaTokens?: number;
  readonly refillIntervalMs?: number;
  readonly maxConcurrency?: number;
  readonly maxQueueSize?: number;
}

export interface SchedulerOptions {
  readonly baseQuantum?: number;
  readonly maxInflight?: number;
  readonly now?: () => number;
}

export interface TenantStats {
  readonly id: string;
  readonly submitted: number;
  readonly completed: number;
  readonly failed: number;
  readonly rejected: number;
  readonly queued: number;
  readonly inflight: number;
  readonly tokens: number;
  readonly deficit: number;
}

export class QuotaExceededError extends Error {
  readonly tenantId: string;

  constructor(tenantId: string, message: string) {
    super(message);
    this.name = 'QuotaExceededError';
    this.tenantId = tenantId;
  }
}

interface JobEntry {
  readonly size: number;
  readonly run: () => Promise<unknown>;
  readonly resolve: (value: unknown) => void;
  readonly reject: (err: unknown) => void;
}

interface TenantState {
  readonly id: string;
  readonly weight: number;
  readonly quantum: number;
  readonly quotaTokens: number;
  readonly refillIntervalMs: number;
  readonly maxConcurrency: number;
  readonly maxQueueSize: number;
  tokens: number;
  lastRefill: number;
  deficit: number;
  inflight: number;
  active: boolean;
  readonly queue: JobEntry[];
  submitted: number;
  completed: number;
  failed: number;
  rejected: number;
}

const INF = Number.POSITIVE_INFINITY;

export class MultiTenantScheduler {
  private readonly tenants = new Map<string, TenantState>();
  private readonly activeList: TenantState[] = [];
  private readonly baseQuantum: number;
  private readonly maxInflight: number;
  private readonly now: () => number;
  private inflightTotal = 0;
  private pumping = false;
  private drainWaiters: Array<() => void> = [];

  constructor(options: SchedulerOptions = {}) {
    this.baseQuantum = options.baseQuantum ?? 1;
    this.maxInflight = options.maxInflight ?? INF;
    this.now = options.now ?? Date.now;
    if (!(this.baseQuantum > 0)) throw new RangeError('baseQuantum 必须为正数');
    if (!(this.maxInflight > 0)) throw new RangeError('maxInflight 必须为正数');
  }

  registerTenant(config: TenantConfig): void {
    if (this.tenants.has(config.id)) {
      throw new Error('租户 ' + config.id + ' 已注册');
    }
    const weight = config.weight ?? 1;
    const quotaTokens = config.quotaTokens ?? INF;
    const refillIntervalMs = config.refillIntervalMs ?? 1000;
    const maxConcurrency = config.maxConcurrency ?? INF;
    const maxQueueSize = config.maxQueueSize ?? INF;
    if (!(weight > 0)) throw new RangeError('weight 必须为正数');
    if (!(quotaTokens > 0)) throw new RangeError('quotaTokens 必须为正数');
    if (!(refillIntervalMs > 0)) throw new RangeError('refillIntervalMs 必须为正数');
    if (!(maxConcurrency >= 1)) throw new RangeError('maxConcurrency 必须 >= 1');
    if (!(maxQueueSize >= 1)) throw new RangeError('maxQueueSize 必须 >= 1');
    this.tenants.set(config.id, {
      id: config.id,
      weight,
      quantum: this.baseQuantum * weight,
      quotaTokens,
      refillIntervalMs,
      maxConcurrency,
      maxQueueSize,
      tokens: quotaTokens,
      lastRefill: this.now(),
      deficit: 0,
      inflight: 0,
      active: false,
      queue: [],
      submitted: 0,
      completed: 0,
      failed: 0,
      rejected: 0,
    });
  }

  submit<T>(tenantId: string, size: number, run: () => Promise<T> | T): Promise<T> {
    const tenant = this.tenants.get(tenantId);
    if (!tenant) throw new Error('未注册的租户 ' + tenantId);
    if (!Number.isFinite(size) || size <= 0) {
      throw new RangeError('size 必须为正的有限数');
    }
    tenant.submitted++;
    if (tenant.queue.length >= tenant.maxQueueSize) {
      tenant.rejected++;
      return Promise.reject(
        new QuotaExceededError(
          tenantId,
          '租户 ' + tenantId + ' 队列已满（上限 ' + tenant.maxQueueSize + '），任务已拒绝（隔离保护）',
        ),
      );
    }
    let resolveFn!: (value: unknown) => void;
    let rejectFn!: (err: unknown) => void;
    const promise = new Promise<T>((resolve, reject) => {
      resolveFn = resolve as (value: unknown) => void;
      rejectFn = reject;
    });
    tenant.queue.push({
      size,
      run: run as () => Promise<unknown>,
      resolve: resolveFn,
      reject: rejectFn,
    });
    if (!tenant.active) {
      tenant.active = true;
      this.activeList.push(tenant);
    }
    this.pump();
    return promise;
  }

  tick(): void {
    this.pump();
  }

  stats(tenantId: string): TenantStats {
    const t = this.tenants.get(tenantId);
    if (!t) throw new Error('未注册的租户 ' + tenantId);
    return {
      id: t.id,
      submitted: t.submitted,
      completed: t.completed,
      failed: t.failed,
      rejected: t.rejected,
      queued: t.queue.length,
      inflight: t.inflight,
      tokens: t.tokens,
      deficit: t.deficit,
    };
  }

  drain(): Promise<void> {
    if (this.isIdle()) return Promise.resolve();
    return new Promise<void>((resolve) => {
      this.drainWaiters.push(resolve);
    });
  }

  assertNoLeaks(): void {
    const problems: string[] = [];
    for (const t of this.tenants.values()) {
      if (t.queue.length > 0) problems.push('租户 ' + t.id + ' 残留 ' + t.queue.length + ' 个排队任务');
      if (t.inflight > 0) problems.push('租户 ' + t.id + ' 残留 ' + t.inflight + ' 个在途任务');
      if (t.active) problems.push('租户 ' + t.id + ' 仍挂在 DRR 活跃链表');
      if (t.deficit !== 0) problems.push('租户 ' + t.id + ' 的 deficit 未归零（' + t.deficit + '）');
    }
    if (this.activeList.length > 0) {
      problems.push('DRR 活跃链表残留 ' + this.activeList.length + ' 个租户');
    }
    if (this.inflightTotal !== 0) {
      problems.push('全局在途计数未归零（' + this.inflightTotal + '）');
    }
    if (this.drainWaiters.length > 0) {
      problems.push('存在 ' + this.drainWaiters.length + ' 个未兑现的 drain 等待者');
    }
    if (problems.length > 0) {
      throw new Error('调度器泄漏检测失败：' + problems.join('；'));
    }
  }

  private isIdle(): boolean {
    for (const t of this.tenants.values()) {
      if (t.queue.length > 0 || t.inflight > 0) return false;
    }
    return true;
  }

  private checkDrained(): void {
    if (!this.isIdle()) return;
    const waiters = this.drainWaiters;
    this.drainWaiters = [];
    for (const resolve of waiters) resolve();
  }

  private refill(t: TenantState): void {
    if (!Number.isFinite(t.quotaTokens)) return;
    const now = this.now();
    const elapsed = now - t.lastRefill;
    if (elapsed < t.refillIntervalMs) return;
    const periods = Math.floor(elapsed / t.refillIntervalMs);
    t.tokens = Math.min(t.quotaTokens, t.tokens + periods * t.quotaTokens);
    t.lastRefill += periods * t.refillIntervalMs;
  }

  private pump(): void {
    if (this.pumping) return;
    this.pumping = true;
    try {
      let stalled = 0;
      while (this.activeList.length > 0) {
        if (this.inflightTotal >= this.maxInflight) break;
        if (stalled >= this.activeList.length) break;
        const t = this.activeList[0];
        this.refill(t);
        const head = t.queue[0];
        if (!head) {
          this.activeList.shift();
          t.active = false;
          t.deficit = 0;
          stalled++;
          continue;
        }
        if (t.inflight >= t.maxConcurrency || t.tokens < head.size) {
          this.activeList.push(this.activeList.shift()!);
          stalled++;
          continue;
        }
        const deficitBefore = t.deficit;
        t.deficit += t.quantum;
        let sent = false;
        while (
          t.queue.length > 0 &&
          t.queue[0].size <= t.deficit &&
          t.inflight < t.maxConcurrency &&
          t.tokens >= t.queue[0].size &&
          this.inflightTotal < this.maxInflight
        ) {
          const job = t.queue.shift()!;
          t.deficit -= job.size;
          if (Number.isFinite(t.quotaTokens)) t.tokens -= job.size;
          t.inflight++;
          this.inflightTotal++;
          this.dispatch(t, job);
          sent = true;
        }
        if (t.queue.length === 0) {
          this.activeList.shift();
          t.active = false;
          t.deficit = 0;
        } else {
          this.activeList.push(this.activeList.shift()!);
        }
        stalled = sent || t.deficit > deficitBefore ? 0 : stalled + 1;
      }
    } finally {
      this.pumping = false;
    }
  }

  private dispatch(t: TenantState, job: JobEntry): void {
    Promise.resolve()
      .then(() => job.run())
      .then(
        (value) => {
          t.inflight--;
          this.inflightTotal--;
          t.completed++;
          job.resolve(value);
          this.settle();
        },
        (err) => {
          t.inflight--;
          this.inflightTotal--;
          t.failed++;
          job.reject(err);
          this.settle();
        },
      );
  }

  private settle(): void {
    this.pump();
    this.checkDrained();
  }
}
