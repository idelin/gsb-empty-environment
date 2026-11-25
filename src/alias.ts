import type {
  Alias,
  AliasAssertion,
  SignedAliasAssertion,
  SubjectId,
} from "./types.ts";
import type { ChainIssue } from "./materialize.ts";

export interface AliasResolution {
  status: "resolved" | "unknown" | "ambiguous";
  subject?: SubjectId;
}

export interface AliasDirectoryView {
  resolve(alias: Alias): AliasResolution;
  readonly issues: ReadonlySet<ChainIssue>;
  /** All subjects an alias has ever been asserted to (for diagnostics). */
  subjectsOf(alias: Alias): readonly SubjectId[];
}

/**
 * Build the global alias view. Multiple aliases may map to one subject and
 * share its consent. A single alias asserted to map to two subjects makes that
 * recipient ambiguous and forces any item addressed to it into "pending".
 */
export function buildAliasDirectory(
  assertions: readonly SignedAliasAssertion[],
  verify: (envelope: SignedAliasAssertion) => boolean,
): AliasDirectoryView {
  const byAlias = new Map<Alias, Map<SubjectId, AliasAssertion>>();
  const issues = new Set<ChainIssue>();

  for (const envelope of assertions) {
    if (!verify(envelope)) {
      issues.add("unverified-signature");
      continue;
    }
    const assertion = envelope.statement;
    let map = byAlias.get(assertion.alias);
    if (!map) {
      map = new Map();
      byAlias.set(assertion.alias, map);
    }
    const existing = map.get(assertion.subject);
    if (!existing || assertion.issuedAt >= existing.issuedAt) {
      map.set(assertion.subject, assertion);
    }
  }

  const ambiguous = new Set<Alias>();
  for (const [alias, map] of byAlias) {
    if (map.size > 1) {
      ambiguous.add(alias);
    }
  }

  return {
    issues,
    resolve(alias) {
      const map = byAlias.get(alias);
      if (!map || map.size === 0) {
        return { status: "unknown" };
      }
      if (ambiguous.has(alias) || map.size > 1) {
        return { status: "ambiguous" };
      }
      return { status: "resolved", subject: [...map.keys()][0] };
    },
    subjectsOf(alias) {
      return [...(byAlias.get(alias)?.keys() ?? [])];
    },
  };
}
