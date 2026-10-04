import fs from "node:fs";
import path from "node:path";
import { pathKey } from "./paths.ts";
import type {
  AccessRecord,
  EffectiveCasePolicy,
  SupplementManifest,
} from "./types.ts";

/**
 * Build the minimal supplemental manifest covering every undeclared
 * access. "Minimal" means the fewest manifest entries:
 *
 *  - two or more undeclared paths that are siblings (same immediate
 *    parent directory) are emitted as a single `directories` entry for
 *    their parent instead of one entry per file, but only when every
 *    direct child of that parent is an undeclared target — otherwise
 *    the directory entry would over-authorize unrelated siblings;
 *  - everything else is emitted as an individual `files` entry;
 *  - exec-kind accesses are always emitted individually as
 *    `executables` entries and are never folded into directories.
 *
 * Directory entries use canonical paths, so a later audit of the
 * supplemented manifest still rejects symlink escapes. Folding is
 * deliberately limited to the sibling level: rolling up into higher
 * ancestors would over-authorize unrelated subtrees.
 */
export function buildSupplement(
  undeclared: AccessRecord[],
  policy: EffectiveCasePolicy,
): SupplementManifest {
  const fileTargets = new Map<string, string>(); // key -> canonical path
  const execTargets = new Map<string, string>();

  for (const record of undeclared) {
    const target = record.resolved.canonical;
    const key = pathKey(target, policy);
    if (record.kinds.includes("exec")) {
      execTargets.set(key, target);
    } else {
      fileTargets.set(key, target);
    }
  }

  // Count undeclared file targets per immediate parent directory.
  const dirCounts = new Map<string, { count: number; display: string }>();
  for (const target of fileTargets.values()) {
    const dir = path.dirname(target);
    const dirKey = pathKey(dir, policy);
    const entry = dirCounts.get(dirKey);
    if (entry) entry.count++;
    else dirCounts.set(dirKey, { count: 1, display: dir });
  }

  // Parents of two or more undeclared siblings become directory entries,
  // provided folding would not authorize anything beyond the undeclared
  // set (every direct child of the parent must itself be a target).
  const selectedDirs: { key: string; display: string }[] = [];
  for (const [key, info] of dirCounts) {
    if (info.count < 2) continue;
    if (!fullyCovered(info.display, fileTargets, policy)) continue;
    selectedDirs.push({ key, display: info.display });
  }

  const directories = selectedDirs.map((d) => d.display).sort();

  const files: string[] = [];
  for (const [key, target] of fileTargets) {
    const covered = selectedDirs.some((sel) => {
      const prefix = sel.key.endsWith(path.sep) ? sel.key : sel.key + path.sep;
      return key.startsWith(prefix);
    });
    if (!covered) files.push(target);
  }
  files.sort();

  const executables = [...execTargets.values()].sort();
  return { files, directories, executables };
}

/**
 * True when every direct child of `dir` is one of the undeclared file
 * targets, so replacing the individual entries with a directory entry
 * authorizes nothing extra.
 */
function fullyCovered(
  dir: string,
  fileTargets: Map<string, string>,
  policy: EffectiveCasePolicy,
): boolean {
  let children: string[];
  try {
    children = fs.readdirSync(dir);
  } catch {
    return false; // e.g. parent of a nonexistent path
  }
  if (children.length === 0) return false;
  for (const child of children) {
    const childKey = pathKey(path.join(dir, child), policy);
    if (!fileTargets.has(childKey)) return false;
  }
  return true;
}
