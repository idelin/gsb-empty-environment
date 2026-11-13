const ALLOWED_ORIGIN_PROTOCOLS = new Set(["http:", "https:"]);

export class InvalidOriginError extends Error {
  constructor(value: unknown) {
    super(`Invalid origin: ${String(value)}`);
    this.name = "InvalidOriginError";
  }
}

/**
 * Returns the canonical serialization of an HTTP(S) origin.
 *
 * Origin equality is tuple equality (scheme, host, port), never a string
 * prefix/suffix check. The WHATWG URL parser normalizes default ports,
 * hostname casing, and Internationalized Domain Names for us.
 */
export function normalizeOrigin(value: string | URL): string {
  let url: URL;

  try {
    url = typeof value === "string" ? new URL(value) : new URL(value.href);
  } catch {
    throw new InvalidOriginError(value);
  }

  if (!ALLOWED_ORIGIN_PROTOCOLS.has(url.protocol)) {
    throw new InvalidOriginError(value);
  }

  if (url.username !== "" || url.password !== "") {
    throw new InvalidOriginError(value);
  }

  return url.origin;
}

export function tryNormalizeOrigin(value: unknown): string | null {
  if (typeof value !== "string") {
    return null;
  }

  try {
    return normalizeOrigin(value);
  } catch {
    return null;
  }
}

export function originsEqual(left: string | URL, right: string | URL): boolean {
  const normalizedLeft = tryNormalizeOrigin(
    typeof left === "string" ? left : left.href,
  );
  const normalizedRight = tryNormalizeOrigin(
    typeof right === "string" ? right : right.href,
  );

  return (
    normalizedLeft !== null &&
    normalizedRight !== null &&
    normalizedLeft === normalizedRight
  );
}
