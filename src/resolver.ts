import fs from "node:fs";
import path from "node:path";
import type { ResolvedPath, SymlinkHop } from "./types.ts";

const MAX_HOPS = 40; // mirrors the kernel's ELOOP limit

interface WalkResult {
  canonical: string;
  exists: boolean;
  missingAt?: string;
  loop: boolean;
}

function isAbs(p: string): boolean {
  return path.isAbsolute(p);
}

/**
 * Walk every component of `absPath`; whenever a component is a symlink,
 * record the hop and restart the walk from the resolved target. This
 * yields the full ordered expansion chain, handles chains of links,
 * absolute and relative targets, loops, and dangling links.
 */
function walk(
  absPath: string,
  hops: SymlinkHop[],
  seen: Set<string>,
): WalkResult {
  if (hops.length >= MAX_HOPS) {
    return { canonical: absPath, exists: false, loop: true };
  }
  if (seen.has(absPath)) {
    return { canonical: absPath, exists: false, loop: true };
  }
  seen.add(absPath);

  const parsed = path.parse(absPath);
  const rel = absPath.slice(parsed.root.length);
  const parts = rel.split(path.sep).filter((p) => p.length > 0);

  let current = parsed.root;
  for (let i = 0; i < parts.length; i++) {
    const candidate = path.join(current, parts[i]);
    let stat: fs.Stats;
    try {
      stat = fs.lstatSync(candidate);
    } catch {
      // Component does not exist (or is not stat-able): the remaining
      // suffix is appended lexically so callers still get a stable path.
      const rest = parts.slice(i).join(path.sep);
      const canonical = rest ? path.join(current, rest) : current;
      return { canonical, exists: false, missingAt: candidate, loop: false };
    }
    if (stat.isSymbolicLink()) {
      let target: string;
      try {
        target = fs.readlinkSync(candidate);
      } catch {
        return { canonical: candidate, exists: false, missingAt: candidate, loop: false };
      }
      const resolvedTo = isAbs(target)
        ? path.normalize(target)
        : path.resolve(path.dirname(candidate), target);
      hops.push({ at: candidate, target, resolvedTo });
      const rest = parts.slice(i + 1).join(path.sep);
      const next = rest ? path.join(resolvedTo, rest) : resolvedTo;
      return walk(next, hops, seen);
    }
    current = candidate;
  }
  return { canonical: current, exists: true, loop: false };
}

/**
 * Resolve `input` to an absolute path, expanding every symlink along the
 * way and recording the expansion chain. Never throws: nonexistent paths
 * resolve lexically from the first missing component.
 */
export function resolvePath(input: string, cwd: string = process.cwd()): ResolvedPath {
  const absolute = path.resolve(cwd, input);
  const hops: SymlinkHop[] = [];
  const result = walk(absolute, hops, new Set());

  let canonical = result.canonical;
  if (result.exists) {
    // realpath also normalizes the on-disk casing (relevant on
    // case-insensitive filesystems); prefer it when available.
    try {
      canonical = fs.realpathSync(absolute);
    } catch {
      /* keep manual result */
    }
  }

  const resolved: ResolvedPath = {
    input,
    absolute,
    canonical,
    hops,
    exists: result.exists,
    loop: result.loop,
  };
  if (result.missingAt !== undefined) resolved.missingAt = result.missingAt;
  return resolved;
}
