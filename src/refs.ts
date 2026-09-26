import {
  deepClone,
  getIn,
  isPlainObject,
  MISSING,
  pathToPointer,
  pointerToPath,
} from "./json.ts";
import type { JsonObject, JsonValue, Path } from "./json.ts";
import { RefCycleError, RefResolutionError } from "./errors.ts";

/**
 * Pure cross-field reference: a node of the exact shape `{ "$ref": "#/a/b" }`.
 * The referenced value is substituted wholesale; references are side-effect
 * free and may chain (a referenced subtree may itself contain references).
 */
export function isRefNode(v: unknown): v is { $ref: string } {
  return (
    isPlainObject(v) && Object.keys(v).length === 1 && typeof v.$ref === "string"
  );
}

/** Parses "#/a/b" into a path; returns null for malformed references. */
export function parseRefTarget(ref: string): Path | null {
  if (!ref.startsWith("#")) return null;
  const pointer = ref.slice(1);
  if (pointer !== "" && !pointer.startsWith("/")) return null;
  try {
    return pointerToPath(pointer);
  } catch {
    return null;
  }
}

/**
 * Compiles the reference dependency graph of a merged (unresolved) tree.
 * Returns [refLocationPointer, targetPointer] pairs.
 */
export function findRefs(root: JsonValue, base: Path): Array<[string, string]> {
  const out: Array<[string, string]> = [];
  const walk = (node: JsonValue, path: Path): void => {
    if (isRefNode(node)) {
      const target = parseRefTarget(node.$ref);
      if (target === null) {
        throw new RefResolutionError(
          `malformed reference ${JSON.stringify(node.$ref)}`,
          path,
        );
      }
      out.push([pathToPointer(path), pathToPointer(target)]);
      return;
    }
    if (Array.isArray(node)) {
      node.forEach((v, i) => walk(v, [...path, String(i)]));
    } else if (isPlainObject(node)) {
      for (const [k, v] of Object.entries(node)) walk(v, [...path, k]);
    }
  };
  walk(root, base);
  return out;
}

/**
 * Resolves references against the merged raw tree. Detects cycles via the
 * resolution stack and reports the full pointer chain. Resolved values are
 * cached per reference location and per target subtree so the incremental
 * hot-update path can invalidate exactly the stale entries.
 */
export class RefResolver {
  private readonly stack: string[] = [];
  private readonly raw: JsonValue;
  private readonly resolvedRefs: Map<string, JsonValue>;
  private readonly resolvedTargets: Map<string, JsonValue>;

  constructor(
    raw: JsonValue,
    resolvedRefs: Map<string, JsonValue>,
    resolvedTargets: Map<string, JsonValue>,
  ) {
    this.raw = raw;
    this.resolvedRefs = resolvedRefs;
    this.resolvedTargets = resolvedTargets;
  }

  resolveSubtree(node: JsonValue, path: Path): JsonValue {
    if (isRefNode(node)) {
      const target = parseRefTarget(node.$ref);
      if (target === null) {
        throw new RefResolutionError(
          `malformed reference ${JSON.stringify(node.$ref)}`,
          path,
        );
      }
      return this.resolveRef(pathToPointer(path), target, path);
    }
    if (Array.isArray(node)) {
      return node.map((v, i) => this.resolveSubtree(v, [...path, String(i)]));
    }
    if (isPlainObject(node)) {
      const out: JsonObject = {};
      for (const [k, v] of Object.entries(node)) {
        out[k] = this.resolveSubtree(v, [...path, k]);
      }
      return out;
    }
    return node;
  }

  private resolveRef(pointer: string, target: Path, refPath: Path): JsonValue {
    const cached = this.resolvedRefs.get(pointer);
    if (cached !== undefined) return deepClone(cached);
    const at = this.stack.indexOf(pointer);
    if (at >= 0) {
      throw new RefCycleError([...this.stack.slice(at), pointer]);
    }
    this.stack.push(pointer);
    try {
      const targetPointer = pathToPointer(target);
      let resolvedTarget = this.resolvedTargets.get(targetPointer);
      if (resolvedTarget === undefined) {
        const rawTarget = getIn(this.raw, target);
        if (rawTarget === MISSING) {
          throw new RefResolutionError(
            `reference target ${JSON.stringify("#" + targetPointer)} does not exist`,
            refPath,
          );
        }
        resolvedTarget = this.resolveSubtree(rawTarget, target);
        this.resolvedTargets.set(targetPointer, resolvedTarget);
      }
      const out = deepClone(resolvedTarget);
      this.resolvedRefs.set(pointer, out);
      return out;
    } finally {
      this.stack.pop();
    }
  }
}
