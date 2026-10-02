/**
 * Multi-tenant resource scheduler with isolation and weighted fair queuing.
 *
 * - Isolation: each tenant has its own FIFO queue, concurrency cap
 *   (`maxConcurrent`) and queue-length quota (`maxQueue`). One tenant's load
 *   or failures never consume another tenant's permits.
 * - Fairness: tenants are served in Deficit Round Robin order. Each visit a
 *   tenant's deficit counter is credited `weight * quantum`; dispatching one
 *   task costs 1 unit. Heavier-weight tenants therefore receive
 *   proportionally more dispatch slots, while no tenant can starve others.
 */

export interface TenantConfig {
  id: string;
  /** Relative share of dispatch capacity (positive integer). */
  weight: number;
  /** Max tasks of this tenant running at the same time. */
  maxConcurrent: number;
  /** Max tasks of this tenant waiting in queue (0 = unbounded). */
  maxQueue?: number;
}

export class QuotaExceededError extends Error {
  readonly tenantId: string;
  readonly limit: number;

  constructor(tenantId: string, limit: number) {
    super(`tenant "${tenantId}" queue quota exceeded (limit=${limit})`);
    this.name = 'QuotaExceededError';
    this.tenantId = tenantId;
    this.limit = limit;
  }
}

export class UnknownTenantError extends Error {
  readonly tenantId: string;

  constructor(tenantId: string) {
    super(`tenant "${tenantId}" is not registered`);
    this.name = 'UnknownTenantError';
    this.tenantId = tenantId;
  }
}

interface QueuedTask {
  run: () => Promise<unknown>;
  resolve: (value: unknown) => void;
  reject: (err: unknown) => void;
}

interface TenantState {
  config: Required<Omit<TenantConfig, 'maxQueue'>> & { maxQueue: number };
  queue: QueuedTask[];
  deficit: number;
  running: number;
  /** True while the tenant is in the active round-robin list. */
  enqueued: boolean;
}

export class TenantScheduler {
  private readonly tenants = new Map<string, TenantState>();
  /** Round-robin ring of tenants that currently have queued work. */
  private readonly ring: TenantState[] = [];
  private readonly quantum: number;
  private pumping = false;
  private pumpScheduled = false;

  constructor(configs: TenantConfig[], quantum = 1) {
    if (quantum <= 0) throw new Error('quantum must be positive');
    this.quantum = quantum;
    for (const c of configs) {
      if (c.weight <= 0) throw new Error(`tenant "${c.id}": weight must be positive`);
      if (c.maxConcurrent <= 0)
        throw new Error(`tenant "${c.id}": maxConcurrent must be positive`);
      if (this.tenants.has(c.id)) throw new Error(`tenant "${c.id}" registered twice`);
      this.tenants.set(c.id, {
        config: { ...c, maxQueue: c.maxQueue ?? 0 },
        queue: [],
        deficit: 0,
        running: 0,
        enqueued: false,
      });
    }
  }

  /** Submit a task; the returned promise settles with the task's result. */
  submit<T>(tenantId: string, task: () => Promise<T>): Promise<T> {
    const tenant = this.tenants.get(tenantId);
    if (!tenant) return Promise.reject(new UnknownTenantError(tenantId));
    if (tenant.config.maxQueue > 0 && tenant.queue.length >= tenant.config.maxQueue) {
      return Promise.reject(new QuotaExceededError(tenantId, tenant.config.maxQueue));
    }
    return new Promise<T>((resolve, reject) => {
      tenant.queue.push({
        run: task as () => Promise<unknown>,
        resolve: resolve as (v: unknown) => void,
        reject,
      });
      this.enqueueTenant(tenant);
      this.schedulePump();
    });
  }

  /** Snapshot of per-tenant state, for observability and tests. */
  stats(tenantId: string): { queued: number; running: number; deficit: number } {
    const t = this.tenants.get(tenantId);
    if (!t) throw new UnknownTenantError(tenantId);
    return { queued: t.queue.length, running: t.running, deficit: t.deficit };
  }

  private enqueueTenant(tenant: TenantState): void {
    if (!tenant.enqueued) {
      tenant.enqueued = true;
      this.ring.push(tenant);
    }
  }

  /**
   * Batch submissions made within the same event-loop tick into a single
   * pump, so DRR weights apply to the whole backlog rather than to tasks
   * trickling in one by one.
   */
  private schedulePump(): void {
    if (this.pumpScheduled) return;
    this.pumpScheduled = true;
    queueMicrotask(() => {
      this.pumpScheduled = false;
      this.pump();
    });
  }

  /**
   * DRR dispatch loop. Each full pass credits every backlogged tenant with
   * `weight * quantum` deficit and dispatches queued tasks while deficit and
   * the tenant's concurrency cap allow. Runs at most one macrotask slice.
   */
  private pump(): void {
    if (this.pumping) return;
    this.pumping = true;
    try {
      let progress = true;
      while (progress && this.ring.length > 0) {
        progress = false;
        const n = this.ring.length;
        for (let i = 0; i < n; i++) {
          const tenant = this.ring.shift()!;
          tenant.enqueued = false;
          tenant.deficit += tenant.config.weight * this.quantum;
          while (
            tenant.deficit >= 1 &&
            tenant.queue.length > 0 &&
            tenant.running < tenant.config.maxConcurrent
          ) {
            tenant.deficit -= 1;
            const task = tenant.queue.shift()!;
            tenant.running += 1;
            progress = true;
            void this.execute(tenant, task);
          }
          if (tenant.queue.length > 0) this.enqueueTenant(tenant);
          else tenant.deficit = 0; // DRR: idle tenants bank no deficit
        }
      }
    } finally {
      this.pumping = false;
    }
  }

  private async execute(tenant: TenantState, task: QueuedTask): Promise<void> {
    try {
      const value = await task.run();
      task.resolve(value);
    } catch (err) {
      task.reject(err);
    } finally {
      // Permit is always released — failures never leak capacity.
      tenant.running -= 1;
      this.schedulePump();
    }
  }
}
