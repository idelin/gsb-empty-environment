import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

/**
 * A small, framework-agnostic cookie component.
 *
 * The important security property is that a Cookie header alone does not carry
 * trustworthy Domain/Path/issuer metadata. Every value emitted by this module
 * therefore contains a signed envelope with that metadata. An attacker who
 * controls a sibling subdomain can write a cookie with the same name, but it
 * cannot produce a valid signature or become an active session.
 */

export type CookieSameSite = "Strict" | "Lax" | "None";
export type SameSite = CookieSameSite;

export type CookieRejectionReason =
  | "malformed"
  | "invalid-name"
  | "invalid-signature"
  | "name-mismatch"
  | "prefix"
  | "prefix-domain"
  | "prefix-path"
  | "prefix-secure"
  | "same-site-none-insecure"
  | "domain-mismatch"
  | "unowned-domain"
  | "host-only-issuer-mismatch"
  | "path-mismatch"
  | "insecure-transport"
  | "expired"
  | "untrusted-issuer"
  | "session-revoked";

export type CookieErrorCode =
  | "INVALID_OPTIONS"
  | "INVALID_NAME"
  | "INVALID_DOMAIN"
  | "INVALID_PATH"
  | "INVALID_SAME_SITE"
  | "PREFIX_DOMAIN"
  | "PREFIX_PATH"
  | "PREFIX_SECURE"
  | "SAME_SITE_NONE_INSECURE"
  | "ISSUER_MISMATCH"
  | "DOMAIN_NOT_OWNED"
  | "SESSION_ISSUER_MISMATCH"
  | "SESSION_NOT_FOUND"
  | "SESSION_REVOKED";

export class CookieError extends Error {
  code: CookieErrorCode;
  detail: unknown;

  constructor(code: CookieErrorCode, message: string, detail?: unknown) {
    super(message);
    this.name = "CookieError";
    this.code = code;
    this.detail = detail;
  }
}

export interface CookieOptions {
  name: string;
  value: string;
  /** Domain attribute. A leading dot is accepted and normalised away. */
  domain?: string | null;
  /** Path attribute. When omitted, the RFC 6265 default-path is used. */
  path?: string;
  secure?: boolean;
  httpOnly?: boolean;
  sameSite?: CookieSameSite | Lowercase<CookieSameSite> | null;
  /** Epoch milliseconds or a Date/parsable date string. */
  expires?: Date | number | string | null;
  /** Max-Age in seconds. */
  maxAge?: number | null;
  /**
   * The host on whose behalf the cookie is being signed. In normal use this
   * is inferred from the request context and cannot be spoofed.
   */
  issuer?: string;
  /** Creation time in epoch milliseconds. Useful for deterministic tests. */
  created?: number;
  /** Mark the cookie as a server-managed session cookie. */
  session?: boolean;
  /** Emit a deletion (Max-Age=0) instead of a live cookie. */
  clear?: boolean;
}

export interface CookieData {
  name: string;
  value: string;
  domain?: string;
  path: string;
  secure: boolean;
  httpOnly: boolean;
  sameSite: CookieSameSite;
  /** Effective expiry in epoch milliseconds, or null for a session cookie. */
  expires: number | null;
  maxAge?: number | null;
  /** The exact host/subdomain that signed this cookie. */
  issuer: string;
  /** Epoch milliseconds. */
  created: number;
  session: boolean;
}

export interface IssuedCookie extends CookieData {
  /** Name=value pair used in Set-Cookie. */
  wireValue: string;
  /** Complete Set-Cookie header value. */
  header: string;
  setCookie: string;
}

export interface RequestContext {
  host: string;
  path?: string;
  secure?: boolean;
  protocol?: string;
}

export type HeaderBag =
  | Record<string, string | string[] | number | undefined>
  | {
      get(name: string): string | null | undefined;
      has?(name: string): boolean;
    };

export interface RequestLike {
  url?: string;
  originalUrl?: string;
  host?: string;
  hostname?: string;
  protocol?: string;
  secure?: boolean;
  path?: string;
  headers?: HeaderBag;
}

export interface RawCookie {
  name: string;
  value: string;
  index: number;
}

export interface ReceivedCookie extends CookieData {
  rawName: string;
  rawValue: string;
  wireValue: string;
  index: number;
}

export interface RejectedCookie {
  name: string;
  value: string;
  reason: CookieRejectionReason;
  detail?: string;
}

export interface CookieReception {
  /** One effective cookie per name after path/creation-time arbitration. */
  readonly cookies: Record<string, ReceivedCookie>;
  readonly effective: Record<string, ReceivedCookie>;
  readonly selected: Record<string, ReceivedCookie>;
  /** Effective plain values keyed by cookie name. */
  readonly values: Record<string, string>;
  /** Every individually valid cookie, including non-winning duplicates. */
  readonly accepted: ReceivedCookie[];
  readonly rejected: RejectedCookie[];
  get(name: string): ReceivedCookie | undefined;
  getValue(name: string): string | undefined;
  has(name: string): boolean;
}

export interface ValidationResult {
  valid: boolean;
  reason?: CookieRejectionReason;
  detail?: string;
}

interface WirePayload {
  v: 1;
  n: string;
  value: string;
  domain: string | null;
  path: string;
  secure: boolean;
  httpOnly: boolean;
  sameSite: CookieSameSite;
  expires: number | null;
  maxAge: number | null;
  issuer: string;
  created: number;
  session: boolean;
}

interface SessionState {
  id: string;
  issuer: string;
  domain?: string;
  path: string;
  secure: boolean;
  httpOnly: boolean;
  sameSite: CookieSameSite;
  expires: number | null;
  createdAt: number;
  generation: number;
}

export interface ActiveSession {
  id: string;
  issuer: string;
  scope: string;
  domain?: string;
  path: string;
  generation: number;
}

const WIRE_VERSION = "c1";
const TOKEN_NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
const IPV4 = /^(?:\d{1,3}\.){3}\d{1,3}$/;

export const DEFAULT_SESSION_COOKIE_NAME = "__Host-session";

function stripTrailingDot(value: string): string {
  let result = value;
  while (result.endsWith(".")) {
    result = result.slice(0, -1);
  }
  return result;
}

/** Host/domain comparison is case-insensitive and ports are irrelevant. */
export function normalizeHost(input: string): string {
  let value = String(input ?? "").trim();
  if (!value) {
    throw new CookieError("INVALID_OPTIONS", "A host is required");
  }
  if (/[@?#\\]/u.test(value) || /\s/u.test(value)) {
    throw new CookieError("INVALID_OPTIONS", `Invalid host: ${input}`);
  }
  if (!value.includes("://") && value.includes("/")) {
    throw new CookieError("INVALID_OPTIONS", `Invalid host: ${input}`);
  }

  value = value.toLowerCase();
  try {
    const url = value.includes("://")
      ? new URL(value)
      : new URL(`http://${value}`);
    value = url.hostname;
  } catch {
    throw new CookieError("INVALID_OPTIONS", `Invalid host: ${input}`);
  }

  value = stripTrailingDot(value.toLowerCase());
  if (!value || value.includes("*") || value.includes("/") || value.includes(";")) {
    throw new CookieError("INVALID_OPTIONS", `Invalid host: ${input}`);
  }
  return value;
}

export function normalizeDomain(input: string): string {
  let value = String(input ?? "").trim();
  if (value.startsWith(".")) {
    value = value.slice(1);
  }
  if (/[/@?#\\]/u.test(value) || /\s/u.test(value)) {
    throw new CookieError("INVALID_DOMAIN", `Invalid cookie domain: ${input}`);
  }
  const domain = normalizeHost(value);
  if (domain.startsWith(".") || domain.includes(" ")) {
    throw new CookieError("INVALID_DOMAIN", `Invalid cookie domain: ${input}`);
  }
  return domain;
}

/**
 * True when `host` is the domain itself or a more specific subdomain of it.
 * The suffix check is boundary-aware, so "notexample.com" does not match
 * "example.com".
 */
export function domainMatches(host: string, domain: string): boolean {
  if (!host || !domain) return false;
  let normalizedHost: string;
  let normalizedDomain: string;
  try {
    normalizedHost = normalizeHost(host);
    normalizedDomain = normalizeDomain(domain);
  } catch {
    return false;
  }
  return (
    normalizedHost === normalizedDomain ||
    normalizedHost.endsWith(`.${normalizedDomain}`)
  );
}

export function normalizePath(path: string | null | undefined): string {
  if (path == null || path === "") return "/";
  if (!path.startsWith("/")) {
    throw new CookieError("INVALID_PATH", `Cookie path must start with "/": ${path}`);
  }
  if (path.includes(";") || path.includes("\r") || path.includes("\n")) {
    throw new CookieError("INVALID_PATH", `Invalid cookie path: ${path}`);
  }
  return path;
}

/** RFC 6265 default-path algorithm. */
export function defaultCookiePath(requestPath?: string): string {
  if (!requestPath || !requestPath.startsWith("/")) return "/";
  if (requestPath === "/") return "/";
  const lastSlash = requestPath.lastIndexOf("/");
  return lastSlash <= 0 ? "/" : requestPath.slice(0, lastSlash);
}

/** RFC 6265 path-match algorithm, including path-segment boundaries. */
export function pathMatches(requestPath: string, cookiePath: string): boolean {
  const request = normalizePath(requestPath || "/");
  const cookie = normalizePath(cookiePath || "/");
  if (request === cookie) return true;
  if (!request.startsWith(cookie)) return false;
  if (cookie.endsWith("/")) return true;
  return request.charAt(cookie.length) === "/";
}

export function normalizeSameSite(
  value: CookieOptions["sameSite"] | undefined,
): CookieSameSite {
  if (value == null) return "Lax";
  const normalized = String(value).toLowerCase();
  if (normalized === "strict") return "Strict";
  if (normalized === "lax") return "Lax";
  if (normalized === "none") return "None";
  throw new CookieError(
    "INVALID_SAME_SITE",
    `Invalid SameSite value: ${String(value)}`,
  );
}

export function assertCookieName(name: string): void {
  if (typeof name !== "string" || !name || !TOKEN_NAME.test(name)) {
    throw new CookieError("INVALID_NAME", `Invalid cookie name: ${name}`);
  }
}

/** Validate the RFC 6265bis __Host-/__Secure- prefix contract. */
export function validateCookiePrefix(record: CookieData): ValidationResult {
  if (record.name.startsWith("__Host-")) {
    if (record.domain) {
      return {
        valid: false,
        reason: "prefix-domain",
        detail: "__Host- cookies must not specify a Domain",
      };
    }
    if (record.path !== "/") {
      return {
        valid: false,
        reason: "prefix-path",
        detail: "__Host- cookies must use Path=/",
      };
    }
    if (!record.secure) {
      return {
        valid: false,
        reason: "prefix-secure",
        detail: "__Host- cookies must be Secure",
      };
    }
  }

  if (record.name.startsWith("__Secure-") && !record.secure) {
    return {
      valid: false,
      reason: "prefix-secure",
      detail: "__Secure- cookies must be Secure",
    };
  }

  if (record.sameSite === "None" && !record.secure) {
    return {
      valid: false,
      reason: "same-site-none-insecure",
      detail: "SameSite=None cookies must be Secure",
    };
  }

  return { valid: true };
}

function toEpochMs(value: CookieOptions["expires"]): number | null {
  if (value == null) return null;
  if (value instanceof Date) {
    const ms = value.getTime();
    if (!Number.isFinite(ms)) {
      throw new CookieError("INVALID_OPTIONS", "Invalid cookie expiry");
    }
    return ms;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new CookieError("INVALID_OPTIONS", "Invalid cookie expiry");
    }
    return value;
  }
  const parsed = Date.parse(String(value));
  if (!Number.isFinite(parsed)) {
    throw new CookieError("INVALID_OPTIONS", `Invalid cookie expiry: ${value}`);
  }
  return parsed;
}

function normalizeContext(input: RequestContext | RequestLike | string): RequestContext {
  if (typeof input === "string") {
    return { host: normalizeHost(input), path: "/" };
  }
  if (!input || typeof input !== "object") {
    throw new CookieError("INVALID_OPTIONS", "A request context is required");
  }

  const request = input as RequestLike;
  let url: URL | undefined;
  const rawUrl = request.url ?? request.originalUrl;
  if (rawUrl) {
    try {
      url = new URL(rawUrl, "http://placeholder.invalid");
    } catch {
      url = undefined;
    }
  }

  const hostInput = request.host ?? request.hostname ?? url?.hostname;
  if (!hostInput) {
    throw new CookieError("INVALID_OPTIONS", "A request host is required");
  }
  const host = normalizeHost(hostInput);
  const protocol = String(request.protocol ?? url?.protocol ?? "")
    .replace(/:$/, "")
    .toLowerCase();
  const secure =
    request.secure != null ? Boolean(request.secure) : protocol === "https";
  let path = request.path;
  if (path == null && url) {
    path = rawUrl?.startsWith("http") || rawUrl?.startsWith("/")
      ? url.pathname
      : undefined;
  }

  return {
    host,
    path: path ?? "/",
    secure,
    ...(protocol ? { protocol } : {}),
  };
}

function combineContext(
  request: RequestLike,
  explicit?: RequestContext | RequestLike | string,
): RequestContext {
  let merged: RequestContext | RequestLike | string = request;
  if (explicit) {
    if (typeof explicit === "string") {
      merged = {
        ...request,
        host: explicit,
      };
    } else {
      merged = {
        ...request,
        ...explicit,
        headers: request.headers,
      };
    }
  }
  return normalizeContext(merged);
}

function getHeader(headers: HeaderBag | undefined, name: string): string | undefined {
  if (!headers) return undefined;
  if (typeof (headers as { get?: unknown }).get === "function") {
    const value = (headers as { get(name: string): unknown }).get(name);
    if (value == null) return undefined;
    return String(value);
  }
  const bag = headers as Record<string, string | string[] | number | undefined>;
  const wanted = name.toLowerCase();
  for (const key of Object.keys(bag)) {
    if (key.toLowerCase() === wanted) {
      const value = bag[key];
      if (Array.isArray(value)) return value.join("; ");
      if (value == null) return undefined;
      return String(value);
    }
  }
  return undefined;
}

function extractCookieHeader(
  input: string | string[] | RequestLike | null | undefined,
): string | undefined {
  if (typeof input === "string") return input;
  if (Array.isArray(input)) return input.join("; ");
  if (!input || typeof input !== "object") return undefined;
  const direct = (input as { cookie?: unknown }).cookie;
  if (typeof direct === "string") return direct;
  return getHeader(input.headers, "cookie");
}

/** Parse a Cookie header while retaining duplicate names and their order. */
export function parseCookieHeader(
  header: string | string[] | null | undefined,
): RawCookie[] {
  if (header == null) return [];
  const text = Array.isArray(header) ? header.join("; ") : String(header);
  const result: RawCookie[] = [];
  let index = 0;
  const parts: string[] = [];
  let current = "";
  let inQuotes = false;
  let escaped = false;
  for (const character of text) {
    if (escaped) {
      current += character;
      escaped = false;
      continue;
    }
    if (inQuotes && character === "\\") {
      current += character;
      escaped = true;
      continue;
    }
    if (character === '"') {
      inQuotes = !inQuotes;
      current += character;
      continue;
    }
    if (character === ";" && !inQuotes) {
      parts.push(current);
      current = "";
      continue;
    }
    current += character;
  }
  parts.push(current);

  for (const part of parts) {
    const trimmed = part.trim();
    if (!trimmed) continue;
    const equals = trimmed.indexOf("=");
    const name = equals === -1 ? trimmed : trimmed.slice(0, equals).trim();
    let value = equals === -1 ? "" : trimmed.slice(equals + 1).trim();
    if (
      value.length >= 2 &&
      value.startsWith('"') &&
      value.endsWith('"')
    ) {
      value = value
        .slice(1, -1)
        .replace(/\\"/g, '"')
        .replace(/\\\\/g, "\\");
    }
    result.push({ name, value, index });
    index += 1;
  }
  return result;
}

function encodeWireValue(record: CookieData, secret: string | Uint8Array): string {
  const payload: WirePayload = {
    v: 1,
    n: record.name,
    value: record.value,
    domain: record.domain ?? null,
    path: record.path,
    secure: record.secure,
    httpOnly: record.httpOnly,
    sameSite: record.sameSite,
    expires: record.expires,
    maxAge: record.maxAge ?? null,
    issuer: record.issuer,
    created: record.created,
    session: record.session,
  };
  const body = Buffer.from(JSON.stringify(payload), "utf8").toString("base64url");
  const signature = createHmac("sha256", secret)
    .update(`${WIRE_VERSION}.${body}`)
    .digest("base64url");
  return `${WIRE_VERSION}.${body}.${signature}`;
}

function decodeWireValue(
  wireValue: string,
  secret: string | Uint8Array,
): WirePayload | null {
  const parts = wireValue.split(".");
  if (parts.length !== 3 || parts[0] !== WIRE_VERSION) return null;
  const expected = createHmac("sha256", secret)
    .update(`${WIRE_VERSION}.${parts[1]}`)
    .digest();
  let actual: Buffer;
  try {
    actual = Buffer.from(parts[2], "base64url");
  } catch {
    return null;
  }
  if (expected.length !== actual.length) return null;
  if (!timingSafeEqual(expected, actual)) return null;

  let payload: unknown;
  try {
    payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
  } catch {
    return null;
  }
  if (!payload || typeof payload !== "object") return null;
  const candidate = payload as Partial<WirePayload>;
  if (
    candidate.v !== 1 ||
    typeof candidate.n !== "string" ||
    typeof candidate.value !== "string" ||
    (candidate.domain !== null && typeof candidate.domain !== "string") ||
    typeof candidate.path !== "string" ||
    typeof candidate.secure !== "boolean" ||
    typeof candidate.httpOnly !== "boolean" ||
    (candidate.sameSite !== "Strict" &&
      candidate.sameSite !== "Lax" &&
      candidate.sameSite !== "None") ||
    (candidate.expires !== null && typeof candidate.expires !== "number") ||
    (candidate.maxAge !== null && typeof candidate.maxAge !== "number") ||
    typeof candidate.issuer !== "string" ||
    typeof candidate.created !== "number" ||
    typeof candidate.session !== "boolean"
  ) {
    return null;
  }
  return candidate as WirePayload;
}

function payloadToRecord(payload: WirePayload): CookieData {
  return {
    name: payload.n,
    value: payload.value,
    ...(payload.domain ? { domain: payload.domain } : {}),
    path: payload.path,
    secure: payload.secure,
    httpOnly: payload.httpOnly,
    sameSite: payload.sameSite,
    expires: payload.expires,
    ...(payload.maxAge != null ? { maxAge: payload.maxAge } : {}),
    issuer: payload.issuer,
    created: payload.created,
    session: payload.session,
  };
}

/** Cookie scope used to key the server-side session registry. */
export function cookieScope(record: Pick<CookieData, "domain" | "issuer">): string {
  return record.domain
    ? normalizeDomain(record.domain)
    : normalizeHost(record.issuer);
}

function sessionKey(name: string, scope: string): string {
  return `${name}\u0000${scope}`;
}

/**
 * Server-side active-session registry. Only the current identifier is
 * accepted, so rotating a session invalidates the previous identifier
 * immediately. Sessions in the same name/domain scope are issuer-bound: a
 * sibling subdomain cannot take over a parent-domain session even if it can
 * write a Domain cookie with the same name.
 */
export class SessionRegistry {
  states = new Map<string, SessionState>();

  activate(name: string, record: CookieData): ActiveSession {
    const scope = cookieScope(record);
    const key = sessionKey(name, scope);
    let existing = this.states.get(key);
    if (
      existing &&
      existing.expires != null &&
      record.created >= existing.expires
    ) {
      this.states.delete(key);
      existing = undefined;
    }
    if (existing && existing.issuer !== record.issuer) {
      throw new CookieError(
        "SESSION_ISSUER_MISMATCH",
        `Session "${name}" for ${scope} is already bound to issuer ${existing.issuer}`,
        { existingIssuer: existing.issuer, attemptedIssuer: record.issuer },
      );
    }

    const generation = (existing?.generation ?? 0) + 1;
    const state: SessionState = {
      id: record.value,
      issuer: record.issuer,
      ...(record.domain ? { domain: record.domain } : {}),
      path: record.path,
      secure: record.secure,
      httpOnly: record.httpOnly,
      sameSite: record.sameSite,
      expires: record.expires,
      createdAt: record.created,
      generation,
    };
    this.states.set(key, state);
    return {
      id: state.id,
      issuer: state.issuer,
      scope,
      ...(state.domain ? { domain: state.domain } : {}),
      path: state.path,
      generation: state.generation,
    };
  }

  isActive(name: string, record: CookieData): boolean {
    const state = this.states.get(sessionKey(name, cookieScope(record)));
    return Boolean(
      state &&
        state.id === record.value &&
        state.issuer === record.issuer &&
        state.domain === record.domain,
    );
  }

  get(name: string, scope: string): SessionState | undefined {
    return this.states.get(sessionKey(name, scope));
  }

  has(name: string, scope: string): boolean {
    return this.states.has(sessionKey(name, scope));
  }

  findByIssuer(name: string, issuer: string): { scope: string; state: SessionState } | null {
    const normalizedIssuer = normalizeHost(issuer);
    for (const [key, state] of this.states) {
      if (key.startsWith(`${name}\u0000`) && state.issuer === normalizedIssuer) {
        return { scope: state.domain ?? state.issuer, state };
      }
    }
    return null;
  }

  revoke(name: string, scope: string, issuer?: string): boolean {
    const key = sessionKey(name, scope);
    const state = this.states.get(key);
    if (!state) return false;
    if (issuer && state.issuer !== normalizeHost(issuer)) {
      throw new CookieError(
        "SESSION_ISSUER_MISMATCH",
        `Session "${name}" for ${scope} can only be revoked by ${state.issuer}`,
      );
    }
    this.states.delete(key);
    return true;
  }

  current(name: string, scope: string): ActiveSession | null {
    const state = this.states.get(sessionKey(name, scope));
    if (!state) return null;
    return {
      id: state.id,
      issuer: state.issuer,
      scope,
      ...(state.domain ? { domain: state.domain } : {}),
      path: state.path,
      generation: state.generation,
    };
  }
}

export interface CookieManagerOptions {
  /** HMAC key. Use a high-entropy, application-private value. */
  secret: string | Uint8Array;
  now?: () => number;
  sessionRegistry?: SessionRegistry;
  /** Cookie names that are always treated as server-managed sessions. */
  sessionCookieNames?: string[];
  /** Optional allow-list of issuer hosts. */
  trustedIssuers?: string[];
}

function formatExpires(expires: number): string {
  return new Date(expires).toUTCString();
}

function assertIssuableRecord(
  record: CookieData,
  context: RequestContext,
): void {
  assertCookieName(record.name);
  if (record.issuer !== normalizeHost(context.host)) {
    throw new CookieError(
      "ISSUER_MISMATCH",
      `Cookie issuer ${record.issuer} does not match request host ${context.host}`,
    );
  }

  const prefix = validateCookiePrefix(record);
  if (!prefix.valid) {
    const code: CookieErrorCode =
      prefix.reason === "prefix-domain"
        ? "PREFIX_DOMAIN"
        : prefix.reason === "prefix-path"
          ? "PREFIX_PATH"
          : prefix.reason === "same-site-none-insecure"
            ? "SAME_SITE_NONE_INSECURE"
            : "PREFIX_SECURE";
    throw new CookieError(code, prefix.detail ?? "Invalid cookie prefix");
  }

  if (record.domain) {
    if (!domainMatches(record.issuer, record.domain)) {
      throw new CookieError(
        "DOMAIN_NOT_OWNED",
        `Issuer ${record.issuer} cannot set a cookie for Domain=${record.domain}`,
      );
    }
    if (IPV4.test(record.issuer) || record.issuer.startsWith("[")) {
      throw new CookieError(
        "INVALID_DOMAIN",
        "Domain cookies cannot be issued for an IP address",
      );
    }
  }

  if (record.session && record.value.length === 0) {
    throw new CookieError(
      "INVALID_OPTIONS",
      "Session cookies require a non-empty identifier",
    );
  }

  if (record.expires != null && !Number.isFinite(record.expires)) {
    throw new CookieError("INVALID_OPTIONS", "Cookie expiry must be finite");
  }
}

function buildHeader(record: CookieData, wireValue: string, clear: boolean): string {
  const parts = [`${record.name}=${clear ? "" : wireValue}`];
  if (record.domain) parts.push(`Domain=${record.domain}`);
  parts.push(`Path=${record.path}`);

  if (clear) {
    parts.push("Expires=Thu, 01 Jan 1970 00:00:00 GMT");
    parts.push("Max-Age=0");
  } else {
    if (record.expires != null) {
      parts.push(`Expires=${formatExpires(record.expires)}`);
    }
    if (record.maxAge != null) {
      parts.push(`Max-Age=${record.maxAge}`);
    }
  }
  if (record.secure) parts.push("Secure");
  if (record.httpOnly) parts.push("HttpOnly");
  parts.push(`SameSite=${record.sameSite}`);
  return parts.join("; ");
}

export class CookieManager {
  secret: string | Uint8Array;
  now: () => number;
  registry: SessionRegistry;
  sessionCookieNames: Set<string>;
  trustedIssuers: Set<string>;

  constructor(options: CookieManagerOptions) {
    if (!options || typeof options !== "object") {
      throw new CookieError("INVALID_OPTIONS", "CookieManager options are required");
    }
    if (
      options.secret == null ||
      (typeof options.secret === "string" && options.secret.length === 0) ||
      (options.secret instanceof Uint8Array && options.secret.length === 0)
    ) {
      throw new CookieError("INVALID_OPTIONS", "A non-empty signing secret is required");
    }
    this.secret = options.secret;
    this.now = options.now ?? Date.now;
    this.registry = options.sessionRegistry ?? new SessionRegistry();
    this.sessionCookieNames = new Set(options.sessionCookieNames ?? []);
    this.trustedIssuers = new Set(
      (options.trustedIssuers ?? []).map((host) => normalizeHost(host)),
    );
  }

  /**
   * Sign a cookie and return both its metadata and a ready-to-send Set-Cookie
   * value. Invalid prefix/domain combinations throw synchronously.
   */
  issue(
    options: CookieOptions,
    contextInput?: RequestContext | RequestLike | string,
  ): IssuedCookie {
    if (!options || typeof options !== "object") {
      throw new CookieError("INVALID_OPTIONS", "Cookie options are required");
    }
    if (contextInput == null && options.issuer) {
      contextInput = options.issuer;
    }
    if (contextInput == null) {
      throw new CookieError("INVALID_OPTIONS", "A request context is required");
    }
    const context = normalizeContext(contextInput);
    const requestHost = normalizeHost(context.host);
    const issuer = options.issuer
      ? normalizeHost(options.issuer)
      : requestHost;
    if (issuer !== requestHost) {
      throw new CookieError(
        "ISSUER_MISMATCH",
        `Cookie issuer ${issuer} does not match request host ${requestHost}`,
      );
    }

    assertCookieName(options.name);
    const requestedDomain =
      options.domain == null || String(options.domain).trim() === ""
        ? undefined
        : normalizeDomain(String(options.domain));
    const pendingPath =
      options.path == null || String(options.path).trim() === ""
        ? defaultCookiePath(context.path)
        : normalizePath(String(options.path));
    const sameSite = normalizeSameSite(options.sameSite);
    const secure =
      options.secure == null
        ? context.secure === true
        : Boolean(options.secure);
    const session =
      options.session === true || this.sessionCookieNames.has(options.name);
    const httpOnly =
      options.httpOnly == null ? session : Boolean(options.httpOnly);
    const created =
      options.created == null ? this.now() : Number(options.created);
    if (!Number.isFinite(created)) {
      throw new CookieError("INVALID_OPTIONS", "created must be finite");
    }

    let expires = toEpochMs(options.expires);
    const maxAge =
      options.maxAge == null ? null : Number(options.maxAge);
    if (maxAge != null && (!Number.isFinite(maxAge) || maxAge < 0)) {
      throw new CookieError("INVALID_OPTIONS", "maxAge must be a non-negative number");
    }
    if (maxAge != null && expires == null && options.clear !== true) {
      expires = created + maxAge * 1000;
    }

    const clear = options.clear === true;
    const record: CookieData = {
      name: options.name,
      value: clear ? "" : String(options.value ?? ""),
      ...(requestedDomain ? { domain: requestedDomain } : {}),
      path: pendingPath,
      secure,
      httpOnly,
      sameSite,
      expires: clear ? 0 : expires,
      maxAge: clear ? 0 : maxAge,
      issuer,
      created,
      session: session && !clear,
    };

    assertIssuableRecord(record, context);

    if (record.session) {
      this.registry.activate(record.name, record);
    }

    const wireValue = clear ? "" : encodeWireValue(record, this.secret);
    const header = buildHeader(record, wireValue, clear);
    return {
      ...record,
      wireValue,
      header,
      setCookie: header,
    };
  }

  /** Alias for issue(). */
  sign(
    options: CookieOptions,
    contextInput?: RequestContext | RequestLike | string,
  ): IssuedCookie {
    return this.issue(options, contextInput);
  }

  /**
   * Verify and resolve a Cookie header. For duplicate names the RFC 6265
   * ordering is used: longest matching Path first, then earliest creation.
   */
  receive(
    input?: string | string[] | RequestLike | null,
    contextInput?: RequestContext | RequestLike | string,
  ): CookieReception {
    let context: RequestContext;
    let header: string | undefined;
    if (input && typeof input === "object" && !Array.isArray(input)) {
      context = combineContext(input, contextInput);
      header = extractCookieHeader(input);
      if (
        header == null &&
        contextInput &&
        typeof contextInput === "object" &&
        !Array.isArray(contextInput)
      ) {
        header = extractCookieHeader(contextInput);
      }
    } else {
      header = extractCookieHeader(input);
      if (contextInput == null) {
        throw new CookieError("INVALID_OPTIONS", "A request context is required");
      }
      context = normalizeContext(contextInput);
    }

    const accepted: ReceivedCookie[] = [];
    const rejected: RejectedCookie[] = [];
    const now = this.now();
    const requestHost = normalizeHost(context.host);
    const requestPath = normalizePath(context.path ?? "/");

    const reject = (
      raw: RawCookie,
      reason: CookieRejectionReason,
      detail?: string,
    ): void => {
      rejected.push({
        name: raw.name,
        value: raw.value,
        reason,
        ...(detail ? { detail } : {}),
      });
    };

    for (const raw of parseCookieHeader(header)) {
      if (!raw.name) {
        reject(raw, "malformed", "Empty cookie name");
        continue;
      }
      if (!TOKEN_NAME.test(raw.name)) {
        reject(raw, "invalid-name", `Invalid cookie name: ${raw.name}`);
        continue;
      }

      const payload = decodeWireValue(raw.value, this.secret);
      if (!payload) {
        reject(
          raw,
          "invalid-signature",
          "Cookie value is not a valid signed envelope",
        );
        continue;
      }
      if (payload.n !== raw.name) {
        reject(
          raw,
          "name-mismatch",
          `Signed name ${payload.n} does not match header name ${raw.name}`,
        );
        continue;
      }

      const record = payloadToRecord(payload);
      const prefix = validateCookiePrefix(record);
      if (!prefix.valid && prefix.reason) {
        reject(raw, prefix.reason, prefix.detail);
        continue;
      }

      if (
        this.trustedIssuers.size > 0 &&
        !this.trustedIssuers.has(normalizeHost(record.issuer))
      ) {
        reject(
          raw,
          "untrusted-issuer",
          `Issuer ${record.issuer} is not in the trusted issuer list`,
        );
        continue;
      }

      if (record.domain) {
        if (!domainMatches(requestHost, record.domain)) {
          reject(
            raw,
            "domain-mismatch",
            `Request host ${requestHost} is not within Domain=${record.domain}`,
          );
          continue;
        }
        if (!domainMatches(record.issuer, record.domain)) {
          reject(
            raw,
            "unowned-domain",
            `Issuer ${record.issuer} cannot claim Domain=${record.domain}`,
          );
          continue;
        }
      } else if (record.issuer !== requestHost) {
        reject(
          raw,
          "host-only-issuer-mismatch",
          `Host-only cookie issuer ${record.issuer} does not match request host ${requestHost}`,
        );
        continue;
      }

      if (!pathMatches(requestPath, record.path)) {
        reject(
          raw,
          "path-mismatch",
          `Cookie path ${record.path} does not match request path ${requestPath}`,
        );
        continue;
      }

      if (record.secure && context.secure === false) {
        reject(
          raw,
          "insecure-transport",
          "Secure cookie was presented over an insecure transport",
        );
        continue;
      }

      if (record.expires != null && now >= record.expires) {
        reject(raw, "expired", "Cookie has expired");
        continue;
      }

      const managedSession =
        record.session ||
        this.sessionCookieNames.has(record.name) ||
        this.registry.has(record.name, cookieScope(record));
      if (managedSession && !this.registry.isActive(record.name, record)) {
        reject(
          raw,
          "session-revoked",
          "Session identifier is no longer active",
        );
        continue;
      }

      accepted.push({
        ...record,
        rawName: raw.name,
        rawValue: raw.value,
        wireValue: raw.value,
        index: raw.index,
      });
    }

    const groups = new Map<string, ReceivedCookie[]>();
    for (const cookie of accepted) {
      const existing = groups.get(cookie.name);
      if (existing) existing.push(cookie);
      else groups.set(cookie.name, [cookie]);
    }

    const effective: Record<string, ReceivedCookie> = {};
    for (const [name, candidates] of groups) {
      candidates.sort((left, right) => {
        const pathLength = right.path.length - left.path.length;
        if (pathLength !== 0) return pathLength;
        const created = left.created - right.created;
        if (created !== 0) return created;
        return left.index - right.index;
      });
      // defineProperty keeps names such as "__proto__" as plain own values.
      Object.defineProperty(effective, name, {
        value: candidates[0],
        enumerable: true,
        configurable: true,
        writable: true,
      });
    }

    const values: Record<string, string> = {};
    for (const [name, cookie] of Object.entries(effective)) {
      Object.defineProperty(values, name, {
        value: cookie.value,
        enumerable: true,
        configurable: true,
        writable: true,
      });
    }

    return {
      cookies: effective,
      effective,
      selected: effective,
      values,
      accepted,
      rejected,
      get: (name: string) => effective[name],
      getValue: (name: string) => effective[name]?.value,
      has: (name: string) => Object.prototype.hasOwnProperty.call(effective, name),
    };
  }

  /** Alias for receive(). */
  read(
    input?: string | string[] | RequestLike | null,
    contextInput?: RequestContext | RequestLike | string,
  ): CookieReception {
    return this.receive(input, contextInput);
  }

  /** Alias for receive(). */
  parse(
    input?: string | string[] | RequestLike | null,
    contextInput?: RequestContext | RequestLike | string,
  ): CookieReception {
    return this.receive(input, contextInput);
  }

  /** Alias for receive(). */
  verify(
    input?: string | string[] | RequestLike | null,
    contextInput?: RequestContext | RequestLike | string,
  ): CookieReception {
    return this.receive(input, contextInput);
  }

  rotateSession(
    name: string,
    contextInput: RequestContext | RequestLike | string,
    options?: Omit<CookieOptions, "name" | "value" | "session" | "clear">,
    nextValue?: string,
  ): IssuedCookie {
    const context = normalizeContext(contextInput);
    const issuer = normalizeHost(context.host);
    const state = this.closeSessionLookup(name, context, options);
    if (!state) {
      throw new CookieError(
        "SESSION_NOT_FOUND",
        `No active session "${name}" was found for ${issuer}`,
      );
    }
    const value = nextValue ?? randomBytes(32).toString("base64url");
    const optionDefinesExpiry =
      options?.expires != null || options?.maxAge != null;
    return this.issue(
      {
        ...(state.domain ? { domain: state.domain } : {}),
        path: state.path,
        secure: state.secure,
        httpOnly: state.httpOnly,
        sameSite: state.sameSite,
        ...(state.expires != null && !optionDefinesExpiry
          ? { expires: state.expires }
          : {}),
        ...options,
        name,
        value,
        session: true,
      },
      context,
    );
  }

  logout(
    name: string,
    contextInput: RequestContext | RequestLike | string,
    options?: Omit<CookieOptions, "name" | "value" | "session" | "clear">,
  ): IssuedCookie {
    const context = normalizeContext(contextInput);
    const issuer = normalizeHost(context.host);
    const state = this.closeSessionLookup(name, context, options);
    if (state) {
      const scope = state.domain ?? state.issuer;
      this.registry.revoke(name, scope, issuer);
    }
    return this.issue(
      {
        ...(state?.domain ? { domain: state.domain } : {}),
        path: state?.path ?? options?.path ?? defaultCookiePath(context.path),
        secure: state?.secure ?? options?.secure ?? context.secure === true,
        httpOnly: state?.httpOnly ?? options?.httpOnly ?? false,
        sameSite: state?.sameSite ?? options?.sameSite ?? "Lax",
        ...options,
        name,
        value: "",
        clear: true,
      },
      context,
    );
  }

  private closeSessionLookup(
    name: string,
    context: RequestContext,
    options?: Omit<CookieOptions, "name" | "value" | "session" | "clear">,
  ): SessionState | null {
    if (options?.domain != null) {
      const scope = normalizeDomain(String(options.domain));
      return this.registry.get(name, scope) ?? null;
    }
    const found = this.registry.findByIssuer(name, context.host);
    return found?.state ?? null;
  }

  getSession(name: string, contextInput: RequestContext | RequestLike | string): ActiveSession | null {
    const context = normalizeContext(contextInput);
    const found = this.registry.findByIssuer(name, context.host);
    return found ? this.registry.current(name, found.scope) : null;
  }

  isSessionActive(
    name: string,
    value: string,
    contextInput: RequestContext | RequestLike | string,
  ): boolean {
    const context = normalizeContext(contextInput);
    const found = this.registry.findByIssuer(name, context.host);
    if (!found) return false;
    return found.state.id === value && found.state.issuer === normalizeHost(context.host);
  }
}

export interface SessionManagerOptions extends CookieManagerOptions {
  cookieName?: string;
  sameSite?: CookieSameSite | Lowercase<CookieSameSite>;
  secure?: boolean;
  httpOnly?: boolean;
  path?: string;
  domain?: string | null;
  maxAge?: number;
  expires?: Date | number | string | null;
}

export interface AuthenticationResult {
  authenticated: boolean;
  sessionId?: string;
  cookie?: ReceivedCookie;
  reception: CookieReception;
  reason?: string;
}

/**
 * Opinionated session wrapper. It defaults to a __Host- cookie (no Domain,
 * Path=/, Secure, HttpOnly), which is the only cookie form a sibling subdomain
 * cannot overwrite for the parent host.
 */
export class SessionManager {
  cookies: CookieManager;
  cookieName: string;
  sameSite: CookieSameSite;
  secure: boolean;
  httpOnly: boolean;
  path: string;
  domain?: string;
  maxAge?: number;
  expires?: Date | number | string | null;

  constructor(options: SessionManagerOptions) {
    this.cookies = new CookieManager(options);
    this.cookieName = options.cookieName ?? DEFAULT_SESSION_COOKIE_NAME;
    this.sameSite = normalizeSameSite(options.sameSite);
    this.secure = options.secure ?? true;
    this.httpOnly = options.httpOnly ?? true;
    this.path = options.path ?? "/";
    if (options.domain != null && String(options.domain).trim() !== "") {
      this.domain = normalizeDomain(String(options.domain));
    }
    this.maxAge = options.maxAge ?? 7 * 24 * 60 * 60;
    this.expires = options.expires;
  }

  login(
    contextInput: RequestContext | RequestLike | string,
    value?: string,
  ): IssuedCookie {
    const sessionId = value ?? randomBytes(32).toString("base64url");
    return this.cookies.issue(this.sessionOptions(sessionId), contextInput);
  }

  rotate(
    contextInput: RequestContext | RequestLike | string,
    value?: string,
  ): IssuedCookie {
    return this.cookies.rotateSession(
      this.cookieName,
      contextInput,
      this.sessionDefaults(),
      value,
    );
  }

  logout(contextInput: RequestContext | RequestLike | string): IssuedCookie {
    return this.cookies.logout(
      this.cookieName,
      contextInput,
      this.sessionDefaults(),
    );
  }

  authenticate(
    input: string | string[] | RequestLike | null | undefined,
    contextInput?: RequestContext | RequestLike | string,
  ): AuthenticationResult {
    const reception = this.cookies.receive(input, contextInput);
    const cookie = reception.get(this.cookieName);
    if (cookie) {
      return {
        authenticated: true,
        sessionId: cookie.value,
        cookie,
        reception,
      };
    }
    const matching = reception.rejected.find(
      (candidate) => candidate.name === this.cookieName,
    );
    return {
      authenticated: false,
      reception,
      reason: matching?.reason ?? "missing",
    };
  }

  sessionOptions(value: string): CookieOptions {
    return {
      ...this.sessionDefaults(),
      name: this.cookieName,
      value,
      session: true,
    };
  }

  sessionDefaults(): Omit<CookieOptions, "name" | "value" | "session" | "clear"> {
    return {
      ...(this.domain ? { domain: this.domain } : {}),
      path: this.path,
      secure: this.secure,
      httpOnly: this.httpOnly,
      sameSite: this.sameSite,
      ...(this.expires != null
        ? { expires: this.expires }
        : { maxAge: this.maxAge }),
    };
  }
}

export function createCookieManager(options: CookieManagerOptions): CookieManager {
  return new CookieManager(options);
}

export function createSessionManager(options: SessionManagerOptions): SessionManager {
  return new SessionManager(options);
}

/**
 * Serialize an already validated/signed record to a Set-Cookie header value.
 * The manager's issue() method is preferred because it also signs the value.
 */
export function serializeCookie(
  record: CookieData,
  wireValue: string = record.value,
  clear = false,
): string {
  const prefix = validateCookiePrefix(record);
  if (!prefix.valid) {
    throw new CookieError("PREFIX_SECURE", prefix.detail ?? "Invalid cookie");
  }
  return buildHeader(record, wireValue, clear || record.expires === 0);
}

/**
 * Standalone convenience wrapper for a manager-first call style.
 */
export function issueCookie(
  manager: CookieManager,
  options: CookieOptions,
  context?: RequestContext | RequestLike | string,
): IssuedCookie;
/**
 * Standalone convenience wrapper for a manager-last call style.
 */
export function issueCookie(
  options: CookieOptions,
  context: RequestContext | RequestLike | string,
  manager: CookieManager,
): IssuedCookie;
export function issueCookie(
  first: CookieManager | CookieOptions,
  second: CookieOptions | RequestContext | RequestLike | string,
  third?: CookieManager | RequestContext | RequestLike | string,
): IssuedCookie {
  if (first instanceof CookieManager) {
    return first.issue(
      second as CookieOptions,
      third as RequestContext | RequestLike | string | undefined,
    );
  }
  if (third instanceof CookieManager) {
    return third.issue(first, second as RequestContext | RequestLike | string);
  }
  throw new CookieError("INVALID_OPTIONS", "issueCookie requires a CookieManager");
}

/**
 * Standalone convenience wrapper for a manager-first call style.
 */
export function receiveCookies(
  manager: CookieManager,
  input?: string | string[] | RequestLike | null,
  context?: RequestContext | RequestLike | string,
): CookieReception;
/**
 * Standalone convenience wrapper for a manager-last call style.
 */
export function receiveCookies(
  input: string | string[] | RequestLike | null | undefined,
  context: RequestContext | RequestLike | string,
  manager: CookieManager,
): CookieReception;
export function receiveCookies(
  first: CookieManager | string | string[] | RequestLike | null | undefined,
  second?: string | string[] | RequestLike | null | RequestContext | RequestLike,
  third?: CookieManager | RequestContext | RequestLike | string,
): CookieReception {
  if (first instanceof CookieManager) {
    return first.receive(
      second as string | string[] | RequestLike | null | undefined,
      third as RequestContext | RequestLike | string | undefined,
    );
  }
  if (third instanceof CookieManager) {
    return third.receive(
      first as string | string[] | RequestLike | null | undefined,
      second as RequestContext | RequestLike | string,
    );
  }
  throw new CookieError("INVALID_OPTIONS", "receiveCookies requires a CookieManager");
}

export const verifyCookies = receiveCookies;
export const signCookie = issueCookie;

export default CookieManager;
