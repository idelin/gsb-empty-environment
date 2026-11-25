import { AmbiguousEffectFault, PermanentEffectError, type BusinessGateway } from "./business.ts";
import { canonicalizeForm, joinPath } from "./canonical.ts";
import {
  claimKey,
  claimTicketKey,
  decisionKey,
  decisionPrefix,
  snapshotKey,
  subProofPrefix,
  subCounterKey,
  subKey,
  topoSort,
} from "./keys.ts";
import { verifyGrant, type SigningKey } from "./sign.ts";
import { CasConflict, SimulatedCrash, type CasStore } from "./store/cas.ts";
import type { PolicyStore } from "./store/policy.ts";
import { SubmitCode, SubmitStatus } from "./types.ts";
import type {
  ClaimRecord,
  ConsumptionReport,
  DecisionRecord,
  SnapshotRecord,
  SubOperationRecord,
  SubmitRequest,
  SubmitResult,
} from "./types.ts";

/** Counter entry persisted per (grant, action, subId). */
interface SubCounter {
  readonly kind: "sub-counter";
  readonly value: number;
  /** idemKey that reserved each ordinal (index 0 unused). */
  readonly reservations: readonly string[];
}

export interface AuthorizationServiceOptions {
  readonly store: CasStore;
  readonly policy: PolicyStore;
  readonly gateway: BusinessGateway;
  readonly signingKeys: readonly SigningKey[];
  /** Alias table used to canonicalize submitted form fields. */
  readonly aliases?: Readonly<Record<string, readonly string[]>>;
  /** Injected clock; defaults to Date.now. */
  readonly now?: () => number;
}

function failure(
  status: SubmitStatus,
  code: SubmitCode,
  message: string,
  proofKeys: readonly string[] = [],
): SubmitResult {
  return { status, code, message, proofKeys };
}

/**
 * Durable two-phase authorization service.
 *
 * Phase A (preparation): verify grant against CURRENT policy, persist an
 * independently readable snapshot proof, then atomically insert a claim that
 * grants the single writer slot for (grant, action).
 *
 * Phase B (decision): execute dependency-ordered sub-operations, each with
 * its own counter CAS, idempotent business effect and consumption proof,
 * then write a final decision exactly once. A claim alone never proves that
 * a business sub-operation completed.
 */
export class AuthorizationService {
  readonly #store: CasStore;
  readonly #policy: PolicyStore;
  readonly #gateway: BusinessGateway;
  readonly #keys: readonly SigningKey[];
  readonly #aliases: Readonly<Record<string, readonly string[]>>;
  readonly #now: () => number;

  constructor(options: AuthorizationServiceOptions) {
    this.#store = options.store;
    this.#policy = options.policy;
    this.#gateway = options.gateway;
    this.#keys = options.signingKeys;
    this.#aliases = options.aliases ?? {};
    this.#now = options.now ?? (() => Date.now());
  }

  /** Confirm one action. Safe to call repeatedly with the same idemKey. */
  async submit(request: SubmitRequest): Promise<SubmitResult> {
    const now = this.#now();
    const grant = request.grant;

    const verification = verifyGrant(grant, this.#keys, now);
    if (!verification.ok) {
      return verification.reason === "grant expired"
        ? failure(SubmitStatus.Failed, SubmitCode.GrantExpired, verification.reason)
        : failure(SubmitStatus.Failed, SubmitCode.BadSignature, verification.reason);
    }
    if (request.subject !== grant.subject) {
      return failure(SubmitStatus.Failed, SubmitCode.SubjectMismatch, "subject does not match grant");
    }
    if (request.windowSessionId !== grant.windowSessionId) {
      return failure(SubmitStatus.Failed, SubmitCode.WindowMismatch, "window session does not match grant");
    }
    if (request.objectVersion !== grant.objectVersion) {
      return failure(SubmitStatus.Failed, SubmitCode.VersionMismatch, "object version does not match grant");
    }

    const action = grant.actions.find((candidate) => candidate.actionId === request.actionId);
    if (!action) {
      return failure(SubmitStatus.Failed, SubmitCode.UnknownAction, `unknown action ${request.actionId}`);
    }

    let canonical: ReturnType<typeof canonicalizeForm>;
    try {
      canonical = canonicalizeForm(request.form, this.#aliases);
    } catch (error) {
      return failure(SubmitStatus.Failed, SubmitCode.UnknownPath, (error as Error).message);
    }
    if (canonical.digest !== grant.formDigest) {
      return failure(
        SubmitStatus.Failed,
        SubmitCode.FormDigestMismatch,
        "canonicalized form does not match the signed grant digest",
      );
    }

    let orderedSpecs: ReturnType<typeof topoSort>;
    try {
      orderedSpecs = topoSort(action.subOperations);
    } catch (error) {
      return failure(
        SubmitStatus.Failed,
        (error as Error).message.includes("cycle")
          ? SubmitCode.DependencyCycle
          : SubmitCode.UnknownSubOperation,
        (error as Error).message,
      );
    }
    const requestedIds = request.subOperations.map((sub) => sub.subId);
    const requestedSet = new Set(requestedIds);
    if (requestedSet.size !== requestedIds.length) {
      return failure(
        SubmitStatus.Failed,
        SubmitCode.DuplicateSubOperation,
        "sub-operation requests must be unique within the parent action",
      );
    }
    const requiredIds = orderedSpecs.map((spec) => spec.subId);
    const allKnown = requestedIds.every((id) => requiredIds.includes(id));
    if (!allKnown || requestedIds.length !== requiredIds.length) {
      return failure(
        SubmitStatus.Failed,
        SubmitCode.UnknownSubOperation,
        "sub-operation requests must match the grant exactly",
      );
    }
    const payloadBySubId = new Map(request.subOperations.map((sub) => [sub.subId, sub.payload]));

    // Policy is re-read at decision time. A pre-revocation async validation
    // cannot be reused as evidence that fields remain authorized.
    const policy = await this.#policy.get();
    for (const binding of canonical.bindings) {
      if (policy.revokedPaths.has(joinPath(binding.path))) {
        return failure(
          SubmitStatus.Failed,
          SubmitCode.FieldRevoked,
          `field revoked by policy v${policy.version}: ${joinPath(binding.path)}`,
        );
      }
    }
    const override = policy.actionCapOverrides[action.actionId];
    const effectiveActionCap =
      override === undefined ? action.maxActionConsumptions : Math.min(action.maxActionConsumptions, override);
    if (effectiveActionCap <= 0) {
      return failure(
        SubmitStatus.Failed,
        SubmitCode.ActionLimitExhausted,
        `action cap shrunk to ${effectiveActionCap} by policy v${policy.version}`,
      );
    }

    const snapKey = snapshotKey(grant.grantId, action.actionId, request.idemKey);
    const snapshot: SnapshotRecord = {
      kind: "snapshot",
      grantId: grant.grantId,
      actionId: action.actionId,
      idemKey: request.idemKey,
      subject: grant.subject,
      windowSessionId: grant.windowSessionId,
      objectVersion: grant.objectVersion,
      formDigest: canonical.digest,
      bindings: canonical.bindings,
      policyVersion: policy.version,
      effectiveActionCap,
      createdAt: now,
    };
    const existingSnapshot = await this.#store.read<SnapshotRecord>(snapKey);
    if (existingSnapshot.version === 0) {
      try {
        await this.#store.write(snapKey, snapshot, 0);
      } catch (error) {
        if (error instanceof SimulatedCrash) {
          return failure(
            SubmitStatus.Retryable,
            SubmitCode.TransientFault,
            error.message,
            [],
          );
        }
        if (!(error instanceof CasConflict)) {
          throw error;
        }
      }
    }

    let claim: ClaimRecord;
    try {
      claim = await this.#acquireClaim(
        grant.grantId,
        action.actionId,
        request.idemKey,
        policy.version,
        effectiveActionCap,
      );
    } catch (error) {
      if (error instanceof SimulatedCrash) {
        return failure(
          SubmitStatus.Unknown,
          SubmitCode.ReconcileRequired,
          error.message,
          [snapKey],
        );
      }
      if ((error as Error & { code?: string }).code === SubmitCode.ActionLimitExhausted) {
        return failure(SubmitStatus.Failed, SubmitCode.ActionLimitExhausted, (error as Error).message, [snapKey]);
      }
      throw error;
    }

    if (claim.idemKey !== request.idemKey) {
      return failure(
        SubmitStatus.Conflict,
        SubmitCode.ClaimConflict,
        `single writer slot held by another confirmation (${claim.idemKey})`,
        [snapKey],
      );
    }

    return this.#runPhaseB({
      grantId: grant.grantId,
      actionId: action.actionId,
      idemKey: request.idemKey,
      requiredIds,
      orderedSpecs,
      payloadBySubId,
      snapKey,
    });
  }

  /**
   * Preparation phase.
   *
   * 1. Compete for the single-writer pointer with a create-only CAS, so at
   *    most one window can hold the writer slot for (grant, action).
   * 2. Only the winner enforces the action cap and persists an immutable,
   *    replay-safe claim ticket. A losing window writes nothing.
   */
  async #acquireClaim(
    grantId: string,
    actionId: string,
    idemKey: string,
    policyVersion: number,
    effectiveActionCap: number,
  ): Promise<ClaimRecord> {
    const ticketKey = claimTicketKey(grantId, actionId, idemKey);
    const now = this.#now();
    const fresh: ClaimRecord = {
      kind: "claim",
      grantId,
      actionId,
      idemKey,
      owner: idemKey,
      policyVersion,
      createdAt: now,
      updatedAt: now,
    };
    const pointerKey = claimKey(grantId, actionId);
    const pointer = await this.#store.read<ClaimRecord>(pointerKey);
    if (pointer.version !== 0) {
      // A writer already holds the slot. Replays of the same confirmation
      // continue; other windows get a deterministic conflict.
      return pointer.value!;
    }
    try {
      await this.#store.write(pointerKey, fresh, 0);
    } catch (error) {
      if (error instanceof CasConflict) {
        return (await this.#store.read<ClaimRecord>(pointerKey)).value!;
      }
      throw error;
    }

    // This attempt won the single-writer slot. Replays reuse their ticket.
    const ticketEntry = await this.#store.read<ClaimRecord>(ticketKey);
    if (ticketEntry.version === 0) {
      // Action cap is enforced by counting durable winner tickets. The
      // pointer CAS above guarantees the count cannot be inflated by a race.
      const tickets = await this.#countClaimTickets(grantId, actionId);
      if (tickets + 1 > effectiveActionCap) {
        const held = await this.#store.read<ClaimRecord>(pointerKey);
        if (held.version !== 0 && held.value!.idemKey === idemKey) {
          await this.#store.delete(pointerKey, held.version);
        }
        const error = new Error(
          `action limit exhausted: ${tickets} claim tickets, cap ${effectiveActionCap}`,
        ) as Error & { code: SubmitCode };
        error.code = SubmitCode.ActionLimitExhausted;
        throw error;
      }
      await this.#store.write(ticketKey, fresh, 0);
    }
    return fresh;
  }

  async #countClaimTickets(grantId: string, actionId: string): Promise<number> {
    const prefix = `ledger/claim/${grantId}/${actionId}/`;
    const keys = await this.#store.listKeys();
    return keys.filter((key) => key.startsWith(prefix)).length;
  }

  /** Execute/continue sub-operations and write the terminal decision. */
  async #runPhaseB(context: {
    grantId: string;
    actionId: string;
    idemKey: string;
    requiredIds: readonly string[];
    orderedSpecs: ReturnType<typeof topoSort>;
    payloadBySubId: Map<string, unknown>;
    snapKey: string;
  }): Promise<SubmitResult> {
    const { grantId, actionId, idemKey, requiredIds, orderedSpecs, payloadBySubId, snapKey } =
      context;
    const proofKeys: string[] = [snapKey];
    const executedSubIds: string[] = [];

    // If a terminal decision already exists (e.g. the writer pointer release
    // crashed after the decision write), return it instead of treating the
    // still-present pointer as a conflict.
    const priorDecision = await this.#store.read<DecisionRecord>(
      decisionKey(grantId, actionId, idemKey),
    );
    if (priorDecision.version !== 0) {
      // Best-effort cleanup if the original release crashed after the decision.
      await this.#releaseClaim(grantId, actionId, idemKey);
      return this.#resultFromDecision(priorDecision.value!, proofKeys);
    }

    for (const spec of orderedSpecs) {
      const recordKey = subKey(grantId, actionId, idemKey, spec.subId);
      const existing = await this.#store.read<SubOperationRecord>(recordKey);
      if (existing.version !== 0) {
        proofKeys.push(recordKey);
        executedSubIds.push(spec.subId);
        continue;
      }

      // Re-check policy before each pending effect: a late revocation of a
      // field valid in the snapshot must stop all remaining work.
      const policy = await this.#policy.get();
      const snapshot = (await this.#store.read<SnapshotRecord>(snapKey)).value!;
      const revoked = snapshot.bindings.some((binding) =>
        policy.revokedPaths.has(joinPath(binding.path)),
      );
      if (revoked) {
        return this.#terminalFailure({
          grantId,
          actionId,
          idemKey,
          requiredIds,
          executedSubIds,
          proofKeys,
          snapKey,
          code: SubmitCode.FieldRevoked,
          message: `field revoked mid-run by policy v${policy.version}`,
        });
      }

      const counterKey = subCounterKey(grantId, actionId, spec.subId);
      let counterVersion = 0;
      let counterValue = 0;
      let reservations: string[] = [];
      const counterEntry = await this.#store.read<SubCounter>(counterKey);
      if (counterEntry.version !== 0) {
        counterVersion = counterEntry.version;
        counterValue = counterEntry.value!.value;
        reservations = [...counterEntry.value!.reservations];
      }

      const effectIdFor = (ordinal: number) =>
        `${grantId}:${actionId}:${spec.subId}:${ordinal}`;

      // Ordinal selection. The counter records which idemKey reserved each
      // ordinal, so recovery reuses an ordinal only when THIS confirmation
      // owns it. A different confirmation always needs a fresh ordinal.
      let nextOrdinal = counterValue + 1;
      let reuseExistingEffect = false;
      if (counterValue > 0) {
        const ownerOfHighest = reservations[counterValue];
        const highestEffect = await this.#gateway.findEffect(effectIdFor(counterValue));
        const ownProof = await this.#store.read<SubOperationRecord>(recordKey);
        if (ownProof.version !== 0 && ownerOfHighest === idemKey) {
          nextOrdinal = counterValue;
          reuseExistingEffect = true;
        } else if (ownerOfHighest === idemKey) {
          // This confirmation reserved the highest ordinal but has not proven
          // it yet (crash/timeout). Reuse whether or not the effect applied.
          nextOrdinal = counterValue;
          reuseExistingEffect = highestEffect !== undefined;
        }
      }

      const needsFreshEffect = nextOrdinal !== counterValue || !reuseExistingEffect;
      if (needsFreshEffect && nextOrdinal > spec.maxSubConsumptions) {
        return this.#terminalFailure({
          grantId,
          actionId,
          idemKey,
          requiredIds,
          executedSubIds,
          proofKeys,
          snapKey,
          code: SubmitCode.SubLimitExhausted,
          message: `sub-operation ${spec.subId} cap ${spec.maxSubConsumptions} exhausted`,
        });
      }

      // 1) Reserve the sub-consumption ordinal with its own atomic CAS first.
      //    The reservation never applies a business effect, so an occupied
      //    authorization can never be mistaken for a completed sub-operation.
      if (nextOrdinal > counterValue) {
        const nextReservations = [...reservations];
        nextReservations[nextOrdinal] = idemKey;
        const nextCounter: SubCounter = {
          kind: "sub-counter",
          value: nextOrdinal,
          reservations: nextReservations,
        };
        try {
          await this.#store.write(counterKey, nextCounter, counterVersion);
        } catch (error) {
          if (error instanceof CasConflict) {
            // A concurrent attempt reserved the next ordinal; restart the
            // iteration so we read the fresh counter and effect state.
            continue;
          }
          if (error instanceof SimulatedCrash) {
            return failure(SubmitStatus.Unknown, SubmitCode.ReconcileRequired, error.message, proofKeys);
          }
          throw error;
        }
      }

      // 2) Apply the business effect idempotently under the reserved ordinal.
      const effectId = effectIdFor(nextOrdinal);
      const priorEffect = await this.#gateway.findEffect(effectId);
      let effectResult: unknown;
      if (priorEffect) {
        // A previous attempt reserved the ordinal and the effect did apply,
        // but its answer was lost. Recover without consuming a new ordinal.
        effectResult = priorEffect.detail;
      } else {
        try {
          const applied = await this.#gateway.applyEffect({
            effectId,
            grantId,
            actionId,
            subId: spec.subId,
            payload: payloadBySubId.get(spec.subId),
          });
          effectResult = applied.detail;
        } catch (error) {
          if (error instanceof PermanentEffectError) {
            return this.#terminalFailure({
              grantId,
              actionId,
              idemKey,
              requiredIds,
              executedSubIds,
              proofKeys,
              snapKey,
              code: SubmitCode.Failed,
              message: error.message,
            });
          }
          if (error instanceof AmbiguousEffectFault) {
            return failure(
              SubmitStatus.Unknown,
              SubmitCode.ReconcileRequired,
              `effect ${effectId} outcome unknown; reconcile before retry`,
              proofKeys,
            );
          }
          throw error;
        }
      }

      // The consumption proof is itself an independent, read-back-able CAS.
      const proofEntry = await this.#store.read<SubOperationRecord>(recordKey);
      if (proofEntry.version === 0) {
        const record: SubOperationRecord = {
          kind: "sub",
          grantId,
          actionId,
          idemKey,
          subId: spec.subId,
          subConsumptionNo: nextOrdinal,
          effectId,
          result: effectResult,
          executedAt: this.#now(),
        };
        try {
          await this.#store.write(recordKey, record, 0);
        } catch (error) {
          if (error instanceof CasConflict) {
            // A concurrent/replayed attempt already wrote the same proof.
            proofKeys.push(recordKey);
            executedSubIds.push(spec.subId);
            continue;
          }
          if (error instanceof SimulatedCrash) {
            return failure(SubmitStatus.Unknown, SubmitCode.ReconcileRequired, error.message, proofKeys);
          }
          throw error;
        }
      }
      proofKeys.push(recordKey);
      executedSubIds.push(spec.subId);
    }

    return this.#terminalSuccess({
      grantId,
      actionId,
      idemKey,
      requiredIds,
      executedSubIds,
      proofKeys,
      snapKey,
    });
  }

  /** Write the SUCCESS decision once and release the claim via one CAS each. */
  async #terminalSuccess(input: {
    grantId: string;
    actionId: string;
    idemKey: string;
    requiredIds: readonly string[];
    executedSubIds: readonly string[];
    proofKeys: readonly string[];
    snapKey: string;
  }): Promise<SubmitResult> {
    const { grantId, actionId, idemKey, requiredIds, executedSubIds, proofKeys, snapKey } = input;
    const key = decisionKey(grantId, actionId, idemKey);
    const existing = await this.#store.read<DecisionRecord>(key);
    if (existing.version !== 0) {
      return this.#resultFromDecision(existing.value!, proofKeys);
    }
    const pendingSubIds = requiredIds.filter((id) => !executedSubIds.includes(id));
    if (pendingSubIds.length > 0) {
      // Mandatory sub-operations are missing: never claim full success.
      return failure(
        SubmitStatus.Partial,
        SubmitCode.ReconcileRequired,
        `mandatory sub-operations not executed: ${pendingSubIds.join(",")}`,
        proofKeys,
      );
    }
    const decision: DecisionRecord = {
      kind: "decision",
      grantId,
      actionId,
      idemKey,
      outcome: "SUCCESS",
      code: SubmitCode.Ok,
      requiredSubIds: [...requiredIds],
      executedSubIds: [...executedSubIds],
      subProofKeys: proofKeys.filter((candidate) => candidate.startsWith("proof/sub/")),
      snapshotKey: snapKey,
      decidedAt: this.#now(),
    };
    try {
      await this.#store.write(key, decision, 0);
    } catch (error) {
      if (error instanceof SimulatedCrash) {
        return failure(SubmitStatus.Unknown, SubmitCode.ReconcileRequired, error.message, proofKeys);
      }
      throw error;
    }
    await this.#releaseClaim(grantId, actionId, idemKey);
    return this.#resultFromDecision(decision, proofKeys);
  }

  /** Persist a terminal FAILED decision but keep executed proofs visible. */
  async #terminalFailure(input: {
    grantId: string;
    actionId: string;
    idemKey: string;
    requiredIds: readonly string[];
    executedSubIds: readonly string[];
    proofKeys: readonly string[];
    snapKey: string;
    code: SubmitCode;
    message: string;
  }): Promise<SubmitResult> {
    const {
      grantId,
      actionId,
      idemKey,
      requiredIds,
      executedSubIds,
      proofKeys,
      snapKey,
      code,
      message,
    } = input;
    const key = decisionKey(grantId, actionId, idemKey);
    const decision: DecisionRecord = {
      kind: "decision",
      grantId,
      actionId,
      idemKey,
      outcome: "FAILED",
      code,
      requiredSubIds: [...requiredIds],
      executedSubIds: [...executedSubIds],
      subProofKeys: proofKeys.filter((candidate) => candidate.startsWith("proof/sub/")),
      snapshotKey: snapKey,
      decidedAt: this.#now(),
    };
    const existing = await this.#store.read<DecisionRecord>(key);
    if (existing.version === 0) {
      await this.#store.write(key, decision, 0);
    }
    await this.#releaseClaim(grantId, actionId, idemKey);
    const storedDecision = existing.version !== 0 ? existing.value! : decision;
    const pendingSubIds = requiredIds.filter((id) => !executedSubIds.includes(id));
    const status =
      executedSubIds.length > 0 && pendingSubIds.length > 0
        ? SubmitStatus.Partial
        : SubmitStatus.Failed;
    return {
      status,
      code,
      message,
      proofKeys,
      decision: storedDecision,
      executedSubIds: [...executedSubIds],
      pendingSubIds,
    };
  }

  async #releaseClaim(grantId: string, actionId: string, idemKey: string): Promise<void> {
    const key = claimKey(grantId, actionId);
    for (let attempt = 0; attempt < 3; attempt += 1) {
      const entry = await this.#store.read<ClaimRecord>(key);
      if (entry.version === 0) {
        return;
      }
      const current = entry.value!;
      if (current.idemKey !== idemKey) {
        return;
      }
      try {
        await this.#store.delete(key, entry.version);
        return;
      } catch (error) {
        if (error instanceof SimulatedCrash) {
          // The terminal decision is already durable. Releasing the pointer
          // is best-effort; a later replay reconciles and retries the delete.
          return;
        }
        if (!(error instanceof CasConflict)) {
          throw error;
        }
      }
    }
  }

  #resultFromDecision(decision: DecisionRecord, proofKeys: readonly string[]): SubmitResult {
    if (decision.outcome !== "SUCCESS") {
      const pendingSubIds = decision.requiredSubIds.filter(
        (id) => !decision.executedSubIds.includes(id),
      );
      return {
        status: pendingSubIds.length > 0 ? SubmitStatus.Partial : SubmitStatus.Failed,
        code: decision.code,
        message: `terminal decision ${decision.outcome}`,
        proofKeys,
        decision,
        executedSubIds: [...decision.executedSubIds],
        pendingSubIds,
      };
    }
    return {
      status: SubmitStatus.Success,
      code: SubmitCode.Ok,
      message: "all required sub-operations executed and proven",
      proofKeys,
      decision,
      executedSubIds: [...decision.executedSubIds],
    };
  }

  /**
   * Explicit reconciliation entry point. It never trusts an occupied claim as
   * evidence that a business effect happened: each effect is verified through
   * its own sub-operation proof and gateway record.
   */
  async reconcile(grantId: string, actionId: string, idemKey: string): Promise<SubmitResult> {
    const snapKey = snapshotKey(grantId, actionId, idemKey);
    const proofKeys: string[] = [];
    const snapshotEntry = await this.#store.read<SnapshotRecord>(snapKey);
    if (snapshotEntry.version !== 0) {
      proofKeys.push(snapKey);
    }
    const allKeys = await this.#store.listKeys();
    const subPrefix = `proof/sub/${grantId}/${actionId}/${idemKey}/`;
    const subKeys = allKeys.filter((key) => key.startsWith(subPrefix)).sort();
    const executedSubIds: string[] = [];
    for (const key of subKeys) {
      const record = (await this.#store.read<SubOperationRecord>(key)).value!;
      // Independently verify the business effect really exists. A claim or
      // counter alone is never sufficient to infer completion.
      const effect = await this.#gateway.findEffect(record.effectId);
      if (!effect) {
        return failure(
          SubmitStatus.Unknown,
          SubmitCode.ReconcileRequired,
          `proof ${key} has no matching gateway effect ${record.effectId}`,
          proofKeys,
        );
      }
      proofKeys.push(key);
      executedSubIds.push(record.subId);
    }

    const decisionEntry = await this.#store.read<DecisionRecord>(
      decisionKey(grantId, actionId, idemKey),
    );
    if (decisionEntry.version !== 0) {
      return this.#resultFromDecision(decisionEntry.value!, proofKeys);
    }

    const pointer = await this.#store.read<ClaimRecord>(claimKey(grantId, actionId));
    const ticket = await this.#store.read<ClaimRecord>(
      claimTicketKey(grantId, actionId, idemKey),
    );
    const ownsWriterSlot = pointer.version !== 0 && pointer.value!.idemKey === idemKey;
    if (!ownsWriterSlot && ticket.version === 0) {
      return failure(
        SubmitStatus.Retryable,
        SubmitCode.TransientFault,
        "no durable claim for this confirmation; the attempt can be retried",
        proofKeys,
      );
    }
    if (executedSubIds.length === 0) {
      return failure(
        SubmitStatus.Retryable,
        SubmitCode.TransientFault,
        "claim ticket exists but no sub-operation effect is proven; safe to continue",
        proofKeys,
      );
    }
    return {
      status: SubmitStatus.Partial,
      code: SubmitCode.ReconcileRequired,
      message: "proven sub-effects exist but no terminal decision; resume the confirmation",
      proofKeys,
      executedSubIds: [...executedSubIds],
    };
  }

  /** Read-only consumption audit used by operators and the test oracle. */
  async reportConsumption(grantId: string, actionId: string): Promise<ConsumptionReport> {
    const allKeys = await this.#store.listKeys();
    const subPrefix = subProofPrefix(grantId, actionId);
    const subConsumptions: Record<string, number> = {};
    for (const key of allKeys.filter((candidate) => candidate.startsWith(subPrefix))) {
      const record = (await this.#store.read<SubOperationRecord>(key)).value!;
      subConsumptions[record.subId] = (subConsumptions[record.subId] ?? 0) + 1;
    }
    const decisions: DecisionRecord[] = [];
    const prefix = decisionPrefix(grantId, actionId);
    for (const key of allKeys.filter((candidate) => candidate.startsWith(prefix)).sort()) {
      decisions.push((await this.#store.read<DecisionRecord>(key)).value!);
    }
    const claimEntry = await this.#store.read<ClaimRecord>(claimKey(grantId, actionId));
    const ticketPrefix = `ledger/claim/${grantId}/${actionId}/`;
    const claimTickets = allKeys.filter((candidate) => candidate.startsWith(ticketPrefix)).length;
    return {
      grantId,
      actionId,
      actionConsumptions: claimTickets,
      activeClaim:
        claimEntry.version !== 0
          ? { idemKey: claimEntry.value!.idemKey, owner: claimEntry.value!.owner }
          : undefined,
      subConsumptions,
      decisions,
    };
  }

  /** Independent proof read-back; throws when the proof key is absent. */
  async readProof<T>(key: string): Promise<T> {
    const entry = await this.#store.read<T>(key);
    if (entry.version === 0) {
      throw new Error(`proof not found: ${key}`);
    }
    return entry.value!;
  }
}
