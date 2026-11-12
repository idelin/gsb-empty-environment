export const OPAQUE_ORIGIN = "opaque";

/**
 * Normalize a serialized origin. The special strings `"opaque"` and `"null"`
 * both represent an opaque origin, matching the HTML serialization of an
 * opaque origin.
 */
export function normalizeOrigin(input: string, label = "origin"): string {
  const trimmed = input.trim();
  if (trimmed.length === 0) {
    throw new TypeError(`${label} must not be empty`);
  }

  if (trimmed === OPAQUE_ORIGIN || trimmed === "null") {
    return OPAQUE_ORIGIN;
  }

  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    throw new TypeError(
      `${label} must be an absolute URL or "opaque": received ${JSON.stringify(input)}`,
    );
  }

  return url.origin === "null" ? OPAQUE_ORIGIN : url.origin;
}

/**
 * Resolve a frame URL. `about:blank` inherits the parent's effective origin.
 * Relative URLs resolve against the parent's origin; if the parent is opaque,
 * a relative URL cannot produce a concrete origin.
 */
export function resolveFrameOrigin(
  src: string,
  parentOrigin: string,
): string {
  const trimmed = src.trim();
  if (trimmed.length === 0 || trimmed === "about:blank") {
    return parentOrigin;
  }
  if (trimmed === "about:srcdoc") {
    return parentOrigin;
  }

  try {
    const base =
      parentOrigin === OPAQUE_ORIGIN ? undefined : parentOrigin;
    const url = new URL(trimmed, base);
    return url.origin === "null" ? OPAQUE_ORIGIN : url.origin;
  } catch {
    return OPAQUE_ORIGIN;
  }
}

export function isSameOrigin(left: string, right: string): boolean {
  return (
    left !== OPAQUE_ORIGIN &&
    right !== OPAQUE_ORIGIN &&
    left === right
  );
}
