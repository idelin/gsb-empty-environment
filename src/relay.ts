import { isOriginAllowed } from "./origins.ts";
import {
  getPathname,
  getRequestTarget,
  normalizePath,
} from "./paths.ts";
import {
  hasSensitiveParams,
  stripSensitiveParams,
  type SensitiveParamOptions,
} from "./params.ts";
import {
  applyReferrerPolicy,
  REDIRECT_REFERRER_POLICY,
} from "./policy.ts";

export interface RelayRecord {
  token: string;
  target: string;
  createdAt: number;
  expiresAt: number;
  usedAt?: number;
  metadata?: Record<string, string>;
}

export interface RelayPutOptions {
  token?: string;
  ttlMs?: number;
  metadata?: Record<string, string>;
}

export interface RelayStore {
  put(target: string, options?: RelayPutOptions): RelayRecord;
  consume(token: string): RelayRecord | null;
  peek(token: string): RelayRecord | null;
}

export interface RelayStoreOptions {
  ttlMs?: number;
  maxEntries?: number;
  now?: () => number;
  tokenFactory?: () => string;
}

function base64Url(bytes: Uint8Array): string {
  if (typeof Buffer !== "undefined") {
    return Buffer.from(bytes).toString("base64url");
  }
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

/** Cryptographically random opaque relay token. */
export function createRelayToken(): string {
  const cryptoApi = (globalThis as {
    crypto?: {
      randomUUID?: () => string;
      getRandomValues?: (array: Uint8Array) => Uint8Array;
    };
  }).crypto;

  if (cryptoApi?.randomUUID !== undefined) {
    return cryptoApi.randomUUID().replace(/-/g, "");
  }

  if (cryptoApi?.getRandomValues !== undefined) {
    const bytes = new Uint8Array(32);
    cryptoApi.getRandomValues(bytes);
    return base64Url(bytes);
  }

  throw new Error("No secure random source is available for relay tokens");
}

/**
 * In-memory, one-time relay store. `consume` deletes the record before
 * validating expiry, so an expired or already-used token can never be replayed.
 */
export class InMemoryRelayStore implements RelayStore {
  ttlMs: number;
  maxEntries: number;
  now: () => number;
  tokenFactory: () => string;
  records: Map<string, RelayRecord>;

  constructor(options: RelayStoreOptions = {}) {
    this.ttlMs = Math.max(1, options.ttlMs ?? 5 * 60_000);
    this.maxEntries = Math.max(1, options.maxEntries ?? 10_000);
    this.now = options.now ?? (() => Date.now());
    this.tokenFactory = options.tokenFactory ?? createRelayToken;
    this.records = new Map<string, RelayRecord>();
  }

  prune(at: number = this.now()): number {
    let removed = 0;
    for (const [token, record] of this.records) {
      if (record.expiresAt <= at) {
        this.records.delete(token);
        removed += 1;
      }
    }
    return removed;
  }

  put(target: string, options: RelayPutOptions = {}): RelayRecord {
    const at = this.now();
    this.prune(at);
    while (this.records.size >= this.maxEntries) {
      const oldest = this.records.keys().next();
      if (oldest.done === true) break;
      this.records.delete(oldest.value);
    }

    let token = options.token ?? this.tokenFactory();
    let attempts = 0;
    while (this.records.has(token)) {
      attempts += 1;
      if (attempts > 100) {
        throw new Error("Unable to allocate a unique relay token");
      }
      token = this.tokenFactory();
    }

    const record: RelayRecord = {
      token,
      target,
      createdAt: at,
      expiresAt: at + Math.max(1, options.ttlMs ?? this.ttlMs),
      metadata: options.metadata,
    };
    this.records.set(token, record);
    return record;
  }

  consume(token: string): RelayRecord | null {
    const record = this.records.get(token);
    if (record === undefined) return null;

    // Delete before checking expiry: a consumed link can never be replayed.
    this.records.delete(token);
    if (record.expiresAt <= this.now()) return null;
    record.usedAt = this.now();
    return record;
  }

  peek(token: string): RelayRecord | null {
    const record = this.records.get(token);
    if (record === undefined) return null;
    if (record.expiresAt <= this.now()) {
      this.records.delete(token);
      return null;
    }
    return record;
  }

  get size(): number {
    return this.records.size;
  }

  clear(): void {
    this.records.clear();
  }
}

export interface OneTimeRelayOptions extends RelayStoreOptions {
  store?: RelayStore;
  /** Path that consumes relay tokens. Defaults to `/__referrer-relay`. */
  mountPath?: string;
  /** Query parameter carrying the opaque token. Defaults to `t`. */
  paramName?: string;
  /** Base URL used when returning an absolute relay href. */
  baseUrl?: string;
  /** Only http/https targets may be stored in the relay. */
  allowedProtocols?: readonly string[];
  /** Optional destination allow-list. Omit to allow any http(s) destination. */
  allowedOrigins?: readonly string[];
  maxTargetLength?: number;
  stripOptions?: SensitiveParamOptions;
}

export interface RelayLink {
  token: string;
  url: string;
  target: string;
  expiresAt: number;
}

export interface RelayResponse {
  status: number;
  headers: Record<string, string>;
  body?: string;
  location?: string;
}

const RELAY_POLICY_HEADERS: Record<string, string> = {
  "Referrer-Policy": REDIRECT_REFERRER_POLICY,
  "Cache-Control": "no-store",
};

function normalizeMountPath(mountPath: string): string {
  const normalized = normalizePath(mountPath);
  return normalized === "/" ? "/__referrer-relay" : normalized;
}

function hasScheme(value: string): boolean {
  return /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(value);
}

function normalizeBase(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length === 0) return "https://portal.invalid";
  if (trimmed.startsWith("//")) return `https:${trimmed}`;
  if (hasScheme(trimmed)) return trimmed;
  return `https://${trimmed}`;
}

function protocolAllowed(
  protocol: string,
  allowed: readonly string[],
): boolean {
  const normalizedProtocol = protocol.toLowerCase();
  return allowed.some(
    (value) => {
      const normalizedValue = String(value).toLowerCase();
      return (
        (normalizedValue.endsWith(":")
          ? normalizedValue
          : `${normalizedValue}:`) === normalizedProtocol
      );
    },
  );
}

/**
 * One-time outbound relay. The public relay URL contains only an opaque token;
 * the destination and the initiating page's sensitive parameters never appear
 * in the URL a user can copy or a third party can observe.
 */
export class OneTimeRelay {
  store: RelayStore;
  mountPath: string;
  paramName: string;
  baseUrl?: string;
  allowedProtocols: readonly string[];
  allowedOrigins?: readonly string[];
  maxTargetLength: number;
  stripOptions: SensitiveParamOptions;

  constructor(options: OneTimeRelayOptions = {}) {
    this.store =
      options.store ??
      new InMemoryRelayStore({
        ttlMs: options.ttlMs,
        maxEntries: options.maxEntries,
        now: options.now,
        tokenFactory: options.tokenFactory,
      });
    this.mountPath = normalizeMountPath(
      options.mountPath ?? "/__referrer-relay",
    );
    this.paramName = options.paramName ?? "t";
    this.baseUrl =
      options.baseUrl === undefined
        ? undefined
        : normalizeBase(options.baseUrl);
    this.allowedProtocols = options.allowedProtocols ?? ["https:", "http:"];
    this.allowedOrigins = options.allowedOrigins;
    this.maxTargetLength = options.maxTargetLength ?? 4_096;
    this.stripOptions = options.stripOptions ?? {};
  }

  /** Normalizes, sanitizes and validates a relay destination. */
  sanitizeTarget(target: string | URL, baseUrl?: string): string | null {
    const raw = target instanceof URL ? target.href : String(target).trim();
    if (raw.length === 0 || raw.length > this.maxTargetLength) return null;
    if (/[\u0000-\u001f\u007f]/.test(raw)) return null;
    if (!hasScheme(raw) && !raw.startsWith("//")) {
      if (baseUrl === undefined && this.baseUrl === undefined) return null;
    }

    let parsed: URL;
    try {
      parsed = new URL(
        raw.startsWith("//") ? `https:${raw}` : raw,
        baseUrl ?? this.baseUrl ?? "https://portal.invalid",
      );
    } catch {
      return null;
    }

    if (!protocolAllowed(parsed.protocol, this.allowedProtocols)) return null;
    if (
      this.allowedOrigins !== undefined &&
      !isOriginAllowed(parsed.origin, this.allowedOrigins)
    ) {
      return null;
    }

    const hadSensitiveParams = hasSensitiveParams(parsed.href, this.stripOptions);
    let sanitized = stripSensitiveParams(parsed.href, {
      ...this.stripOptions,
      stripCredentials: true,
    });

    if (hadSensitiveParams) {
      try {
        const queryFree = new URL(sanitized);
        queryFree.search = "";
        sanitized = queryFree.href;
      } catch {
        // Keep the already-sanitized value when it cannot be parsed.
      }
    }

    return sanitized.length > this.maxTargetLength ? null : sanitized;
  }

  buildRelayUrl(token: string): string {
    const path = `${this.mountPath}?${encodeURIComponent(this.paramName)}=${encodeURIComponent(token)}`;
    if (this.baseUrl === undefined) return path;
    try {
      const base = this.baseUrl.startsWith("//")
        ? `https:${this.baseUrl}`
        : this.baseUrl;
      return new URL(path, base).href;
    } catch {
      return path;
    }
  }

  createLink(
    target: string | URL,
    options: RelayPutOptions = {},
  ): RelayLink | null {
    const safeTarget = this.sanitizeTarget(target);
    if (safeTarget === null) return null;
    const record = this.store.put(safeTarget, options);
    return {
      token: record.token,
      url: this.buildRelayUrl(record.token),
      target: safeTarget,
      expiresAt: record.expiresAt,
    };
  }

  /** Aliases used by link factories. */
  createUrl(target: string | URL, options: RelayPutOptions = {}): string | null {
    return this.createLink(target, options)?.url ?? null;
  }

  consume(token: string): string | null {
    const record = this.store.consume(token);
    if (record === null) return null;
    return this.sanitizeTarget(record.target);
  }

  consumeRecord(token: string): RelayRecord | null {
    const record = this.store.consume(token);
    if (record === null) return null;
    if (this.sanitizeTarget(record.target) === null) return null;
    return record;
  }

  peek(token: string): RelayRecord | null {
    return this.store.peek(token);
  }

  matchesPath(input: unknown): boolean {
    return normalizePath(getRequestTarget(input)) === this.mountPath;
  }

  /**
   * Pure relay handler. It consumes the token once, then returns a 302 with a
   * sanitized Location. Repeated requests receive 410 Gone.
   */
  handleRequest(input: unknown, options: { method?: string } = {}): RelayResponse {
    const method = (options.method ?? "GET").toUpperCase();
    if (method !== "GET" && method !== "HEAD") {
      return {
        status: 405,
        headers: { ...RELAY_POLICY_HEADERS, Allow: "GET, HEAD" },
        body: "Method Not Allowed",
      };
    }

    const target = getRequestTarget(input);
    let parsed: URL;
    try {
      parsed = new URL(
        target.startsWith("//") ? `https:${target}` : target,
        this.baseUrl ?? "https://portal.invalid",
      );
    } catch {
      return { status: 400, headers: RELAY_POLICY_HEADERS, body: "Bad Request" };
    }

    const token = parsed.searchParams.get(this.paramName);
    if (token === null || token.length === 0) {
      return { status: 400, headers: RELAY_POLICY_HEADERS, body: "Missing relay token" };
    }

    const destination = this.consume(token);
    if (destination === null) {
      return {
        status: 410,
        headers: { ...RELAY_POLICY_HEADERS, "Content-Type": "text/plain; charset=utf-8" },
        body: "This relay link has already been used or has expired.",
      };
    }

    return {
      status: 302,
      headers: {
        ...RELAY_POLICY_HEADERS,
        Location: destination,
        "Content-Type": "text/plain; charset=utf-8",
      },
      body: "Redirecting…",
      location: destination,
    };
  }

  /** Short alias for `handleRequest`. */
  handle(input: unknown, options: { method?: string } = {}): RelayResponse {
    return this.handleRequest(input, options);
  }
}

/** Creates a relay with the given options. */
export function createRelay(
  options: OneTimeRelayOptions = {},
): OneTimeRelay {
  return new OneTimeRelay(options);
}

export const createOneTimeRelay = createRelay;

/** Convenience wrapper that returns a relay link for a destination. */
export function createRelayLink(
  relay: OneTimeRelay,
  target: string | URL,
  options: RelayPutOptions = {},
): RelayLink | null {
  return relay.createLink(target, options);
}

export function createInMemoryRelayStore(
  options: RelayStoreOptions = {},
): InMemoryRelayStore {
  return new InMemoryRelayStore(options);
}

export const createRelayStore = createInMemoryRelayStore;

/** Pure convenience wrapper around `OneTimeRelay.handleRequest`. */
export function handleRelayRequest(
  relay: OneTimeRelay,
  input: unknown,
  options: { method?: string } = {},
): RelayResponse {
  return relay.handleRequest(input, options);
}

/** Builds a fetch-style Response from a relay result. */
export function createRelayResponse(
  relay: OneTimeRelay,
  input: unknown,
  options: { method?: string } = {},
): unknown {
  const result = relay.handleRequest(input, options);
  const ResponseCtor = (globalThis as { Response?: unknown }).Response;
  const HeadersCtor = (globalThis as { Headers?: unknown }).Headers;
  if (typeof ResponseCtor !== "function" || typeof HeadersCtor !== "function") {
    return result;
  }
  const headers = new (HeadersCtor as new (init?: unknown) => Headers)(
    result.headers,
  );
  return new (
    ResponseCtor as new (
      body?: unknown,
      init?: { status?: number; headers?: unknown },
    ) => unknown
  )(result.body ?? null, { status: result.status, headers });
}

function writeNodeResponse(
  res: unknown,
  result: RelayResponse,
  method: string,
): void {
  if (res === null || typeof res !== "object") return;
  const response = res as {
    statusCode?: number;
    setHeader?: (name: string, value: string) => void;
    end?: (body?: string) => void;
  };
  response.statusCode = result.status;
  if (typeof response.setHeader === "function") {
    for (const [name, value] of Object.entries(result.headers)) {
      response.setHeader(name, value);
    }
  }
  if (typeof response.end === "function") {
    response.end(method === "HEAD" ? "" : (result.body ?? ""));
  }
}

/**
 * Node/Express handler for the relay route. Calls `next` when the request is
 * not the relay path; callers can mount it globally or at the relay path.
 */
export function createRelayHandler(
  relay: OneTimeRelay,
): (req: unknown, res: unknown, next?: () => void) => boolean {
  return function relayHandler(req, res, next) {
    applyReferrerPolicy(res, req);
    const target = getRequestTarget(req);
    if (!relay.matchesPath(target)) {
      if (typeof next === "function") next();
      return false;
    }

    const method =
      typeof (req as { method?: unknown })?.method === "string"
        ? String((req as { method: string }).method)
        : "GET";
    const result = relay.handleRequest(target, { method });
    writeNodeResponse(res, result, method.toUpperCase());
    return true;
  };
}

export const relayHandler = createRelayHandler;

/** True when the request path is the relay's consume endpoint. */
export function isRelayPath(
  input: unknown,
  mountPath = "/__referrer-relay",
): boolean {
  return getPathname(input) === normalizeMountPath(mountPath);
}
