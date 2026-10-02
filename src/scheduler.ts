/**
 * Multi-tenant resource isolation scheduler with Deficit Round Robin (DRR)
 * weighted fair queueing.
 *
 * Isolation guarantees:
 *  - Per-tenant FIFO queues: one tenant's flood never reorders or drops
 *    another tenant's work.
 *  - Per-tenant quota (token bucket per time window): a tenant cannot exceed
 *    its allotted service in any window, regardless of demand.
 *  - Per-tenant max concurrency: caps simultaneously running tasks.
 *
 * Fairness: each scheduling round every backlogged tenant is credited
 * `quantum * weight` deficit; a queued task may dispatch only while the
 * tenant's deficit covers the task cost. Unused deficit carries over, and is
 * reset to zero when a tenant drains its queue (classic DRR).
 */

export interface TenantConfig {
  /** Relative share of service. Must be a positive integer. */
  weight: number;
  /** Max tasks the tenant may start per quota window. */
  quotaPerWindow: number;
  /** Quota window length in milliseconds. */
  windowMs: number;
  /** Max concurrently running tasks for this tenant. */
  maxConcurrency: number;
  /** Max queued (not yet started) tasks; overflow is rejected. */
  maxQueue: number;
}

export interface Task {
  id: string;
  tenantId: string;
  /** Service cost charged against the tenant's DRR deficit. */
  cost: number;
  run: () => Promise<void>;
}

export type TaskOutcome = "completed" | "failed" | "rejected";

export interface TaskResult {
  taskId: string;
  tenantId: string;
  outcome: TaskOutcome;
  error?: unknown;
}

interface TenantState {
  config: TenantConfig;
  queue: Task[];
  deficit: number;
  inFlight: number;
  windowStart: number;
  usedInWindow: number;
  stats: { submitted: number; started: number; completed: number; failed: number; rejected: number };
}

export class QuotaExceededError extends Error {}
export class UnknownTenantError extends Error {}
export class QueueFullError extends Error {}

const DEFAULT_QUANTUM = 100;

export class TenantScheduler {
  private tenants = new Map<string, TenantState>();
  private results: TaskResult[] = [];
  private waiters: Array<() => void> = [];
  private roundRobinOrder: string[] = [];
  private rrCursor = 0;
  private readonly quantum: number;
  private readonly now: () => number;

  constructor(quantum: number = DEFAULT_QUANTUM, now: () => number = () => Date.now()) {
    this.quantum = quantum;
    this.now = now;
  }

  registerTenant(tenantId: string, config: TenantConfig): void {
    if (this.tenants.has(tenantId)) throw new Error(`tenant already registered: ${tenantId}`);
    if (!Number.isInteger(config.weight) || config.weight <= 0) {
      throw new Error(`weight must be a positive integer, got ${config.weight}`);
    }
    for (const key of ["quotaPerWindow", "maxConcurrency", "maxQueue"] as const) {
      if (!Number.isInteger(config[key]) || config[key] <= 0) {
        throw new Error(`${key} must be a positive integer, got ${config[key]}`);
      }
    }
    if (!Number.isFinite(config.windowMs) || config.windowMs <= 0) {
      throw new Error(`windowMs must be positive, got ${config.windowMs}`);
    }
    this.tenants.set(tenantId, {
      config,
      queue: [],
      deficit: 0,
      inFlight: 0,
      windowStart: this.now(),
      usedInWindow: 0,
      stats: { submitted: 0, started: 0, completed: 0, failed: 0, rejected: 0 },
    });
    this.roundRobinOrder.push(tenantId);
  }

  /**
   * Enqueue a task. Synchronously rejects (throws) when the tenant is
   * unknown or its queue is full, so overload is contained at the edge.
   */
  submit(task: Task): void {
    const tenant = this.tenants.get(task.tenantId);
    if (!tenant) {
      throw new UnknownTenantError(`unknown tenant: ${task.tenantId}`);
    }
    if (!Number.isInteger(task.cost) || task.cost <= 0) {
      throw new Error(`task cost must be a positive integer, got ${task.cost}`);
    }
    tenant.stats.submitted++;
    if (tenant.queue.length >= tenant.config.maxQueue) {
      tenant.stats.rejected++;
      this.results.push({ taskId: task.id, tenantId: task.tenantId, outcome: "rejected" });
      throw new QueueFullError(`queue full for tenant: ${task.tenantId}`);
    }
    tenant.queue.push(task);
  }

  /** Roll the quota window forward for a tenant if it has expired. */
  private refreshWindow(tenant: TenantState): void {
    const now = this.now();
    if (now - tenant.windowStart >= tenant.config.windowMs) {
      tenant.windowStart = now;
      tenant.usedInWindow = 0;
    }
  }

  private canDispatch(tenant: TenantState, task: Task): boolean {
    if (tenant.inFlight >= tenant.config.maxConcurrency) return false;
    if (tenant.usedInWindow >= tenant.config.quotaPerWindow) return false;
    if (tenant.deficit < task.cost) return false;
    return true;
  }

  /**
   * Run one DRR round over all tenants. Returns the number of tasks
   * dispatched this round. Exposed synchronously so tests are deterministic.
   */
  runRound(): number {
    for (const tenant of this.tenants.values()) this.refreshWindow(tenant);

    let dispatched = 0;
    const order = this.roundRobinOrder;
    if (order.length === 0) return 0;

    for (let i = 0; i < order.length; i++) {
      const tenantId = order[(this.rrCursor + i) % order.length];
      const tenant = this.tenants.get(tenantId)!;
      if (tenant.queue.length === 0) {
        tenant.deficit = 0; // no carry-over for idle tenants
        continue;
      }
      // A tenant throttled by its own quota/concurrency limit is not
      // service-starved, so it must not bank DRR credit while blocked.
      if (tenant.usedInWindow >= tenant.config.quotaPerWindow) continue;
      if (tenant.inFlight >= tenant.config.maxConcurrency) continue;
      tenant.deficit += this.quantum * tenant.config.weight;
      while (tenant.queue.length > 0) {
        const head = tenant.queue[0];
        if (!this.canDispatch(tenant, head)) break;
        tenant.queue.shift();
        tenant.deficit -= head.cost;
        tenant.inFlight++;
        tenant.usedInWindow++;
        tenant.stats.started++;
        dispatched++;
        this.dispatch(tenantId, head);
      }
    }
    // Advance cursor so the next round starts with a different tenant.
    this.rrCursor = (this.rrCursor + 1) % order.length;
    return dispatched;
  }

  private dispatch(tenantId: string, task: Task): void {
    const tenant = this.tenants.get(tenantId)!;
    void task
      .run()
      .then(
        () => {
          tenant.stats.completed++;
          this.results.push({ taskId: task.id, tenantId, outcome: "completed" });
        },
        (error: unknown) => {
          tenant.stats.failed++;
          this.results.push({ taskId: task.id, tenantId, outcome: "failed", error });
        },
      )
      .finally(() => {
        tenant.inFlight--;
        this.notify();
      });
  }

  private notify(): void {
    const waiters = this.waiters;
    this.waiters = [];
    for (const wake of waiters) wake();
  }

  /** True while any tenant has queued or running work. */
  hasPendingWork(): boolean {
    for (const tenant of this.tenants.values()) {
      if (tenant.queue.length > 0 || tenant.inFlight > 0) return true;
    }
    return false;
  }

  /**
   * Drain all queues: repeatedly run DRR rounds until every queued task has
   * finished. Resolves when the system is completely idle (no leaks).
   */
  async drain(): Promise<void> {
    while (this.hasPendingWork()) {
      const dispatched = this.runRound();
      if (dispatched === 0) {
        // Blocked on quota windows or running tasks: wait for a completion
        // (or a microtask tick) before re-evaluating.
        await new Promise<void>((resolve) => {
          this.waiters.push(resolve);
          setTimeout(resolve, 1);
        });
      } else {
        await new Promise<void>((resolve) => setImmediate(resolve));
      }
    }
  }

  tenantStats(tenantId: string): TenantState["stats"] {
    const tenant = this.tenants.get(tenantId);
    if (!tenant) throw new UnknownTenantError(`unknown tenant: ${tenantId}`);
    return { ...tenant.stats };
  }

  inFlightOf(tenantId: string): number {
    const tenant = this.tenants.get(tenantId);
    if (!tenant) throw new UnknownTenantError(`unknown tenant: ${tenantId}`);
    return tenant.inFlight;
  }

  queuedOf(tenantId: string): number {
    const tenant = this.tenants.get(tenantId);
    if (!tenant) throw new UnknownTenantError(`unknown tenant: ${tenantId}`);
    return tenant.queue.length;
  }

  usedQuotaOf(tenantId: string): number {
    const tenant = this.tenants.get(tenantId);
    if (!tenant) throw new UnknownTenantError(`unknown tenant: ${tenantId}`);
    this.refreshWindow(tenant);
    return tenant.usedInWindow;
  }

  taskResults(): TaskResult[] {
    return [...this.results];
  }
}
