import { isUnder, pathKey } from "./paths.ts";
import { resolvePath } from "./resolver.ts";
import { buildSupplement } from "./minimize.ts";
import type {
  AccessEvent,
  AccessKind,
  AccessRecord,
  AuditReport,
  EffectiveCasePolicy,
  PreparedDecl,
} from "./types.ts";

interface MatchResult {
  matched: boolean;
  matchedBy?: { kind: PreparedDecl["kind"]; path: string };
  reason?: string;
}

function sameTarget(a: string, b: string, policy: EffectiveCasePolicy): boolean {
  return pathKey(a, policy) === pathKey(b, policy);
}

/**
 * A file/executable declaration authorizes an access when the access
 * path and the declared path name the same object, whether compared
 * lexically or after symlink expansion (either side may be the link).
 */
function matchExact(
  decl: PreparedDecl,
  absolute: string,
  canonical: string,
  policy: EffectiveCasePolicy,
): boolean {
  return (
    sameTarget(decl.absolute, absolute, policy) ||
    sameTarget(decl.canonical, canonical, policy) ||
    sameTarget(decl.absolute, canonical, policy) ||
    sameTarget(decl.canonical, absolute, policy)
  );
}

/**
 * A directory declaration authorizes an access only when the access is
 * inside the directory BOTH lexically and after symlink expansion.
 * A symlink inside a declared directory that points outside of it does
 * not inherit the directory's authorization.
 */
function matchDirectory(
  decl: PreparedDecl,
  absolute: string,
  canonical: string,
  policy: EffectiveCasePolicy,
): { ok: boolean; escaped: boolean } {
  if (!isUnder(absolute, decl.absolute, policy)) {
    return { ok: false, escaped: false };
  }
  const resolvedInside = isUnder(canonical, decl.canonical, policy);
  return { ok: resolvedInside, escaped: !resolvedInside };
}

function matchAccess(
  decls: PreparedDecl[],
  kind: AccessKind,
  absolute: string,
  canonical: string,
  policy: EffectiveCasePolicy,
): MatchResult {
  let escapedDir: PreparedDecl | undefined;
  for (const decl of decls) {
    if (decl.kind === "directory") {
      if (kind === "exec") continue; // directories never authorize execution
      const { ok, escaped } = matchDirectory(decl, absolute, canonical, policy);
      if (ok) return { matched: true, matchedBy: { kind: decl.kind, path: decl.raw } };
      if (escaped && !escapedDir) escapedDir = decl;
    } else if (decl.kind === "executable") {
      if (kind !== "exec") continue;
      if (matchExact(decl, absolute, canonical, policy)) {
        return { matched: true, matchedBy: { kind: decl.kind, path: decl.raw } };
      }
    } else {
      if (kind === "exec") continue; // plain files do not authorize execution
      if (matchExact(decl, absolute, canonical, policy)) {
        return { matched: true, matchedBy: { kind: decl.kind, path: decl.raw } };
      }
    }
  }
  if (escapedDir) {
    return {
      matched: false,
      reason:
        `symlink chain escapes declared directory "${escapedDir.raw}"; ` +
        "directory declarations do not authorize targets outside the directory",
    };
  }
  return { matched: false, reason: "no matching declaration" };
}

export interface AuditOptions {
  policy: EffectiveCasePolicy;
  cwd?: string;
}

export function audit(
  events: AccessEvent[],
  decls: PreparedDecl[],
  options: AuditOptions,
): AuditReport {
  const cwd = options.cwd ?? process.cwd();
  const policy = options.policy;

  // Fold duplicate accesses: same lexical path resolving to same target.
  const records = new Map<string, AccessRecord>();
  let duplicateEvents = 0;

  for (const event of events) {
    const kind: AccessKind = event.kind ?? "read";
    const resolved = resolvePath(event.path, cwd);
    const key = JSON.stringify([
      pathKey(resolved.absolute, policy),
      pathKey(resolved.canonical, policy),
    ]);
    const existing = records.get(key);
    if (existing) {
      duplicateEvents++;
      existing.count++;
      if (!existing.kinds.includes(kind)) existing.kinds.push(kind);
      continue;
    }

    let record: AccessRecord;
    if (resolved.loop) {
      record = {
        path: event.path,
        kinds: [kind],
        count: 1,
        resolved,
        status: "unverifiable",
        reason: "symlink loop detected; access cannot be verified",
      };
    } else {
      const match = matchAccess(
        decls,
        kind,
        resolved.absolute,
        resolved.canonical,
        policy,
      );
      record = {
        path: event.path,
        kinds: [kind],
        count: 1,
        resolved,
        status: match.matched ? "declared" : "undeclared",
        ...(match.matchedBy ? { matchedBy: match.matchedBy } : {}),
        ...(match.reason ? { reason: match.reason } : {}),
      };
      if (!resolved.exists) {
        const note = `path does not exist (missing: ${resolved.missingAt})`;
        record.reason = record.reason ? `${record.reason}; ${note}` : note;
      }
    }
    records.set(key, record);
  }

  const accesses = [...records.values()];
  const undeclared = accesses.filter((a) => a.status !== "declared");
  const supplement = buildSupplement(undeclared, policy);

  return {
    casePolicy: policy,
    summary: {
      totalEvents: events.length,
      uniqueAccesses: accesses.length,
      duplicateEvents,
      declared: accesses.length - undeclared.length,
      undeclared: undeclared.filter((a) => a.status === "undeclared").length,
      unverifiable: undeclared.filter((a) => a.status === "unverifiable").length,
      missingPaths: accesses.filter((a) => !a.resolved.exists).length,
    },
    accesses,
    undeclared,
    supplement,
  };
}
