/**
 * Mutable engine state with snapshot/restore semantics for `@propagate:false`
 * and explicit protected-term registration that centralises collision rules.
 */

import { raise } from "./diagnostic.ts";
import {
  classifyProtectedCollision,
  protectedFingerprint,
} from "./term-definition.ts";
import type {
  ActiveStateSnapshot,
  ContentId,
  ForcedType,
  ProtectedRegistration,
  ResolvedTerm,
} from "./types.ts";

export interface CandidateBinding {
  readonly term: string;
  readonly iri: string;
  readonly reverse: boolean;
  readonly forcedType: ForcedType | null;
  readonly container: readonly string[];
  readonly protectedFlag: boolean;
  readonly nestedUnder: readonly string[];
  readonly provenance: readonly ContentId[];
  readonly definingCid: ContentId;
  readonly importPath: readonly ContentId[];
  readonly depth: number;
}

export class EngineState {
  vocab: string | null = null;
  base: string | null = null;

  readonly terms = new Map<string, ResolvedTerm>();
  readonly protectedTerms = new Map<string, ProtectedRegistration>();
  /** IRI -> term name for every protected forward binding. */
  readonly protectedIriIndex = new Map<string, string>();
  /** Reverse IRI -> term name for every protected reverse binding. */
  readonly protectedReverseIndex = new Map<string, string>();
  /** Nest container name -> bindings parked until the nest is expanded. */
  readonly nestScopes = new Map<string, CandidateBinding[]>();

  snapshot(): ActiveStateSnapshot {
    return {
      vocab: this.vocab,
      base: this.base,
      termBindings: new Map(this.terms),
      protectedTerms: new Map(this.protectedTerms),
      nestScopes: new Map(
        [...this.nestScopes].map(([key, value]) => [key, [...value]]),
      ),
    };
  }

  restore(snapshot: ActiveStateSnapshot): void {
    this.vocab = snapshot.vocab;
    this.base = snapshot.base;
    this.terms = new Map(snapshot.termBindings);
    this.protectedTerms = new Map(snapshot.protectedTerms);
    this.nestScopes = new Map(
      [...snapshot.nestScopes].map(([key, value]) => [key, [...value]]),
    );
    this.rebuildIndexes();
  }

  private rebuildIndexes(): void {
    this.protectedIriIndex.clear();
    this.protectedReverseIndex.clear();
    for (const registration of this.protectedTerms.values()) {
      if (registration.reverse) {
        this.protectedReverseIndex.set(registration.iri, registration.term);
      } else {
        this.protectedIriIndex.set(registration.iri, registration.term);
      }
    }
  }

  /**
   * Register a binding. Protected candidates go through the collision engine;
   * unprotected candidates that would fold onto a protected field are rejected
   * even when the values are identical.
   */
  register(candidate: CandidateBinding): void {
    if (candidate.protectedFlag) {
      this.registerProtected(candidate);
      return;
    }
    this.rejectSilentRewriteOfProtected(candidate);
    this.terms.set(candidate.term, this.toResolved(candidate));
  }

  private registerProtected(candidate: CandidateBinding): void {
    const byTerm = this.protectedTerms.get(candidate.term);
    if (byTerm !== undefined) {
      this.rejectProtected(byTerm, candidate);
      return;
    }
    const sameDirectionIndex = candidate.reverse
      ? this.protectedReverseIndex
      : this.protectedIriIndex;
    const crossDirectionIndex = candidate.reverse
      ? this.protectedIriIndex
      : this.protectedReverseIndex;

    const foldedTerm = sameDirectionIndex.get(candidate.iri);
    if (foldedTerm !== undefined && foldedTerm !== candidate.term) {
      const existing = this.protectedTerms.get(foldedTerm);
      if (existing !== undefined) this.rejectProtected(existing, candidate);
    }
    const crossFoldedTerm = crossDirectionIndex.get(candidate.iri);
    if (crossFoldedTerm !== undefined) {
      const existing = this.protectedTerms.get(crossFoldedTerm);
      if (existing !== undefined) this.rejectProtected(existing, candidate);
    }

    const registration: ProtectedRegistration = {
      term: candidate.term,
      iri: candidate.iri,
      reverse: candidate.reverse,
      forcedType: candidate.forcedType,
      container: [...candidate.container],
      definingCid: candidate.definingCid,
      definitionFingerprint: protectedFingerprint({
        term: candidate.term,
        iri: candidate.iri,
        reverse: candidate.reverse,
        forcedType: candidate.forcedType,
        container: candidate.container,
      }),
    };
    this.protectedTerms.set(candidate.term, registration);
    sameDirectionIndex.set(candidate.iri, candidate.term);
    this.terms.set(candidate.term, this.toResolved(candidate));
  }

  private rejectProtected(
    existing: ProtectedRegistration,
    candidate: CandidateBinding,
  ): void {
    const kind = classifyProtectedCollision(existing, candidate);
    if (kind === "idempotent") {
      this.terms.set(candidate.term, this.toResolved(candidate));
      return;
    }
    const code =
      kind === "reverse"
        ? "PROTECTED_REVERSE_COLLISION"
        : kind === "alias"
          ? "PROTECTED_ALIAS_COLLISION"
          : "PROTECTED_TERM_REDEFINED";
    raise({
      code,
      message: `Protected term ${existing.term} cannot be overridden (${kind})`,
      offendingTerm: candidate.term,
      offendingCid: candidate.definingCid,
      importPath: candidate.importPath,
      depth: candidate.depth,
      detail:
        `existingIri=${existing.iri}; candidateIri=${candidate.iri}; ` +
        `existingCid=${existing.definingCid}; candidateCid=${candidate.definingCid}`,
    });
  }

  /**
   * Unknown/unprotected terms must never silently alter a protected meaning:
   * no alias folding onto a protected IRI, no reverse folding either.
   */
  private rejectSilentRewriteOfProtected(candidate: CandidateBinding): void {
    const crossOwner = candidate.reverse
      ? this.protectedIriIndex.get(candidate.iri)
      : this.protectedReverseIndex.get(candidate.iri);
    if (crossOwner !== undefined) {
      raise({
        code: "PROTECTED_REVERSE_COLLISION",
        message: "Term folds across forward/reverse directions onto a protected field",
        offendingTerm: candidate.term,
        offendingCid: candidate.definingCid,
        importPath: candidate.importPath,
        depth: candidate.depth,
        detail: `protectedOwner=${crossOwner}; iri=${candidate.iri}`,
      });
    }
    if (candidate.reverse) {
      const owner = this.protectedReverseIndex.get(candidate.iri);
      if (owner !== undefined && owner !== candidate.term) {
        raise({
          code: "PROTECTED_ALIAS_COLLISION",
          message: "Unprotected reverse term folds onto a protected field",
          offendingTerm: candidate.term,
          offendingCid: candidate.definingCid,
          importPath: candidate.importPath,
          depth: candidate.depth,
          detail: `protectedOwner=${owner}; iri=${candidate.iri}`,
        });
      }
      return;
    }
    const owner = this.protectedIriIndex.get(candidate.iri);
    if (owner !== undefined && owner !== candidate.term) {
      raise({
        code: "PROTECTED_ALIAS_COLLISION",
        message: "Unprotected alias folds onto a protected field",
        offendingTerm: candidate.term,
        offendingCid: candidate.definingCid,
        importPath: candidate.importPath,
        depth: candidate.depth,
        detail: `protectedOwner=${owner}; iri=${candidate.iri}`,
      });
    }
  }

  parkInNest(scope: string, candidate: CandidateBinding): void {
    const existing = this.nestScopes.get(scope);
    if (existing === undefined) {
      this.nestScopes.set(scope, [candidate]);
      return;
    }
    existing.push(candidate);
  }

  drainNest(scope: string): CandidateBinding[] {
    const queued = this.nestScopes.get(scope) ?? [];
    this.nestScopes.delete(scope);
    return queued;
  }

  private toResolved(candidate: CandidateBinding): ResolvedTerm {
    return {
      term: candidate.term,
      iri: candidate.iri,
      reverse: candidate.reverse,
      forcedType: candidate.forcedType,
      container: [...candidate.container],
      protectedFlag: candidate.protectedFlag,
      nestedUnder: [...candidate.nestedUnder],
      provenance: [...candidate.provenance],
    };
  }
}
