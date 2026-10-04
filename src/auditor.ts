import * as fs from "fs";
import {
  AccessLog,
  AccessRecord,
  AuditReport,
  AuditedAccess,
  CasePolicy,
  DeclEntry,
  SupplementEntry,
} from "./types";
import { foldCase, isWithinDir, resolveCasePolicy } from "./fspaths";

export interface AuditOptions {
  casePolicy: CasePolicy;
  /** Directory used to probe case sensitivity when policy is "auto". */
  probeDir: string;
}

/**
 * A declaration paired with the symlink-resolved form of its path, so that
 * ancestors which are themselves symlinks (e.g. /var -> /private/var) do
 * not cause false escapes.
 */
interface PreparedDecl {
  decl: DeclEntry;
  /** realpath of decl.path when it exists, otherwise the lexical path. */
  real: string;
}

function prepareDeclarations(decls: DeclEntry[]): PreparedDecl[] {
  return decls.map((decl) => {
    let real = decl.path;
    try {
      real = fs.realpathSync(decl.path);
    } catch {
      /* non-existent declaration: keep lexical form */
    }
    return { decl, real };
  });
}

/**
 * Audit an access log against a declaration manifest.
 *
 * Authorization rules:
 *  - A file declaration authorizes exactly its own path.
 *  - A dir declaration authorizes paths lexically beneath it (recursively
 *    unless recursive:false), BUT a symlink hop made from inside the
 *    declared dir that resolves outside it is never authorized by that dir
 *    declaration: the escape target must be declared explicitly.
 *  - An access through a symlink is authorized when the fully resolved
 *    target is itself covered by a declaration.
 *  - Non-existent paths are matched lexically (they cannot escape, since
 *    no symlink could be traversed past the missing component).
 */
export function audit(
  log: AccessLog,
  declarations: DeclEntry[],
  options: AuditOptions
): AuditReport {
  const policy = resolveCasePolicy(options.casePolicy, options.probeDir);
  const prepared = prepareDeclarations(declarations);

  const groups = groupDuplicates(log.records, policy);
  const accesses: AuditedAccess[] = [];
  const supplement: SupplementEntry[] = [];

  for (const group of groups.values()) {
    const audited = auditRecord(group.rep, prepared, policy);
    audited.count = group.count;
    accesses.push(audited);
    if (audited.status !== "declared") {
      supplement.push(supplementFor(audited));
    }
  }

  const minimal = minimizeSupplement(supplement, policy);

  const summary = {
    totalAccesses: log.records.length,
    uniqueAccesses: accesses.length,
    declared: accesses.filter((a) => a.status === "declared").length,
    undeclared: accesses.filter((a) => a.status === "undeclared").length,
    symlinkEscapes: accesses.filter((a) => a.status === "symlink-escape")
      .length,
    missing: accesses.filter((a) => !a.exists).length,
  };

  return { casePolicy: policy, summary, accesses, supplement: minimal };
}

function groupDuplicates(
  records: AccessRecord[],
  policy: "sensitive" | "insensitive"
): Map<string, { rep: AccessRecord; count: number }> {
  const groups = new Map<string, { rep: AccessRecord; count: number }>();
  for (const rec of records) {
    // Duplicates are the same accessed path performing the same kind of op;
    // the resolved target is part of the key so a changed link target is
    // surfaced as a distinct access.
    const key = [
      rec.op,
      foldCase(rec.path, policy),
      rec.final ? foldCase(rec.final, policy) : "<unresolved>",
    ].join(" ");
    const existing = groups.get(key);
    if (existing) {
      existing.count += 1;
    } else {
      groups.set(key, { rep: rec, count: 1 });
    }
  }
  return groups;
}

function auditRecord(
  rec: AccessRecord,
  prepared: PreparedDecl[],
  policy: "sensitive" | "insensitive"
): AuditedAccess {
  const base: AuditedAccess = {
    op: rec.op,
    path: rec.path,
    final: rec.final,
    exists: rec.exists,
    isDirectory: rec.isDirectory,
    chain: rec.chain,
    status: "undeclared",
    count: 1,
    error: rec.error,
  };

  // 1. Lexical coverage of the accessed path.
  const covering = prepared.filter((p) =>
    declarationCovers(p, rec.path, policy)
  );
  if (covering.length > 0) {
    const escape = findEscape(rec, covering, policy);
    if (!escape) {
      return { ...base, status: "declared", authorizedBy: covering[0].decl };
    }
    return {
      ...base,
      status: "symlink-escape",
      reason: `symlink ${escape.at} escapes declared directory to ${escape.resolved}`,
    };
  }

  // 2. Coverage of the fully resolved target (access via symlink from an
  //    undeclared location into a declared one is fine).
  if (rec.final && rec.final !== rec.path) {
    const finalCover = prepared.find((p) =>
      declarationCovers(p, rec.final as string, policy)
    );
    if (finalCover) {
      return { ...base, status: "declared", authorizedBy: finalCover.decl };
    }
  }

  return {
    ...base,
    status: "undeclared",
    reason: rec.exists
      ? "no declaration covers this path"
      : `path does not exist (missing at ${rec.missingAt ?? rec.path}) and no declaration covers it`,
  };
}

/** Lexical coverage of one declaration against an accessed path. */
function declarationCovers(
  p: PreparedDecl,
  accessPath: string,
  policy: "sensitive" | "insensitive"
): boolean {
  return (
    coversWithBase(p.decl, p.decl.path, accessPath, policy) ||
    (p.real !== p.decl.path &&
      coversWithBase(p.decl, p.real, accessPath, policy))
  );
}

function coversWithBase(
  decl: DeclEntry,
  basePath: string,
  accessPath: string,
  policy: "sensitive" | "insensitive"
): boolean {
  const same = foldCase(basePath, policy) === foldCase(accessPath, policy);
  if (decl.type === "file") return same;
  if (same) return true;
  if (decl.recursive === false) {
    // Non-recursive dir: direct children only.
    const parent = parentDir(accessPath);
    return (
      parent !== null &&
      foldCase(parent, policy) === foldCase(basePath, policy)
    );
  }
  return isWithinDir(accessPath, basePath, policy);
}

function parentDir(p: string): string | null {
  const idx = p.lastIndexOf("/");
  if (idx <= 0) return null;
  return p.slice(0, idx);
}

/**
 * A dir declaration must not silently authorize symlink targets that jump
 * out of the declared tree. Only hops taken from inside the declared dir
 * count: symlinks in ancestor components are outside the declaration's
 * control and are ignored. Returns the first escaping hop, if any.
 */
function findEscape(
  rec: AccessRecord,
  covering: PreparedDecl[],
  policy: "sensitive" | "insensitive"
): { at: string; resolved: string } | null {
  const dirs = covering.filter((p) => p.decl.type === "dir");
  if (dirs.length === 0) return null;

  const containedBy = (p: PreparedDecl): boolean => {
    const bases = p.real !== p.decl.path ? [p.decl.path, p.real] : [p.decl.path];
    const within = (q: string) =>
      bases.some((b) => isWithinDir(q, b, policy));
    const innerHops = rec.chain.filter((hop) => within(hop.at));
    const hopsOk = innerHops.every((hop) => within(hop.resolved));
    const finalOk = rec.final === null || within(rec.final);
    return hopsOk && finalOk;
  };

  // Contained as long as at least one covering dir fully contains the walk.
  if (dirs.some(containedBy)) return null;

  for (const hop of rec.chain) {
    const relevant = dirs.some((p) => {
      const bases =
        p.real !== p.decl.path ? [p.decl.path, p.real] : [p.decl.path];
      return bases.some((b) => isWithinDir(hop.at, b, policy));
    });
    if (!relevant) continue;
    const escapesAll = dirs.every((p) => {
      const bases =
        p.real !== p.decl.path ? [p.decl.path, p.real] : [p.decl.path];
      return !bases.some((b) => isWithinDir(hop.resolved, b, policy));
    });
    if (escapesAll) return { at: hop.at, resolved: hop.resolved };
  }
  // Final target escaped without an intermediate hop outside (defensive).
  return { at: rec.path, resolved: rec.final ?? rec.path };
}

function supplementFor(audited: AuditedAccess): SupplementEntry {
  if (audited.status === "symlink-escape" && audited.final) {
    // The real dependency is the resolved target outside the declared dir.
    return {
      path: audited.final,
      type: audited.isDirectory ? "dir" : "file",
      reason: `symlink escape: ${audited.reason}`,
    };
  }
  return {
    path: audited.path,
    type: audited.isDirectory || audited.op === "readdir" ? "dir" : "file",
    reason: audited.reason ?? "undeclared access",
  };
}

/**
 * Minimal supplement: dedupe identical entries and drop file entries that
 * are already covered by a dir entry present in the supplement.
 */
export function minimizeSupplement(
  entries: SupplementEntry[],
  policy: "sensitive" | "insensitive"
): SupplementEntry[] {
  const byKey = new Map<string, SupplementEntry>();
  for (const e of entries) {
    const key = `${e.type}:${foldCase(e.path, policy)}`;
    if (!byKey.has(key)) byKey.set(key, e);
  }
  const dirs = [...byKey.values()].filter((e) => e.type === "dir");
  const result = [...byKey.values()].filter((e) => {
    if (e.type === "dir") return true;
    // A file entry is redundant when a supplement dir already covers it and
    // the entry was not itself an escape (escapes must stay explicit).
    const covered = dirs.some(
      (d) =>
        d.path !== e.path &&
        isWithinDir(e.path, d.path, policy) &&
        !/symlink escape/.test(e.reason)
    );
    return !covered;
  });
  return result.sort((a, b) => a.path.localeCompare(b.path));
}
