/**
 * Connection identity and session binding types.
 *
 * The pool key is deliberately rich: IP+certificate alone is NOT sufficient,
 * because a malicious origin sharing a connection must never observe or
 * occupy another origin's session/datagram context.
 */

/** Network partition key (e.g. top-level site + nonce), prevents cross-partition reuse. */
export interface NetworkPartitionKey {
  topLevelSite: string;
  nonce?: string;
}

/** Ordered proxy chain; empty array means a direct connection. */
export type ProxyChain = readonly string[];

/** Verified server certificate identity (post-validation, not raw bytes). */
export interface CertIdentity {
  /** Fingerprint of the validated certificate chain. */
  chainFingerprint: string;
  /** Verified DNS names (SAN dNSName entries, lowercased, wildcards kept). */
  verifiedNames: readonly string[];
  /** Expiry (ms since epoch); a cert update past this freezes the connection. */
  validUntil: number;
}

/** ECH (Encrypted Client Hello) negotiation outcome. */
export type EchResult =
  | { readonly kind: "none" }
  | { readonly kind: "accepted"; readonly publicName: string }
  | { readonly kind: "rejected" };

/** Remote endpoint of the QUIC connection. */
export interface RemoteEndpoint {
  ip: string;
  port: number;
}

/** All inputs that define the identity of a pooled HTTP/3 connection. */
export interface ConnectionKeyParts {
  partitionKey: NetworkPartitionKey;
  proxyChain: ProxyChain;
  cert: CertIdentity;
  ech: EchResult;
  alpn: string;
  remote: RemoteEndpoint;
}

/** Canonical, serialization-stable connection key. */
export type ConnectionKey = string;

/** Server-advertised ORIGIN frame (RFC 9412 style) content. */
export interface OriginFrame {
  /**
   * True only if the frame was received over the authenticated connection
   * and every entry was validated by the HTTP/3 layer. A forged or
   * unverified frame must carry false and is never honored.
   */
  validated: boolean;
  /** Serialized origins (scheme://host[:port]) the server claims to serve. */
  origins: readonly string[];
}

/** Parameters supplied by the caller when opening a CONNECT session. */
export interface SessionRequest {
  /** Requesting origin, e.g. "https://app.example.com". */
  origin: string;
  /** WebTransport URL; canonicalized before binding. */
  url: string;
  /** Draft/version tokens offered to the server. */
  offeredVersions: readonly string[];
  /**
   * Ignored. Session IDs are allocated by the pool per connection and a
   * caller-supplied ID is never honored, so one origin cannot squat on or
   * replay another origin's session ID.
   */
  requestedSessionId?: number;
}

/** Per-session negotiated quotas. */
export interface SessionQuotas {
  maxStreams: number;
  maxDatagrams: number;
  usedStreams: number;
  usedDatagrams: number;
}

/** Connection-wide congestion statistics. Shared across origins by design. */
export interface CongestionStats {
  bytesInFlight: number;
  smoothedRttMs: number;
  congestionWindow: number;
}

export type ConnectionState = "active" | "draining" | "frozen" | "closed";

export type FreezeReason =
  | "goaway"
  | "cert-update"
  | "network-change"
  | "zero-rtt-rejected";

export type DatagramVerdict =
  | "delivered"
  | "dropped-unknown-generation"
  | "dropped-unknown-session"
  | "dropped-closed-session";

/** Stable, key-order-independent serialization of connection identity. */
export function canonicalizeKey(parts: ConnectionKeyParts): ConnectionKey {
  const sortedNames = [...parts.cert.verifiedNames].sort();
  return JSON.stringify({
    npk: [parts.partitionKey.topLevelSite, parts.partitionKey.nonce ?? ""],
    proxy: [...parts.proxyChain],
    cert: [parts.cert.chainFingerprint, sortedNames],
    ech:
      parts.ech.kind === "accepted"
        ? ["accepted", parts.ech.publicName]
        : [parts.ech.kind],
    alpn: parts.alpn,
    remote: [parts.remote.ip, parts.remote.port],
  });
}

/** RFC 6454 origin serialization, normalized. Returns null if invalid. */
export function canonicalizeOrigin(origin: string): string | null {
  let u: URL;
  try {
    u = new URL(origin);
  } catch {
    return null;
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") return null;
  if (u.username || u.password) return null;
  const host = u.hostname.toLowerCase();
  const defaultPort = u.protocol === "https:" ? "443" : "80";
  const port = u.port === "" || u.port === defaultPort ? "" : `:${u.port}`;
  return `${u.protocol}//${host}${port}`;
}

/** Canonical WebTransport URL: lowercase scheme/host, strip default port. */
export function canonicalizeUrl(url: string): string | null {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return null;
  }
  if (u.protocol !== "https:") return null;
  u.hostname = u.hostname.toLowerCase();
  if (u.port === "443") u.port = "";
  u.hash = "";
  return u.toString();
}

/** Certificate name coverage, including one-label wildcards. */
export function certCoversHost(cert: CertIdentity, host: string): boolean {
  const h = host.toLowerCase();
  for (const name of cert.verifiedNames) {
    const n = name.toLowerCase();
    if (n === h) return true;
    if (n.startsWith("*.")) {
      const suffix = n.slice(1); // ".example.com"
      if (h.endsWith(suffix) && h.indexOf(".") === h.length - suffix.length) {
        return true;
      }
    }
  }
  return false;
}

/** Origin of a canonical https URL ("https://host[:port]"). */
export function originOfUrl(canonicalUrl: string): string | null {
  try {
    return canonicalizeOrigin(new URL(canonicalUrl).origin);
  } catch {
    return null;
  }
}
