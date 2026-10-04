import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { CasePolicy } from "./types";

/**
 * Normalize a path to an absolute, lexical form (no symlink resolution).
 * Relative paths are resolved against `base`.
 */
export function normalizeAbs(p: string, base: string): string {
  const abs = path.isAbsolute(p) ? p : path.join(base, p);
  return path.normalize(abs);
}

/**
 * Detect whether the filesystem holding `dir` is case-insensitive by
 * round-tripping a probe file with flipped casing.
 */
export function detectCaseSensitivity(dir: string): "sensitive" | "insensitive" {
  const probeDir = dir && safeIsDir(dir) ? dir : os.tmpdir();
  const name = `.access-audit-case-probe-${process.pid}-${Date.now()}`;
  const lower = path.join(probeDir, name);
  const upper = path.join(probeDir, name.toUpperCase());
  try {
    fs.writeFileSync(lower, "");
    return fs.existsSync(upper) ? "insensitive" : "sensitive";
  } catch {
    // Fall back to platform default when probing is not possible.
    return process.platform === "darwin" || process.platform === "win32"
      ? "insensitive"
      : "sensitive";
  } finally {
    try {
      fs.unlinkSync(lower);
    } catch {
      /* ignore */
    }
  }
}

function safeIsDir(p: string): boolean {
  try {
    return fs.statSync(p).isDirectory();
  } catch {
    return false;
  }
}

export function resolveCasePolicy(
  policy: CasePolicy,
  probeDir: string
): "sensitive" | "insensitive" {
  return policy === "auto" ? detectCaseSensitivity(probeDir) : policy;
}

/** Case-fold a path for comparison according to the effective policy. */
export function foldCase(
  p: string,
  policy: "sensitive" | "insensitive"
): string {
  return policy === "insensitive" ? p.toLowerCase() : p;
}

/**
 * True when `child` equals `dir` or lives underneath it, comparing
 * lexically on normalized absolute paths under the given case policy.
 */
export function isWithinDir(
  child: string,
  dir: string,
  policy: "sensitive" | "insensitive"
): boolean {
  const c = foldCase(path.normalize(child), policy);
  const d = foldCase(path.normalize(dir), policy);
  if (c === d) return true;
  const prefix = d.endsWith(path.sep) ? d : d + path.sep;
  return c.startsWith(prefix);
}
