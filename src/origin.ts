/**
 * Origin / URL helpers shared by the policy compiler and the adjudicator.
 *
 * Everything here is pure and DOM-free so the same decision logic can run in
 * the embedding shell, inside a simulated fence, or under the Node test runner.
 */

/** Opaque origins (data:, javascript:, sandboxed documents) render as "null". */
export const OPAQUE_ORIGIN = 'null';

/**
 * Serialize an origin the same way `new URL(...).origin` does.
 *
 * Returns {@link OPAQUE_ORIGIN} for opaque origins and throws `TypeError` for
 * inputs that cannot be parsed as absolute URLs.
 */
export function parseOrigin(rawOrigin: string): string {
  const origin = new URL(rawOrigin).origin;
  if (origin === OPAQUE_ORIGIN) {
    return OPAQUE_ORIGIN;
  }
  return origin;
}

/**
 * Resolve a possibly relative request URL against the fence document base.
 *
 * Throws `TypeError` when the result cannot be parsed (mirroring `fetch`,
 * which rejects unparseable URLs).
 */
export function resolveUrl(input: string, baseUrl: string): URL {
  return new URL(input, baseUrl);
}

/** Standard same-origin comparison on serialized origins. */
export function isSameOrigin(a: string, b: string): boolean {
  return a === b && a !== OPAQUE_ORIGIN;
}
