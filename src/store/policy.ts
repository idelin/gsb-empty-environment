import { joinPath } from "../canonical.ts";
import type { FieldPath } from "../types.ts";

/**
 * Mutable permission policy. The service must re-read this on every
 * submission and between sub-operations: an asynchronous validation that
 * started before a revocation is not evidence that the field is still
 * authorized when the commit write happens.
 */
export interface PolicyState {
  readonly version: number;
  /** Canonical paths revoked for the subject (or globally). */
  readonly revokedPaths: ReadonlySet<string>;
  /**
   * Optional action-level cap overrides keyed by actionId. A smaller value
   * shrinks the signed grant cap; larger values never widen it.
   */
  readonly actionCapOverrides: Readonly<Record<string, number>>;
}

export interface PolicyStore {
  get(): Promise<PolicyState>;
}

/** Mutable in-memory policy useful for tests and as a reference adapter. */
export class InMemoryPolicyStore implements PolicyStore {
  #state: PolicyState;

  constructor(initial?: Partial<Omit<PolicyState, "version">>) {
    this.#state = {
      version: 1,
      revokedPaths: new Set(initial?.revokedPaths ?? []),
      actionCapOverrides: { ...(initial?.actionCapOverrides ?? {}) },
    };
  }

  async get(): Promise<PolicyState> {
    return this.#state;
  }

  current(): PolicyState {
    return this.#state;
  }

  /** Revoke one canonical path; bumps the version so snapshots show the change. */
  revokePath(path: FieldPath): void {
    const key = joinPath(path);
    const revokedPaths = new Set(this.#state.revokedPaths);
    revokedPaths.add(key);
    this.#state = { ...this.#state, version: this.#state.version + 1, revokedPaths };
  }

  /** Shrink (or explicitly set) an action-level cap. */
  setActionCap(actionId: string, cap: number): void {
    if (!Number.isInteger(cap) || cap < 0) {
      throw new Error("action cap must be a non-negative integer");
    }
    const actionCapOverrides = { ...this.#state.actionCapOverrides, [actionId]: cap };
    this.#state = { ...this.#state, version: this.#state.version + 1, actionCapOverrides };
  }
}
