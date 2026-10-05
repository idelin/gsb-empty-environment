import { hashCanonical, randomNonce } from "./hash.ts";
import { solveConfiguration } from "./solver.ts";
import type {
  CapabilityOffer,
  Declaration,
  NegotiationResult,
} from "./types.ts";

export class NegotiationError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = "NegotiationError";
    this.code = code;
  }
}

export function createDeclaration(
  party: string,
  round: string,
  capabilities: CapabilityOffer[],
  issuedAt: number = Date.now(),
): Declaration {
  if (!party) throw new NegotiationError("INVALID_DECLARATION", "party is required");
  if (!round) throw new NegotiationError("INVALID_DECLARATION", "round is required");
  const draft = {
    party,
    round,
    nonce: randomNonce(),
    issuedAt,
    capabilities,
  };
  return { ...draft, digest: computeDeclarationDigest(draft) };
}

export function computeDeclarationDigest(
  declaration: Omit<Declaration, "digest">,
): string {
  return hashCanonical({
    party: declaration.party,
    round: declaration.round,
    nonce: declaration.nonce,
    issuedAt: declaration.issuedAt,
    capabilities: declaration.capabilities,
  });
}

export function verifyDeclarationIntegrity(declaration: Declaration): boolean {
  const { digest, ...body } = declaration;
  return computeDeclarationDigest(body) === digest;
}

function assertNegotiable(declA: Declaration, declB: Declaration): void {
  if (declA.party === declB.party) {
    throw new NegotiationError(
      "SAME_PARTY",
      `both declarations belong to party "${declA.party}"`,
    );
  }
  if (declA.round !== declB.round) {
    throw new NegotiationError(
      "ROUND_MISMATCH",
      `declarations belong to different rounds ("${declA.round}" vs "${declB.round}"); ` +
        "a replayed declaration from an older round must not be mixed into a new round",
    );
  }
  if (!verifyDeclarationIntegrity(declA)) {
    throw new NegotiationError(
      "DECLARATION_TAMPERED",
      `declaration of party "${declA.party}" fails its digest check`,
    );
  }
  if (!verifyDeclarationIntegrity(declB)) {
    throw new NegotiationError(
      "DECLARATION_TAMPERED",
      `declaration of party "${declB.party}" fails its digest check`,
    );
  }
}

export function negotiate(declA: Declaration, declB: Declaration): NegotiationResult {
  assertNegotiable(declA, declB);

  const outcome = solveConfiguration(declA, declB);
  const parties: [string, string] = [declA.party, declB.party].sort() as [
    string,
    string,
  ];
  const declarationDigests: Record<string, string> = {
    [declA.party]: declA.digest,
    [declB.party]: declB.digest,
  };

  const base = {
    status: outcome.status,
    round: declA.round,
    parties,
    config: outcome.status === "AGREED" ? outcome.config : null,
    score: outcome.status === "AGREED" ? outcome.score : null,
    conflict: outcome.status === "CONFLICT" ? outcome.conflict : null,
    declarationDigests,
  };

  return { ...base, transcriptHash: hashCanonical(base) };
}
