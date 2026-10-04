import * as fs from "fs";
import * as path from "path";
import { AccessRecord, SymlinkHop } from "./types";

const MAX_SYMLINK_HOPS = 40;

/**
 * Resolve an absolute path component by component, recording every symlink
 * hop so callers can see exactly where a path escapes its expected tree.
 *
 * Unlike fs.realpath, this never throws on missing paths: it reports the
 * first missing component and the chain accumulated up to that point.
 */
export function resolveChain(absPath: string): Omit<AccessRecord, "op" | "path"> {
  const hops: SymlinkHop[] = [];
  let segments = splitSegments(absPath);

  // Walk segments, rebuilding the remaining queue whenever a symlink
  // redirects us elsewhere in the tree.
  let index = 0;
  let current = path.parse(absPath).root;
  let hopCount = 0;

  while (index < segments.length) {
    current = path.join(current, segments[index]);
    index += 1;

    let st: fs.Stats;
    try {
      st = fs.lstatSync(current);
    } catch (err) {
      const code = (err as NodeJS.ErrnoException).code;
      if (code === "ENOENT" || code === "ENOTDIR") {
        return {
          chain: hops,
          final: null,
          exists: false,
          isDirectory: false,
          missingAt: current,
        };
      }
      return {
        chain: hops,
        final: null,
        exists: false,
        isDirectory: false,
        error: code ?? String(err),
      };
    }

    if (!st.isSymbolicLink()) continue;

    hopCount += 1;
    if (hopCount > MAX_SYMLINK_HOPS) {
      return {
        chain: hops,
        final: null,
        exists: false,
        isDirectory: false,
        error: "ELOOP",
      };
    }

    const target = fs.readlinkSync(current);
    const resolved = path.normalize(
      path.isAbsolute(target) ? target : path.join(path.dirname(current), target)
    );
    hops.push({ at: current, target, resolved });

    // Restart traversal from the link target, keeping the not-yet-walked
    // remainder of the original path.
    segments = splitSegments(resolved).concat(segments.slice(index));
    index = 0;
    current = path.parse(resolved).root;
  }

  let isDirectory = false;
  try {
    isDirectory = fs.statSync(current).isDirectory();
  } catch {
    // current exists per lstat above; stat failure is non-fatal here.
  }

  return { chain: hops, final: current, exists: true, isDirectory };
}

function splitSegments(absPath: string): string[] {
  const parsed = path.parse(path.normalize(absPath));
  const rel = path.relative(parsed.root, path.normalize(absPath));
  return rel === "" ? [] : rel.split(path.sep);
}
