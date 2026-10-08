import type { Operation, Receipt, Transport } from "../src/types.ts";

export type Behavior =
  | { type: "success"; idMap?: Record<string, string> }
  | { type: "failure"; error?: string }
  // server commits the write but the client never sees the receipt
  | { type: "drop" };

/**
 * Scriptable, idempotent server. Every operation id is committed at most
 * once; re-sending a committed opId returns the original receipt again
 * (which is how duplicate receipts reach the client).
 */
export class FakeServer implements Transport {
  readonly received: Operation[] = [];
  private readonly behaviors = new Map<string, Behavior[]>();
  private readonly committed = new Map<string, Receipt>();

  plan(opId: string, behavior: Behavior): void {
    const queue = this.behaviors.get(opId) ?? [];
    queue.push(behavior);
    this.behaviors.set(opId, queue);
  }

  commitCount(opId: string): number {
    return this.committed.has(opId) ? 1 : 0;
  }

  send(op: Operation): Promise<Receipt> {
    this.received.push(structuredClone(op));
    const existing = this.committed.get(op.id);
    if (existing) return Promise.resolve(existing);

    const queue = this.behaviors.get(op.id) ?? [];
    const behavior: Behavior =
      queue.length > 1 ? (queue.shift() as Behavior) : (queue[0] ?? { type: "success" });

    if (behavior.type === "failure") {
      const receipt: Receipt = { opId: op.id, ok: false, error: behavior.error ?? "rejected" };
      this.committed.set(op.id, receipt);
      return Promise.resolve(receipt);
    }
    const receipt: Receipt = { opId: op.id, ok: true, idMap: behavior.idMap };
    this.committed.set(op.id, receipt);
    if (behavior.type === "drop") {
      return Promise.reject(new Error("connection lost before receipt"));
    }
    return Promise.resolve(receipt);
  }
}
