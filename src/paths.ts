import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import type { CasePolicy, EffectiveCasePolicy } from "./types.ts";

/**
 * Probe the filesystem that contains `dir` to decide whether path
 * comparison should be case-sensitive. Falls back to a per-OS guess.
 */
export function detectCasePolicy(dir: string): EffectiveCasePolicy {
  const base = path.resolve(dir);
  const probeDir = fs.existsSync(base) ? base : path.dirname(base);
  const name = `.__case_probe_${process.pid}_${Math.random().toString(36).slice(2)}`;
  const probe = path.join(probeDir, name);
  try {
    fs.writeFileSync(probe, "");
    const flipped = path.join(probeDir, name.toUpperCase());
    return fs.existsSync(flipped) ? "insensitive" : "sensitive";
  } catch {
    return os.platform() === "darwin" || os.platform() === "win32"
      ? "insensitive"
      : "sensitive";
  } finally {
    try {
      fs.rmSync(probe, { force: true });
    } catch {
      /* ignore */
    }
  }
}

export function resolveCasePolicy(
  policy: CasePolicy | undefined,
  probeDir: string,
): EffectiveCasePolicy {
  if (policy === "sensitive" || policy === "insensitive") return policy;
  return detectCasePolicy(probeDir);
}

/** Comparison key for a path under the given case policy. */
export function pathKey(p: string, policy: EffectiveCasePolicy): string {
  const normalized = path.normalize(p);
  return policy === "insensitive" ? normalized.toLowerCase() : normalized;
}

/**
 * True when `child` equals `ancestor` or lives strictly beneath it,
 * compared on path boundaries under the given case policy.
 */
export function isUnder(
  child: string,
  ancestor: string,
  policy: EffectiveCasePolicy,
): boolean {
  const childKey = pathKey(child, policy);
  const ancestorKey = pathKey(ancestor, policy);
  if (childKey === ancestorKey) return true;
  const prefix = ancestorKey.endsWith(path.sep)
    ? ancestorKey
    : ancestorKey + path.sep;
  return childKey.startsWith(prefix);
}
