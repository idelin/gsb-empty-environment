/**
 * Business effect gateway.
 *
 * The service never assumes a business write succeeded because an
 * authorization slot was occupied. Effects are addressed by a durable
 * idempotency token (`effectId`) and the gateway itself applies each effect
 * with compare-and-swap semantics.
 */

/** A fault indistinguishable from a timeout: the effect outcome is unknown. */
export class AmbiguousEffectFault extends Error {
  readonly effectId: string;

  constructor(effectId: string) {
    super(`ambiguous outcome for effect ${effectId}`);
    this.name = "AmbiguousEffectFault";
    this.effectId = effectId;
  }
}

/** A definite, permanent business rejection (no effect applied). */
export class PermanentEffectError extends Error {
  readonly effectId: string;
  override readonly code: string;

  constructor(effectId: string, code: string) {
    super(`permanent failure for effect ${effectId}: ${code}`);
    this.name = "PermanentEffectError";
    this.effectId = effectId;
    this.code = code;
  }
}

export interface BusinessEffectRequest {
  readonly effectId: string;
  readonly grantId: string;
  readonly actionId: string;
  readonly subId: string;
  readonly payload: unknown;
}

export interface BusinessEffectResult {
  readonly effectId: string;
  readonly applied: boolean;
  readonly detail: unknown;
}

export interface BusinessGateway {
  /**
   * Apply (or return the existing result for) an idempotent effect.
   * Implementations must key on `effectId` and use a create-only CAS.
   */
  applyEffect(request: BusinessEffectRequest): Promise<BusinessEffectResult>;
  /** Read back an effect; absence means the effect definitely did not apply. */
  findEffect(effectId: string): Promise<BusinessEffectResult | undefined>;
  /** Count effects actually applied for one (grant, action, sub-operation). */
  countEffects(grantId: string, actionId: string, subId: string): Promise<number>;
}

export interface BusinessFaultRule {
  readonly effectIdIncludes?: string;
  readonly occurrence?: number;
  readonly mode: "permanent" | "ambiguous";
}

/** Reference gateway storing effects in a plain map guarded by create-only CAS. */
export class InMemoryBusinessGateway implements BusinessGateway {
  readonly #effects = new Map<string, BusinessEffectResult>();
  readonly #seen = new Map<string, number>();
  readonly #fired = new Set<string>();
  readonly #permanent = new Set<string>();
  readonly #rules: readonly BusinessFaultRule[];

  constructor(rules: readonly BusinessFaultRule[] = []) {
    this.#rules = rules;
  }

  async applyEffect(
    request: BusinessEffectRequest,
  ): Promise<BusinessEffectResult> {
    const existing = this.#effects.get(request.effectId);
    if (existing) {
      return existing;
    }
    if (this.#permanent.has(request.effectId)) {
      throw new PermanentEffectError(request.effectId, "INJECTED_PERMANENT");
    }
    // A fault fires at most once per effectId. A retry after an ambiguous
    // timeout reaches the backend and goes through normal idempotent apply.
    if (this.#fired.has(request.effectId)) {
      const recovered: BusinessEffectResult = {
        effectId: request.effectId,
        applied: true,
        detail: { payload: request.payload, recovered: true },
      };
      this.#effects.set(request.effectId, recovered);
      return recovered;
    }
    const count = (this.#seen.get(request.effectId) ?? 0) + 1;
    this.#seen.set(request.effectId, count);
    const rule = this.#rules.find(
      (candidate) =>
        (candidate.effectIdIncludes === undefined ||
          request.effectId.includes(candidate.effectIdIncludes)) &&
        (candidate.occurrence === undefined || candidate.occurrence === count),
    );
    if (rule) {
      this.#fired.add(request.effectId);
      if (rule.mode === "permanent") {
        this.#permanent.add(request.effectId);
        throw new PermanentEffectError(request.effectId, "INJECTED_PERMANENT");
      }
      if (Math.random() < 0.5) {
        this.#effects.set(request.effectId, {
          effectId: request.effectId,
          applied: true,
          detail: { payload: request.payload, injected: "ambiguous-maybe-applied" },
        });
      }
      throw new AmbiguousEffectFault(request.effectId);
    }
    const result: BusinessEffectResult = {
      effectId: request.effectId,
      applied: true,
      detail: { payload: request.payload },
    };
    this.#effects.set(request.effectId, result);
    return result;
  }

  async findEffect(effectId: string): Promise<BusinessEffectResult | undefined> {
    return this.#effects.get(effectId);
  }

  async countEffects(grantId: string, actionId: string, subId: string): Promise<number> {
    const prefix = `${grantId}:${actionId}:${subId}:`;
    let count = 0;
    for (const key of this.#effects.keys()) {
      if (key.startsWith(prefix)) {
        count += 1;
      }
    }
    return count;
  }

  /** Test helper: count durable effects (each is one real business write). */
  effectCount(): number {
    return this.#effects.size;
  }
}
