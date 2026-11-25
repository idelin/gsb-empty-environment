import { verifyEnvelope } from "./signing.ts";
import type {
  AuthorizationBasis,
  KeyDirectory,
  SignedConsentStatement,
} from "./types.ts";

/**
 * Independently re-verify every signature cited in a delivery basis against a
 * key directory. Receivers can call this on a published receipt without
 * trusting the interpreter process.
 */
export function verifyBasisSignatures(
  basis: AuthorizationBasis,
  statements: readonly SignedConsentStatement[],
  keyDirectory: KeyDirectory,
): boolean {
  const byId = new Map(
    statements.map((envelope) => [envelope.statement.id, envelope]),
  );
  const cited = [...basis.grants, ...basis.revocations];
  if (cited.length === 0) {
    return false;
  }
  for (const evidence of cited) {
    const envelope = byId.get(evidence.statementId);
    if (!envelope) {
      return false;
    }
    const ok = verifyEnvelope(
      envelope,
      keyDirectory,
      (statement) => statement.issuer,
    );
    if (!ok || envelope.signature !== evidence.signature) {
      return false;
    }
  }
  return true;
}
