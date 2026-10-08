import type { Journal } from "./journal.ts";
import { collectTempRefs, rewriteTempIds } from "./id-rewrite.ts";
import type {
  FlushSummary,
  OpRecord,
  Operation,
  PersistedState,
  Receipt,
  Transport,
} from "./types.ts";

export interface EngineOptions {
  /** string prefix marking a temporary (client-side) object id. */
  tempPrefix?: string;
}

export class ReplayEngine {
  private readonly journal: Journal;
  private readonly transport: Transport;
  private readonly tempPrefix: string;
  private readonly records = new Map<string, OpRecord>();
  private readonly order: string[] = [];
  private readonly idMap = new Map<string, string>();

  private constructor(journal: Journal, transport: Transport, options: EngineOptions) {
    this.journal = journal;
    this.transport = transport;
    this.tempPrefix = options.tempPrefix ?? "temp:";
  }

  static async open(
    journal: Journal,
    transport: Transport,
    options: EngineOptions = {},
  ): Promise<ReplayEngine> {
    const engine = new ReplayEngine(journal, transport, options);
    const state = await journal.load();
    if (state) {
      for (const rec of state.ops) {
        engine.records.set(rec.op.id, { ...rec, op: structuredClone(rec.op) });
        engine.order.push(rec.op.id);
      }
      for (const [tempId, serverId] of Object.entries(state.idMap)) {
        engine.idMap.set(tempId, serverId);
      }
    }
    return engine;
  }

  async enqueue(op: Operation): Promise<void> {
    if (this.records.has(op.id)) throw new Error(`duplicate operation id: ${op.id}`);
    this.records.set(op.id, { op: structuredClone(op), status: "pending" });
    this.order.push(op.id);
    await this.persist();
  }

  statusOf(opId: string): OpRecord["status"] | undefined {
    return this.records.get(opId)?.status;
  }

  errorOf(opId: string): string | undefined {
    return this.records.get(opId)?.error;
  }

  resolvedId(tempId: string): string | undefined {
    return this.idMap.get(tempId);
  }

  pendingPayload(opId: string): unknown {
    return this.records.get(opId)?.op.payload;
  }

  /**
   * Executes ready operations in dependency-topological order until none are
   * ready. A failed operation marks itself failed and transitively blocks its
   * dependents; independent operations keep going. Operations left in "sent"
   * by a previous crashed flush are retransmitted (receipts are idempotent).
   */
  async flush(): Promise<FlushSummary> {
    for (;;) {
      const ready = this.readyOps();
      if (ready.length === 0) break;
      for (const rec of ready) {
        await this.setStatus(rec, "sent");
        const outgoing: Operation = {
          ...rec.op,
          payload: rewriteTempIds(rec.op.payload, this.idMap),
        };
        const receipt = await this.transport.send(outgoing);
        await this.applyReceipt(receipt);
      }
    }
    return this.summary();
  }

  /**
   * Applies a server receipt. Safe to call with duplicates: a receipt for an
   * already-resolved operation is a no-op.
   */
  async applyReceipt(receipt: Receipt): Promise<void> {
    const rec = this.records.get(receipt.opId);
    if (!rec) return;
    if (rec.status === "acked" || rec.status === "failed") return;
    if (receipt.ok) {
      rec.status = "acked";
      rec.error = undefined;
      if (receipt.idMap) {
        for (const [tempId, serverId] of Object.entries(receipt.idMap)) {
          this.idMap.set(tempId, serverId);
        }
        this.rewriteUnsent();
      }
    } else {
      rec.status = "failed";
      rec.error = receipt.error;
      this.cascadeBlocked(rec.op.id);
    }
    await this.persist();
  }

  private readyOps(): OpRecord[] {
    const providers = this.tempProviders();
    const ready: OpRecord[] = [];
    for (const id of this.order) {
      const rec = this.records.get(id);
      if (!rec || (rec.status !== "pending" && rec.status !== "sent")) continue;
      if (this.depsSatisfied(rec.op, providers)) ready.push(rec);
    }
    return ready;
  }

  private depsSatisfied(op: Operation, providers: Map<string, string>): boolean {
    for (const dep of op.dependsOn ?? []) {
      if (this.records.get(dep)?.status !== "acked") return false;
    }
    for (const ref of collectTempRefs(op.payload, this.tempPrefix)) {
      const providerId = providers.get(ref);
      if (!providerId) return false; // unknown temp id: never send it upstream
      if (providerId === op.id) continue;
      if (this.records.get(providerId)?.status !== "acked") return false;
    }
    return true;
  }

  private tempProviders(): Map<string, string> {
    const providers = new Map<string, string>();
    for (const id of this.order) {
      const rec = this.records.get(id);
      if (!rec) continue;
      for (const tempId of rec.op.provides ?? []) {
        if (!providers.has(tempId)) providers.set(tempId, id);
      }
    }
    return providers;
  }

  private rewriteUnsent(): void {
    for (const rec of this.records.values()) {
      if (rec.status !== "pending") continue;
      rec.op = { ...rec.op, payload: rewriteTempIds(rec.op.payload, this.idMap) };
    }
  }

  private cascadeBlocked(failedId: string): void {
    const providers = this.tempProviders();
    const dependents = new Map<string, Set<string>>();
    for (const rec of this.records.values()) {
      if (rec.status !== "pending") continue;
      const deps = new Set(rec.op.dependsOn ?? []);
      for (const ref of collectTempRefs(rec.op.payload, this.tempPrefix)) {
        const providerId = providers.get(ref);
        if (providerId && providerId !== rec.op.id) deps.add(providerId);
      }
      for (const dep of deps) {
        let set = dependents.get(dep);
        if (!set) {
          set = new Set();
          dependents.set(dep, set);
        }
        set.add(rec.op.id);
      }
    }
    const queue = [failedId];
    while (queue.length > 0) {
      const current = queue.shift();
      if (current === undefined) break;
      for (const dependentId of dependents.get(current) ?? []) {
        const rec = this.records.get(dependentId);
        if (rec && rec.status === "pending") {
          rec.status = "blocked";
          rec.error = `dependency ${current} failed`;
          queue.push(dependentId);
        }
      }
    }
  }

  private async setStatus(rec: OpRecord, status: OpRecord["status"]): Promise<void> {
    rec.status = status;
    await this.persist();
  }

  private summary(): FlushSummary {
    const result: FlushSummary = { acked: [], failed: [], blocked: [], pending: [] };
    for (const id of this.order) {
      const rec = this.records.get(id);
      if (!rec) continue;
      if (rec.status === "acked") result.acked.push(id);
      else if (rec.status === "failed") result.failed.push(id);
      else if (rec.status === "blocked") result.blocked.push(id);
      else result.pending.push(id);
    }
    return result;
  }

  private persist(): Promise<void> {
    const ops: OpRecord[] = [];
    for (const id of this.order) {
      const rec = this.records.get(id);
      if (rec) ops.push({ ...rec, op: structuredClone(rec.op) });
    }
    const state: PersistedState = {
      version: 1,
      ops,
      idMap: Object.fromEntries(this.idMap),
    };
    return this.journal.save(state);
  }
}
