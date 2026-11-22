import type { AnswerId, IdentityId, InvitationDigest, SessionId } from "./types.ts";
import { sha256Hex } from "./canonical.ts";
import { withCasRetry } from "./store.ts";
import type { CasStore } from "./store.ts";

/**
 * Queryable, idempotent delivery receipts held by the delivery side.
 * A receipt identifies the exact routing decision so that a relay crash
 * cannot turn an unconfirmed send into a delivery to a different identity.
 */
export interface DeliveryReceipt {
  readonly receiptId: string;
  readonly answerId: AnswerId;
  readonly sessionId: SessionId;
  readonly invitationDigest: InvitationDigest;
  readonly recipient: IdentityId;
  readonly generation: number;
  readonly node: string;
  readonly state: "received" | "consumed";
  readonly createdAt: number;
}

export interface ReceiptDirectory {
  /**
   * Atomically claim an answer for one routing target. Repeated claims for
   * the same answer return the existing receipt; a conflicting target is
   * rejected instead of silently re-routing.
   */
  claim(receipt: Omit<DeliveryReceipt, "receiptId" | "state" | "createdAt">): Promise<DeliveryReceipt>;
  getByAnswer(answerId: AnswerId): Promise<DeliveryReceipt | null>;
  markConsumed(answerId: AnswerId): Promise<void>;
}

export class CasReceiptDirectory implements ReceiptDirectory {
  readonly #store: CasStore;

  constructor(store: CasStore) {
    this.#store = store;
  }

  async claim(
    requested: Omit<DeliveryReceipt, "receiptId" | "state" | "createdAt">,
  ): Promise<DeliveryReceipt> {
    const key = receiptKey(requested.answerId);
    return withCasRetry<DeliveryReceipt | null>(this.#store, key, (current) => {
      if (current) {
        if (
          current.recipient !== requested.recipient ||
          current.invitationDigest !== requested.invitationDigest ||
          current.sessionId !== requested.sessionId
        ) {
          throw new Error(
            `receipt conflict for answer ${requested.answerId}: already claimed by ${current.recipient}`,
          );
        }
        return current;
      }
      const now = Date.now();
      return {
        ...requested,
        receiptId: receiptId(requested),
        state: "received",
        createdAt: now,
      };
    }).then((record) => {
      if (!record.value) {
        throw new Error(`receipt ${requested.answerId} disappeared during claim`);
      }
      return record.value;
    });
  }

  async getByAnswer(answerId: AnswerId): Promise<DeliveryReceipt | null> {
    const record = await this.#store.get<DeliveryReceipt>(receiptKey(answerId));
    return record ? record.value : null;
  }

  async markConsumed(answerId: AnswerId): Promise<void> {
    await withCasRetry<DeliveryReceipt | null>(this.#store, receiptKey(answerId), (current) => {
      if (!current) {
        throw new Error(`unknown receipt for answer ${answerId}`);
      }
      return { ...current, state: "consumed" };
    });
  }
}

function receiptKey(answerId: AnswerId): string {
  return `receipt:${answerId}`;
}

function receiptId(receipt: { answerId: AnswerId; recipient: IdentityId; node: string }): string {
  return `rcpt:${receipt.answerId}:${sha256Hex(`${receipt.recipient}|${receipt.node}`).slice(0, 16)}`;
}
