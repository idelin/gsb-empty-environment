export interface RelyingParty {
  /** Registered wtrealm value. Also used as the token audience. */
  realm: string;
  /** Registered reply addresses. A wreply must match one of these on
   *  protocol/host/port exactly and stay under its path prefix. */
  replyAddresses: string[];
}

export class WreplyValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "WreplyValidationError";
  }
}

function fail(reason: string): never {
  throw new WreplyValidationError(reason);
}

/** Fully decode a percent-encoded path, rejecting malformed input. */
function fullyDecode(value: string): string {
  let current = value;
  for (let round = 0; round < 8; round++) {
    let decoded: string;
    try {
      decoded = decodeURIComponent(current);
    } catch {
      fail("wreply contains malformed percent-encoding");
    }
    if (decoded === current) return decoded;
    current = decoded;
  }
  fail("wreply path is excessively nested in percent-encoding");
}

/** Reject paths that can escape the registered prefix once decoded. */
function assertSafePathname(rawPathname: string): void {
  const decoded = fullyDecode(rawPathname);
  if (decoded.includes("\\")) {
    fail("wreply path must not contain backslashes");
  }
  if (/[\u0000-\u001f]/.test(decoded)) {
    fail("wreply path must not contain control characters");
  }
  for (const segment of decoded.split("/")) {
    if (segment === "..") {
      fail("wreply path must not contain '..' traversal segments");
    }
  }
}

/** True when `path` equals `prefix` or descends from it on a segment boundary. */
function isPathWithinPrefix(path: string, prefix: string): boolean {
  if (path === prefix) return true;
  const base = prefix.endsWith("/") ? prefix : `${prefix}/`;
  return path.startsWith(base);
}

/**
 * Validate a wreply parameter against a relying party's registered reply
 * addresses. Returns the validated URL on success, throws
 * WreplyValidationError otherwise.
 */
export function validateWreply(wreply: string, party: RelyingParty): URL {
  if (typeof wreply !== "string" || wreply.length === 0) {
    fail("wreply is required");
  }
  // Browsers treat '\' as '/', so reject it outright before parsing.
  if (wreply.includes("\\") || /%5c/i.test(wreply)) {
    fail("wreply must not contain backslashes");
  }

  let target: URL;
  try {
    target = new URL(wreply);
  } catch {
    fail("wreply is not a valid absolute URL");
  }
  if (target.protocol !== "https:" && target.protocol !== "http:") {
    fail(`wreply scheme '${target.protocol}' is not allowed`);
  }
  if (target.username !== "" || target.password !== "") {
    fail("wreply must not contain userinfo");
  }
  if (target.hostname === "") {
    fail("wreply must have a host");
  }
  assertSafePathname(target.pathname);

  for (const registered of party.replyAddresses) {
    let base: URL;
    try {
      base = new URL(registered);
    } catch {
      continue;
    }
    if (
      base.protocol === target.protocol &&
      base.hostname === target.hostname &&
      base.port === target.port &&
      isPathWithinPrefix(target.pathname, base.pathname)
    ) {
      return target;
    }
  }
  fail("wreply does not match any registered reply address");
}

/** The token audience is always the registered realm value, never request input. */
export function resolveAudience(party: RelyingParty): string {
  return party.realm;
}
