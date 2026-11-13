import type { ResourceKind } from "./types.ts";

const SECURE_SCHEMES = new Set(["https:", "wss:", "data:", "blob:", "file:"]);
const PLAINTEXT_SCHEMES = new Set(["http:", "ws:"]);

/** Resource kinds that can execute code or exfiltrate data and are blocked outright. */
const ACTIVE_KINDS: ReadonlySet<ResourceKind> = new Set<ResourceKind>([
  "script",
  "xhr",
  "fetch",
  "module",
  "websocket",
]);

/**
 * Passive kinds that receive one upgrade attempt rather than an immediate
 * block. If the upgraded URL fails the caller must treat the resource as
 * failed instead of falling back to plaintext.
 */
const UPGRADEABLE_KINDS: ReadonlySet<ResourceKind> = new Set<ResourceKind>([
  "image",
  "audiovideo",
  "style",
  "font",
]);

/**
 * Resolve a possibly relative resource URL against the embedding document.
 * Throws TypeError on malformed input, matching the URL constructor.
 */
export function resolveUrl(resourceUrl: string | URL, base?: string | URL): URL {
  if (base === undefined) {
    return new URL(resourceUrl);
  }
  const baseUrl = base instanceof URL ? base : new URL(base);
  return new URL(resourceUrl, baseUrl);
}

export function isSecureScheme(url: URL): boolean {
  return SECURE_SCHEMES.has(url.protocol);
}

export function isPlaintextScheme(url: URL): boolean {
  return PLAINTEXT_SCHEMES.has(url.protocol);
}

export function isActiveKind(kind: ResourceKind): boolean {
  return ACTIVE_KINDS.has(kind);
}

export function isUpgradeableKind(kind: ResourceKind): boolean {
  return UPGRADEABLE_KINDS.has(kind);
}

/**
 * Upgrade an insecure URL to its secure counterpart:
 * http -> https, ws -> wss. Non-plaintext URLs are returned unchanged.
 */
export function upgradeToSecure(url: URL): URL {
  if (url.protocol === "http:") {
    const upgraded = new URL(url.href);
    upgraded.protocol = "https:";
    return upgraded;
  }
  if (url.protocol === "ws:") {
    const upgraded = new URL(url.href);
    upgraded.protocol = "wss:";
    return upgraded;
  }
  return url;
}

/** Infer whether a document URL represents a secure context. */
export function inferSecureContext(documentUrl?: string | URL): boolean {
  if (documentUrl === undefined) {
    return true;
  }
  const doc = documentUrl instanceof URL ? documentUrl : new URL(documentUrl);
  return doc.protocol === "https:" || doc.protocol === "wss:";
}
