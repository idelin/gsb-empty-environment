import { HARD_LIMITS } from "./security.ts";

export interface StructuralContainer {
  readonly children: ArrayLike<StructuralNode>;
}

export interface StructuralNode extends StructuralContainer {
  readonly localName: string;
}

export type StructuralSelection =
  | Readonly<{
      ok: true;
      element: StructuralNode;
      path: readonly string[];
    }>
  | Readonly<{
      ok: false;
      reason: "invalid_path" | "missing" | "ambiguous";
      depth: number;
    }>;

const TAG_PATTERN = /^[a-z][a-z0-9-]*$/;

function validatePath(path: unknown): readonly string[] {
  if (!Array.isArray(path)) {
    return Object.freeze([]);
  }
  if (path.length === 0 || path.length > HARD_LIMITS.maxPathDepth) {
    return Object.freeze([]);
  }

  const output: string[] = [];
  for (let index = 0; index < path.length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(path, String(index));
    if (
      descriptor === undefined ||
      !("value" in descriptor) ||
      typeof descriptor.value !== "string"
    ) {
      return Object.freeze([]);
    }
    const tag = descriptor.value;
    if (
      tag.length > HARD_LIMITS.maxPathTagLength ||
      !TAG_PATTERN.test(tag)
    ) {
      return Object.freeze([]);
    }
    output.push(tag);
  }
  return Object.freeze(output);
}

/**
 * Walks direct-child structure only: each path step is an element tag chosen
 * by fixed position. IDs, classes, names, attributes and CSS selectors from
 * the page are never inputs. Multiple matching siblings make the path
 * ambiguous and are rejected instead of choosing one opportunistically.
 */
export function selectUniqueStructuralPath(
  root: StructuralContainer,
  path: readonly string[],
): StructuralSelection {
  const tags = validatePath(path);
  if (tags.length === 0) {
    return Object.freeze({
      ok: false as const,
      reason: "invalid_path" as const,
      depth: 0,
    });
  }

  let current: StructuralNode = root as StructuralNode;
  for (let depth = 0; depth < tags.length; depth += 1) {
    const wanted = tags[depth];
    const children = current.children;
    if (
      typeof children !== "object" ||
      children === null ||
      typeof children.length !== "number"
    ) {
      return Object.freeze({
        ok: false as const,
        reason: "missing" as const,
        depth,
      });
    }

    const matches: StructuralNode[] = [];
    for (let index = 0; index < children.length; index += 1) {
      const child = children[index];
      if (
        typeof child !== "object" ||
        child === null ||
        typeof child.localName !== "string"
      ) {
        continue;
      }
      if (child.localName.toLowerCase() === wanted) {
        matches.push(child);
      }
    }

    if (matches.length === 0) {
      return Object.freeze({
        ok: false as const,
        reason: "missing" as const,
        depth,
      });
    }
    if (matches.length !== 1) {
      return Object.freeze({
        ok: false as const,
        reason: "ambiguous" as const,
        depth,
      });
    }
    current = matches[0];
  }

  return Object.freeze({
    ok: true as const,
    element: current,
    path: tags,
  });
}

/**
 * The only page element this component may use is an optional, non-privileged
 * UI anchor at a fixed structural location. It cannot change policy or select
 * a communication destination.
 */
export function findPageAnchor(
  documentLike: StructuralContainer,
): StructuralSelection {
  return selectUniqueStructuralPath(documentLike, ["html", "body", "main"]);
}
