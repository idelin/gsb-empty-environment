/**
 * Multi-tenant fair scheduler.
 *
 * Isolation:  every tenant gets its own FIFO queue, its own in-flight
 *             concurrency quota, and its own bounded backlog. One tenant can
 *             never starve, block, or exhaust the resources of another.
 * Fairness:   tenants are served with Deficit Round Robin (DRR). When the
 *             round-robin pointer visits a backlogged tenant, the tenant's
 *             deficit is charged with `weight * baseQuantum` tokens; queued
 *             jobs dispatch while their cost fits the deficit. Leftover
 *             deficit carries over to the next visit, so long-run service is
 *             proportional to weight regardless of job-cost mix.
 * Safety:     permits are acquired before dispatch and released in a
 *             `finally` block, so a throwing or rejecting task can never
 *             leak a slot. `drain()` lets callers assert the scheduler
 *             returns to a completely idle state.
 */

export interface TenantOptions {
  /** DRR weight; service share is proportional to weight. Default 1. */
  weight?: number;
  /** Max concurrently running jobs for this tenant. Default Infinity. */
  maxConcurrent?: number;
  /** Max queued (not yet dispatched) jobs; overflow is rejected. Default Infinity. */
  maxQueue?: number;
}

export interface SchedulerOptions {
  /** Token unit charged per DRR visit, multiplied by tenant weight. Default 1. */
  baseQuantum?: number;
  /** Global cap on concurrently running jobs across all tenants. Default Infinity. */
  maxConcurrentTotal?: number;
}

export interface Task<T> {
  /** Deficit cost charged to the tenant. Default 1. */
  cost?: number;
  run: () => T | Promise<T>;
}

export interface TenantStats {
  id: string;
  weight: number;
  maxConcurrent: number;
  queued: number;
  inFlight: number;
  dispatched: number;
  completed: number;
  failed: number;
  servedCost: number;
  deficit: number;
}

export class SchedulerError extends Error {
  readonly code: "unknown-tenant" | "queue-full";
  constructor(code: "unknown-tenant" | "queue-full", message: string) {
    super(message);
    this.name = "SchedulerError";
    this.code = code;
  }
}

interface Job {
  cost: number;
  run: () => unknown;
  resolve: (value: unknown) => void;
  reject: (reason: unknown) => void;
}

interface Tenant {
  id: string;
  weight: number;
  maxConcurrent: number;
  maxQueue: number;
  queue: Job[];
  deficit: number;
  /** True while the DRR pointer is parked on this tenant (charged, not yet exhausted). */
  visiting: boolean;
  inFlight: number;
  dispatched: number;
  completed: number;
  failed: number;
  servedCost: number;
}

export class TenantScheduler {
  private readonly baseQuantum: number;
  private readonly maxConcurrentTotal: number;
  private readonly tenants = new Map<string, Tenant>();
  private inFlightTotal = 0;
  private cursor = 0;
  private pumping = false;
  private idleWaiters: Array<() => void> = [];

  constructor(options: SchedulerOptions = {}) {
    this.baseQuantum = options.baseQuantum ?? 1;
    this.maxConcurrentTotal = options.maxConcurrentTotal ?? Infinity;
    if (this.baseQuantum <= 0) throw new Error("baseQuantum must be positive");
  }

  registerTenant(id: string, options: TenantOptions = {}): void {
    if (this.tenants.has(id)) throw new SchedulerError("unknown-tenant", `tenant ${id} already registered`);
    const weight = options.weight ?? 1;
    if (weight <= 0) throw new Error("weight must be positive");
    this.tenants.set(id, {
      id,
      weight,
      maxConcurrent: options.maxConcurrent ?? Infinity,
      maxQueue: options.maxQueue ?? Infinity,
      queue: [],
      deficit: 0,
      visiting: false,
      inFlight: 0,
      dispatched: 0,
      completed: 0,
      failed: 0,
      servedCost: 0,
    });
  }

  unregisterTenant(id: string): void {
    const tenant = this.tenants.get(id);
    if (!tenant) return;
    if (tenant.queue.length > 0 || tenant.inFlight > 0) {
      throw new SchedulerError("unknown-tenant", `tenant ${id} still has pending or running work`);
    }
    this.tenants.delete(id);
  }

  /** Submit a job; the returned promise settles with the job's own result. */
  submit<T>(tenantId: string, task: Task<T>): Promise<T> {
    const tenant = this.tenants.get(tenantId);
    if (!tenant) {
      return Promise.reject(new SchedulerError("unknown-tenant", `tenant ${tenantId} is not registered`));
    }
    if (tenant.queue.length >= tenant.maxQueue) {
      return Promise.reject(
        new SchedulerError("queue-full", `tenant ${tenantId} backlog limit ${tenant.maxQueue} reached`),
      );
    }
    const cost = task.cost ?? 1;
    if (cost <= 0) return Promise.reject(new Error("task cost must be positive"));

    return new Promise<T>((resolve, reject) => {
      tenant.queue.push({
        cost,
        run: task.run,
        resolve: resolve as (value: unknown) => void,
        reject,
      });
      this.pump();
    });
  }

  /** Snapshot of every tenant's counters (for tests and monitoring). */
  stats(): TenantStats[] {
    return [...this.tenants.values()].map((t) => ({
      id: t.id,
      weight: t.weight,
      maxConcurrent: t.maxConcurrent,
      queued: t.queue.length,
      inFlight: t.inFlight,
      dispatched: t.dispatched,
      completed: t.completed,
      failed: t.failed,
      servedCost: t.servedCost,
      deficit: t.deficit,
    }));
  }

  get totalInFlight(): number {
    return this.inFlightTotal;
  }

  /** True when no job is queued or running anywhere. */
  get idle(): boolean {
    if (this.inFlightTotal > 0) return false;
    for (const t of this.tenants.values()) if (t.queue.length > 0) return false;
    return true;
  }

  /** Resolves once the scheduler is completely idle (no permits held). */
  drain(): Promise<void> {
    if (this.idle) return Promise.resolve();
    return new Promise<void>((resolve) => this.idleWaiters.push(resolve));
  }

  private checkIdle(): void {
    if (!this.idle || this.idleWaiters.length === 0) return;
    const waiters = this.idleWaiters;
    this.idleWaiters = [];
    for (const resolve of waiters) resolve();
  }

  private dispatch(tenant: Tenant, job: Job): void {
    tenant.queue.shift();
    tenant.deficit -= job.cost;
    tenant.inFlight += 1;
    tenant.dispatched += 1;
    tenant.servedCost += job.cost;
    this.inFlightTotal += 1;

    Promise.resolve()
      .then(() => job.run())
      .then(
        (value) => {
          tenant.completed += 1;
          job.resolve(value);
        },
        (err) => {
          tenant.failed += 1;
          job.reject(err);
        },
      )
      .finally(() => {
        tenant.inFlight -= 1;
        this.inFlightTotal -= 1;
        this.pump();
        this.checkIdle();
      });
  }

  /**
   * DRR pump. The cursor stays parked on a tenant for the whole of its visit
   * (charge once, dispatch until the head job no longer fits the deficit),
   * then advances. A tenant blocked only by its own concurrency quota keeps
   * its unspent deficit and visit state instead of banking extra credit.
   */
  private pump(): void {
    if (this.pumping) return;
    this.pumping = true;
    try {
      const order = [...this.tenants.values()];
      const n = order.length;
      if (n === 0) return;
      this.cursor %= n;

      // Outer loop: a tenant whose head job costs more than one quantum
      // needs several DRR rounds to accumulate enough deficit. Keep cycling
      // while any tenant was charged or dispatched; cap-blocked tenants are
      // skipped without a charge, so this always terminates.
      let active = true;
      while (active) {
        active = false;
        let scanned = 0; // consecutive tenants visited without a dispatch
        while (scanned < n) {
          if (this.inFlightTotal >= this.maxConcurrentTotal) break;
          const tenant = order[this.cursor];

          if (tenant.queue.length === 0) {
            tenant.deficit = 0;
            tenant.visiting = false;
            this.cursor = (this.cursor + 1) % n;
            scanned += 1;
            continue;
          }
          if (tenant.inFlight >= tenant.maxConcurrent) {
            // Paused by its own quota: skip without charging or consuming the visit.
            this.cursor = (this.cursor + 1) % n;
            scanned += 1;
            continue;
          }
          if (!tenant.visiting) {
            tenant.deficit += tenant.weight * this.baseQuantum;
            tenant.visiting = true;
            active = true;
          }
          if (tenant.queue[0].cost > tenant.deficit) {
            // Cannot afford the head job: keep the deficit, end the visit.
            tenant.visiting = false;
            this.cursor = (this.cursor + 1) % n;
            scanned += 1;
            continue;
          }

          this.dispatch(tenant, tenant.queue[0]);
          scanned = 0;
          active = true;
          if (tenant.queue.length === 0) {
            tenant.deficit = 0;
            tenant.visiting = false;
            this.cursor = (this.cursor + 1) % n;
          } else if (tenant.queue[0].cost > tenant.deficit) {
            tenant.visiting = false;
            this.cursor = (this.cursor + 1) % n;
          }
          // Otherwise the cursor stays parked: the visit continues.
        }
      }
    } finally {
      this.pumping = false;
    }
  }
}
