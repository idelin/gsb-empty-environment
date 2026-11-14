import {
  InvalidConnectionDescriptorError,
  InvalidSessionEstablishmentError,
  InvalidSessionRequestError,
  WebTransportPoolError,
  type WebTransportPoolErrorCode,
} from "./errors.ts";

export type NetworkPartitionKey = string;
export type QuicGeneration = number;
export type Alpn = string;

export interface RemoteEndpoint {
  readonly address: string;
  readonly port: number;
  readonly family?: "ipv4" | "ipv6";
}

export interface CanonicalRemoteEndpoint {
  readonly address: string;
  readonly port: number;
  readonly family: "ipv4" | "ipv6";
}

export interface VerifiedServerCertificate {
  /**
   * Stable identity of the verified certificate (normally the SHA-256
   * fingerprint of the leaf certificate or the verified certificate chain).
   */
  readonly fingerprintSha256: string;
  /** DNS SANs from the certificate that the TLS stack actually verified. */
  readonly dnsNames: readonly string[];
  /** IP SANs from the certificate that the TLS stack actually verified. */
  readonly ipAddresses: readonly string[];
  readonly notBefore?: number;
  readonly notAfter?: number;
}

/** Alias used by network stacks that call the verified identity a certificate identity. */
export type VerifiedCertificateIdentity = VerifiedServerCertificate;

export type EchResult =
  | { readonly status: "not_offered" }
  | { readonly status: "accepted"; readonly configId?: number }
  | { readonly status: "rejected"; readonly retryConfigIds?: readonly number[] };

export interface CanonicalEchResult {
  readonly status: "not_offered" | "accepted" | "rejected";
  readonly configId?: number;
  readonly retryConfigIds?: readonly number[];
}

export interface ConnectionDescriptor {
  readonly networkPartitionKey: NetworkPartitionKey;
  readonly proxyChain: readonly string[];
  readonly certificate: VerifiedServerCertificate;
  readonly ech: EchResult;
  readonly alpn: Alpn;
  readonly remoteEndpoint: RemoteEndpoint;
  /**
   * Monotonic QUIC generation for this transport tuple. If omitted the pool
   * assigns one. Generations must be unique within a pool so late datagrams
   * can be routed by (generation, session ID) without ambiguity.
   */
  readonly quicGeneration?: QuicGeneration;
  /** Origin whose verified handshake established the connection. */
  readonly initialOrigin: string;
}

export interface ConnectionIdentity {
  readonly transportKey: string;
  readonly networkPartitionKey: NetworkPartitionKey;
  readonly proxyChain: readonly string[];
  readonly certificate: VerifiedServerCertificate;
  readonly ech: CanonicalEchResult;
  readonly alpn: Alpn;
  readonly remoteEndpoint: CanonicalRemoteEndpoint;
  readonly quicGeneration: QuicGeneration;
}

export interface ConnectionHandle {
  readonly id: string;
  readonly generation: QuicGeneration;
  readonly initialOrigin: string;
  readonly identity: ConnectionIdentity;
}

export type ConnectionState = "active" | "draining" | "superseded" | "closed";

export type FreezeReason =
  | "goaway"
  | "certificate_update"
  | "network_switch"
  | "zero_rtt_rejection"
  | "superseded"
  | "closed";

export interface OriginFrameInput {
  readonly generation: QuicGeneration;
  readonly origins: readonly string[];
  /**
   * The browser network stack must only pass frames received on the
   * authenticated HTTP/3 control stream. Application JavaScript never has a
   * path to this API.
   */
  readonly source: "http3-control-stream";
}

export interface SessionEstablishment {
  readonly sessionId: string;
  /** Server-returned WebTransport draft, e.g. "draft-13". */
  readonly draft: string;
  /** Server-returned protocol version, e.g. "13". */
  readonly version: string;
  readonly streamLimit?: number;
  readonly datagramLimit?: number;
  /** Aliases accepted from HTTP/3 stacks that expose max* counters. */
  readonly maxStreams?: number;
  readonly maxDatagrams?: number;
  readonly earlyData?: boolean;
}

export interface CreateSessionInput {
  readonly connectionId: string;
  readonly url: string;
  /**
   * Optional assertion by the caller. It must match the origin derived from
   * the canonical URL; it is never used to bypass authorization.
   */
  readonly origin?: string;
  readonly earlyData?: boolean;
  /** Server response supplied by the browser network stack. */
  readonly establishment?: SessionEstablishment;
}

export interface SessionHandle {
  readonly id: string;
  readonly connectionId: string;
  readonly quicGeneration: QuicGeneration;
  readonly origin: string;
  readonly canonicalUrl: string;
  readonly sessionId: string;
  readonly draft: string;
  readonly version: string;
}

export interface SessionSummary {
  readonly state: "open" | "closed";
  readonly origin: string;
  readonly canonicalUrl: string;
  readonly sessionId: string;
  readonly draft: string;
  readonly version: string;
  readonly earlyData: boolean;
  readonly streamQuota: {
    readonly limit: number;
    readonly used: number;
  };
  readonly datagramQuota: {
    readonly limit: number;
    readonly used: number;
  };
  readonly streamIds: readonly number[];
  readonly queuedDatagrams: number;
}

export interface ConnectionSummary {
  readonly id: string;
  readonly generation: QuicGeneration;
  readonly state: ConnectionState;
  readonly freezeReasons: readonly FreezeReason[];
  readonly generationCurrent: boolean;
  readonly activeSessions: number;
  readonly authorizedOrigins: readonly string[];
  readonly originFrameAccepted: boolean;
  readonly zeroRttRejected: boolean;
  readonly congestionSamples: number;
}

export interface CongestionSample {
  readonly smoothedRttMs?: number;
  readonly packetsInFlight?: number;
  readonly congestionWindowBytes?: number;
  readonly lostPackets?: number;
  readonly sentPackets?: number;
}

export interface CongestionStats {
  readonly samples: number;
  readonly last: Readonly<CongestionSample>;
}

export interface DatagramEnvelope {
  readonly quicGeneration: QuicGeneration;
  readonly sessionId: string;
  readonly payload: Uint8Array;
}

export interface StreamGrant {
  readonly quicGeneration: QuicGeneration;
  readonly sessionId: string;
  readonly streamId: number;
}

export interface OriginFrameAcceptance {
  readonly accepted: readonly string[];
  readonly authorizedOrigins: readonly string[];
}

const CONTROL_CHARACTERS = /[\u0000-\u001f\u007f]/;
const IPV4 = /^(?:\d{1,3}\.){3}\d{1,3}$/;

export function stableKey(parts: readonly string[]): string {
  return parts.map((part) => `${part.length}:${part}`).join("|");
}

function fail(code: WebTransportPoolErrorCode, message: string): never {
  throw new WebTransportPoolError(code, message);
}

function requireNonEmptyString(value: unknown, code: WebTransportPoolErrorCode, label: string): string {
  if (typeof value !== "string" || value.trim().length === 0) {
    fail(code, `${label} must be a non-empty string`);
  }
  const normalized = value.trim();
  if (CONTROL_CHARACTERS.test(normalized)) {
    fail(code, `${label} must not contain control characters`);
  }
  return normalized;
}

function requireSafeInteger(
  value: unknown,
  code: WebTransportPoolErrorCode,
  label: string,
  minimum: number,
): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < minimum) {
    fail(code, `${label} must be a safe integer >= ${minimum}`);
  }
  return value;
}

function normalizeAsciiHost(host: string): string {
  const withoutBrackets = host.replace(/^\[/, "").replace(/\]$/, "").toLowerCase();
  try {
    return new URL(`https://${withoutBrackets.includes(":") ? `[${withoutBrackets}]` : withoutBrackets}`)
      .hostname.replace(/^\[/, "")
      .replace(/\]$/, "");
  } catch {
    return withoutBrackets;
  }
}

export function isIpLiteral(host: string): boolean {
  const value = host.replace(/^\[/, "").replace(/\]$/, "");
  if (IPV4.test(value)) {
    return value.split(".").every((part) => Number(part) <= 255);
  }
  if (!value.includes(":")) {
    return false;
  }
  try {
    const url = new URL(`https://[${value}]`);
    return url.hostname.startsWith("[") && url.hostname.endsWith("]");
  } catch {
    return false;
  }
}

export function canonicalizeOrigin(
  raw: string,
  code: WebTransportPoolErrorCode = "invalid_connection_descriptor",
): string {
  const value = requireNonEmptyString(raw, code, "origin");
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    fail(code, `invalid origin ${JSON.stringify(raw)}`);
  }
  if (url.protocol !== "https:") {
    fail(code, `origin must use https, got ${url.protocol}`);
  }
  if (url.username !== "" || url.password !== "") {
    fail(code, "origin must not contain userinfo");
  }
  if (url.pathname !== "/" || url.search !== "" || url.hash !== "") {
    fail(code, `origin must not contain a path, query, or fragment: ${JSON.stringify(raw)}`);
  }
  return url.origin;
}

export function canonicalizeSessionUrl(raw: string): { readonly url: string; readonly origin: string } {
  const value = requireNonEmptyString(raw, "invalid_session_request", "url");
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    fail("invalid_session_request", `invalid URL ${JSON.stringify(raw)}`);
  }
  if (url.protocol !== "https:") {
    fail("invalid_session_request", `WebTransport URL must use https, got ${url.protocol}`);
  }
  if (url.username !== "" || url.password !== "") {
    fail("invalid_session_request", "WebTransport URL must not contain userinfo");
  }
  url.hash = "";
  return Object.freeze({ url: url.href, origin: url.origin });
}

export function hostnameOfOrigin(origin: string): string {
  return new URL(origin).hostname.replace(/^\[/, "").replace(/\]$/, "").toLowerCase();
}

export function canonicalizeCertificate(
  certificate: VerifiedServerCertificate,
): VerifiedServerCertificate {
  if (certificate === null || typeof certificate !== "object") {
    throw new InvalidConnectionDescriptorError("certificate identity is required");
  }
  const fingerprint = requireNonEmptyString(
    certificate.fingerprintSha256,
    "invalid_connection_descriptor",
    "certificate fingerprint",
  );
  if (!Array.isArray(certificate.dnsNames) || !Array.isArray(certificate.ipAddresses)) {
    throw new InvalidConnectionDescriptorError(
      "certificate dnsNames and ipAddresses must be arrays of verified SANs",
    );
  }
  const dnsNames = [...new Set(certificate.dnsNames.map(normalizeDnsName))].filter(Boolean).sort();
  const ipAddresses = [
    ...new Set(
      certificate.ipAddresses.map((ip) =>
        normalizeAsciiHost(requireNonEmptyString(ip, "invalid_connection_descriptor", "IP SAN")),
      ),
    ),
  ].sort();
  validateOptionalTime(certificate.notBefore, "certificate notBefore");
  validateOptionalTime(certificate.notAfter, "certificate notAfter");
  return Object.freeze({
    fingerprintSha256: fingerprint,
    dnsNames: Object.freeze(dnsNames),
    ipAddresses: Object.freeze(ipAddresses),
    ...(certificate.notBefore === undefined ? {} : { notBefore: certificate.notBefore }),
    ...(certificate.notAfter === undefined ? {} : { notAfter: certificate.notAfter }),
  });
}

function validateOptionalTime(value: unknown, label: string): void {
  if (value !== undefined && (typeof value !== "number" || !Number.isFinite(value))) {
    throw new InvalidConnectionDescriptorError(`${label} must be a finite number when present`);
  }
}

function normalizeDnsName(raw: string): string {
  const value = requireNonEmptyString(raw, "invalid_connection_descriptor", "DNS SAN")
    .toLowerCase()
    .replace(/\.$/, "");
  if (value.startsWith("*.")) {
    return `*.${normalizeAsciiHost(value.slice(2).replace(/\.$/, ""))}`;
  }
  return normalizeAsciiHost(value);
}

export function certificateCovers(certificate: VerifiedServerCertificate, origin: string): boolean {
  const host = hostnameOfOrigin(origin);
  if (isIpLiteral(host)) {
    const canonicalIp = normalizeAsciiHost(host);
    return certificate.ipAddresses.some((ip) => normalizeAsciiHost(ip) === canonicalIp);
  }
  const hostname = normalizeDnsName(host);
  for (const rawPattern of certificate.dnsNames) {
    const pattern = normalizeDnsName(rawPattern);
    if (pattern === hostname) {
      return true;
    }
    if (!pattern.startsWith("*.")) {
      continue;
    }
    const suffix = pattern.slice(1);
    if (!hostname.endsWith(suffix)) {
      continue;
    }
    const label = hostname.slice(0, hostname.length - suffix.length);
    if (label.length > 0 && !label.includes(".")) {
      return true;
    }
  }
  return false;
}

export function canonicalizeEch(ech: EchResult): CanonicalEchResult {
  if (ech === null || typeof ech !== "object") {
    throw new InvalidConnectionDescriptorError("ECH result is required");
  }
  if (ech.status === "not_offered") {
    return Object.freeze({ status: "not_offered" });
  }
  if (ech.status === "accepted") {
    if (ech.configId !== undefined) {
      const configId = requireSafeInteger(
        ech.configId,
        "invalid_connection_descriptor",
        "ECH configId",
        0,
      );
      return Object.freeze({ status: "accepted", configId });
    }
    return Object.freeze({ status: "accepted" });
  }
  if (ech.status === "rejected") {
    if (ech.retryConfigIds !== undefined && !Array.isArray(ech.retryConfigIds)) {
      throw new InvalidConnectionDescriptorError("ECH retryConfigIds must be an array");
    }
    const retryConfigIds =
      ech.retryConfigIds === undefined
        ? undefined
        : Object.freeze(
            [...new Set(ech.retryConfigIds)].map((id) =>
              requireSafeInteger(id, "invalid_connection_descriptor", "ECH retry config ID", 0),
            ),
          ).sort((left, right) => left - right);
    return Object.freeze({
      status: "rejected",
      ...(retryConfigIds === undefined ? {} : { retryConfigIds }),
    });
  }
  throw new InvalidConnectionDescriptorError("ECH result status must be not_offered, accepted, or rejected");
}

export function canonicalizeProxyChain(proxyChain: readonly string[]): readonly string[] {
  if (!Array.isArray(proxyChain)) {
    throw new InvalidConnectionDescriptorError("proxyChain must be an array");
  }
  const hops = proxyChain.map((hop) => {
    const value = requireNonEmptyString(hop, "invalid_connection_descriptor", "proxy hop");
    if (value === "direct") {
      return "direct";
    }
    if (value.includes("://")) {
      try {
        const url = new URL(value);
        return url.href.replace(/\/$/, "");
      } catch {
        throw new InvalidConnectionDescriptorError(`invalid proxy hop ${JSON.stringify(hop)}`);
      }
    }
    return value;
  });
  return Object.freeze(hops);
}

export function canonicalizeAlpn(alpn: Alpn): Alpn {
  const value = requireNonEmptyString(alpn, "invalid_connection_descriptor", "ALPN");
  if (!/^h3(?:-\d+)?$/i.test(value)) {
    throw new InvalidConnectionDescriptorError(
      `WebTransport requires an h3-derived ALPN, got ${JSON.stringify(alpn)}`,
    );
  }
  return value;
}

export function canonicalizeRemoteEndpoint(endpoint: RemoteEndpoint): CanonicalRemoteEndpoint {
  if (endpoint === null || typeof endpoint !== "object") {
    throw new InvalidConnectionDescriptorError("remoteEndpoint is required");
  }
  const rawAddress = requireNonEmptyString(
    endpoint.address,
    "invalid_connection_descriptor",
    "remote endpoint address",
  );
  const address = normalizeAsciiHost(rawAddress);
  const inferred = isIpLiteral(address) ? (address.includes(":") ? "ipv6" : "ipv4") : undefined;
  if (inferred === undefined) {
    throw new InvalidConnectionDescriptorError("remote endpoint address must be an IP literal");
  }
  if (endpoint.family !== undefined && endpoint.family !== inferred) {
    throw new InvalidConnectionDescriptorError(
      `remote endpoint family ${endpoint.family} does not match address ${address}`,
    );
  }
  const port = requireSafeInteger(
    endpoint.port,
    "invalid_connection_descriptor",
    "remote endpoint port",
    1,
  );
  if (port > 65535) {
    throw new InvalidConnectionDescriptorError("remote endpoint port must be <= 65535");
  }
  return Object.freeze({ address, port, family: inferred });
}

export interface CanonicalConnectionDescriptor {
  readonly initialOrigin: string;
  readonly transportKey: string;
  readonly identityKey: string;
  readonly descriptorKey: string;
  readonly identity: ConnectionIdentity;
  readonly quicGeneration?: QuicGeneration;
}

export function canonicalizeConnectionDescriptor(
  descriptor: ConnectionDescriptor,
): CanonicalConnectionDescriptor {
  if (descriptor === null || typeof descriptor !== "object") {
    throw new InvalidConnectionDescriptorError("connection descriptor is required");
  }
  const networkPartitionKey = requireNonEmptyString(
    descriptor.networkPartitionKey,
    "invalid_connection_descriptor",
    "network partition key",
  );
  const proxyChain = canonicalizeProxyChain(descriptor.proxyChain);
  const certificate = canonicalizeCertificate(descriptor.certificate);
  const ech = canonicalizeEch(descriptor.ech);
  const alpn = canonicalizeAlpn(descriptor.alpn);
  const remoteEndpoint = canonicalizeRemoteEndpoint(descriptor.remoteEndpoint);
  const initialOrigin = canonicalizeOrigin(descriptor.initialOrigin);
  if (!certificateCovers(certificate, initialOrigin)) {
    throw new InvalidConnectionDescriptorError(
      `verified certificate does not cover the initial origin ${initialOrigin}`,
    );
  }

  const certificateKey = stableKey([
    certificate.fingerprintSha256,
    String(certificate.dnsNames.length),
    ...certificate.dnsNames,
    String(certificate.ipAddresses.length),
    ...certificate.ipAddresses,
  ]);
  const echKey =
    ech.status === "not_offered"
      ? "not_offered"
      : ech.status === "accepted"
        ? `accepted:${ech.configId ?? ""}`
        : `rejected:${(ech.retryConfigIds ?? []).join(",")}`;
  const transportKey = stableKey([
    networkPartitionKey,
    String(proxyChain.length),
    ...proxyChain,
    certificateKey,
    echKey,
    alpn,
    remoteEndpoint.family,
    remoteEndpoint.address,
    String(remoteEndpoint.port),
  ]);

  let quicGeneration: number | undefined;
  if (descriptor.quicGeneration !== undefined) {
    quicGeneration = requireSafeInteger(
      descriptor.quicGeneration,
      "invalid_connection_descriptor",
      "QUIC generation",
      0,
    );
  }

  const identityKey = stableKey([
    transportKey,
    quicGeneration === undefined ? "auto" : String(quicGeneration),
  ]);
  const descriptorKey = stableKey([identityKey, initialOrigin]);
  const identity: ConnectionIdentity = Object.freeze({
    transportKey,
    networkPartitionKey,
    proxyChain,
    certificate,
    ech,
    alpn,
    remoteEndpoint,
    quicGeneration: quicGeneration ?? 0,
  });

  return Object.freeze({
    initialOrigin,
    transportKey,
    identityKey,
    descriptorKey,
    identity,
    ...(quicGeneration === undefined ? {} : { quicGeneration }),
  });
}

export function canonicalizeEstablishment(result: SessionEstablishment): {
  readonly sessionId: string;
  readonly draft: string;
  readonly version: string;
  readonly streamLimit: number;
  readonly datagramLimit: number;
  readonly earlyData: boolean;
} {
  if (result === null || typeof result !== "object") {
    throw new InvalidSessionEstablishmentError("server session establishment result is required");
  }
  const sessionId = requireNonEmptyString(
    result.sessionId,
    "invalid_session_establishment",
    "session ID",
  );
  if (sessionId.length > 255) {
    throw new InvalidSessionEstablishmentError("session ID must be at most 255 characters");
  }
  const draft = requireNonEmptyString(
    result.draft,
    "invalid_session_establishment",
    "server draft",
  );
  const version = requireNonEmptyString(
    result.version,
    "invalid_session_establishment",
    "server version",
  );
  if (draft.length > 128 || version.length > 128) {
    throw new InvalidSessionEstablishmentError(
      "server draft and version must be at most 128 characters",
    );
  }
  const streamLimit = requireSafeInteger(
    result.streamLimit ?? result.maxStreams ?? 16,
    "invalid_session_establishment",
    "stream quota",
    0,
  );
  const datagramLimit = requireSafeInteger(
    result.datagramLimit ?? result.maxDatagrams ?? 256,
    "invalid_session_establishment",
    "datagram quota",
    0,
  );
  return Object.freeze({
    sessionId,
    draft,
    version,
    streamLimit,
    datagramLimit,
    earlyData: result.earlyData === true,
  });
}

export function copyPayload(payload: Uint8Array | string): Uint8Array {
  if (typeof payload === "string") {
    return new TextEncoder().encode(payload);
  }
  if (!(payload instanceof Uint8Array)) {
    throw new WebTransportPoolError(
      "invalid_session_request",
      "datagram payload must be a Uint8Array or string",
    );
  }
  return payload.slice();
}

export function assertCreateSessionInput(input: CreateSessionInput): void {
  if (input === null || typeof input !== "object") {
    throw new InvalidSessionRequestError("create session input is required");
  }
  requireNonEmptyString(input.connectionId, "invalid_session_request", "connectionId");
}
