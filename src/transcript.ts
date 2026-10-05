import { hashCanonical } from "./hash.ts";
import {
  computeDeclarationDigest,
  negotiate,
  NegotiationError,
} from "./negotiate.ts";
import type { Declaration, NegotiationResult } from "./types.ts";

export type ValidationCode =
  | "ROUND_MISMATCH"
  | "SAME_PARTY"
  | "DECLARATION_TAMPERED"
  | "REPLAY_DETECTED"
  | "STALE_DECLARATION"
  | "TRANSCRIPT_MISMATCH";

export interface ValidationIssue {
  code: ValidationCode;
  message: string;
  party?: string;
}

export interface ValidationReport {
  ok: boolean;
  issues: ValidationIssue[];
}

export interface TranscriptValidatorOptions {
  maxDeclarationAgeMs?: number;
  now?: () => number;
}

export class TranscriptValidator {
  private readonly noncesByParty = new Map<string, Map<string, string>>();
  private readonly maxDeclarationAgeMs?: number;
  private readonly now: () => number;

  constructor(options: TranscriptValidatorOptions = {}) {
    if (options.maxDeclarationAgeMs !== undefined) {
      this.maxDeclarationAgeMs = options.maxDeclarationAgeMs;
    }
    this.now = options.now ?? (() => Date.now());
  }

  validate(
    result: NegotiationResult,
    declA: Declaration,
    declB: Declaration,
  ): ValidationReport {
    const issues: ValidationIssue[] = [];
    const declarations = [declA, declB];

    if (declA.party === declB.party) {
      issues.push({
        code: "SAME_PARTY",
        message: `both declarations belong to party "${declA.party}"`,
        party: declA.party,
      });
    }

    for (const declaration of declarations) {
      const { digest, ...body } = declaration;
      if (computeDeclarationDigest(body) !== digest) {
        issues.push({
          code: "DECLARATION_TAMPERED",
          message: `declaration of party "${declaration.party}" does not match its digest`,
          party: declaration.party,
        });
      }
      const bound = result.declarationDigests[declaration.party];
      if (bound !== declaration.digest) {
        issues.push({
          code: "DECLARATION_TAMPERED",
          message:
            `result is not bound to the presented declaration of party ` +
            `"${declaration.party}" (bound digest: ${bound ?? "none"})`,
          party: declaration.party,
        });
      }
      if (declaration.round !== result.round) {
        issues.push({
          code: "ROUND_MISMATCH",
          message:
            `declaration of party "${declaration.party}" belongs to round ` +
            `"${declaration.round}" but the result claims round "${result.round}"`,
          party: declaration.party,
        });
      }
    }

    if (declA.round !== declB.round) {
      issues.push({
        code: "ROUND_MISMATCH",
        message:
          `declarations belong to different rounds ("${declA.round}" vs ` +
          `"${declB.round}"); a replayed declaration from an older round is not ` +
          "valid for the current round",
      });
    }

    for (const declaration of declarations) {
      const replayIssue = this.checkReplay(declaration);
      if (replayIssue) issues.push(replayIssue);
      const staleIssue = this.checkFreshness(declaration);
      if (staleIssue) issues.push(staleIssue);
    }

    const structuralFailure = issues.some((issue) =>
      issue.code === "DECLARATION_TAMPERED" || issue.code === "ROUND_MISMATCH" ||
      issue.code === "SAME_PARTY"
    );
    if (!structuralFailure) {
      try {
        const recomputed = negotiate(declA, declB);
        if (recomputed.transcriptHash !== result.transcriptHash) {
          issues.push({
            code: "TRANSCRIPT_MISMATCH",
            message:
              "re-running the negotiation over the presented declarations does " +
              "not reproduce the recorded transcript hash",
          });
        }
        if (hashCanonical(stripHash(recomputed)) !== hashCanonical(stripHash(result))) {
          issues.push({
            code: "TRANSCRIPT_MISMATCH",
            message: "recorded negotiation outcome differs from the recomputed outcome",
          });
        }
      } catch (error) {
        if (error instanceof NegotiationError) {
          issues.push({
            code: "TRANSCRIPT_MISMATCH",
            message: `negotiation cannot be reproduced: ${error.message}`,
          });
        } else {
          throw error;
        }
      }
    }

    for (const declaration of declarations) {
      this.registerNonce(declaration);
    }

    return { ok: issues.length === 0, issues };
  }

  private checkReplay(declaration: Declaration): ValidationIssue | null {
    const seen = this.noncesByParty.get(declaration.party);
    const seenRound = seen?.get(declaration.nonce);
    if (seenRound !== undefined && seenRound !== declaration.round) {
      return {
        code: "REPLAY_DETECTED",
        message:
          `nonce of party "${declaration.party}" was already used in round ` +
          `"${seenRound}"; the declaration is a replay from another round`,
        party: declaration.party,
      };
    }
    return null;
  }

  private checkFreshness(declaration: Declaration): ValidationIssue | null {
    if (this.maxDeclarationAgeMs === undefined) return null;
    const age = this.now() - declaration.issuedAt;
    if (age > this.maxDeclarationAgeMs) {
      return {
        code: "STALE_DECLARATION",
        message:
          `declaration of party "${declaration.party}" is ${age}ms old, ` +
          `beyond the allowed ${this.maxDeclarationAgeMs}ms`,
        party: declaration.party,
      };
    }
    return null;
  }

  private registerNonce(declaration: Declaration): void {
    let seen = this.noncesByParty.get(declaration.party);
    if (!seen) {
      seen = new Map<string, string>();
      this.noncesByParty.set(declaration.party, seen);
    }
    if (!seen.has(declaration.nonce)) {
      seen.set(declaration.nonce, declaration.round);
    }
  }
}

function stripHash(result: NegotiationResult): Omit<NegotiationResult, "transcriptHash"> {
  const { transcriptHash, ...rest } = result;
  void transcriptHash;
  return rest;
}
