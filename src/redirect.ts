import {
  getRequestTarget,
  normalizePath,
} from "./paths.ts";
import {
  hasSensitiveParams,
  stripSensitiveParams,
  type SensitiveParamOptions,
} from "./params.ts";
import { isOriginAllowed } from "./origins.ts";
import {
  applyReferrerPolicy,
  REDIRECT_REFERRER_POLICY,
  type ReferrerPolicyOptions,
} from "./policy.ts";
import type { OneTimeRelay } from "./relay.ts";

export type RedirectExternalMode = "direct" | "relay" | "block";

export interface RedirectHandlerOptions extends ReferrerPolicyOptions {
  /** Query parameters accepted as the destination. */
  targetParams?: readonly string[];
  /** Optional destination origin allow-list. */
  allowedOrigins?: readonly string[];
  /** Defaults to true when `allowedOrigins` is omitted. */
  allowAnyExternal?: boolean;
  /** How external destinations are reached. Defaults to relay when available. */
  externalMode?: RedirectExternalMode;
  relay?: OneTimeRelay;
  /** Only handle this path. Defaults to `/__referrer-redirect`. */
  mountPath?: string;
  statusCode?: number;
  baseUrl?: string;
  allowedProtocols?: readonly string[];
  maxTargetLength?: number;
  /** Remove the destination's entire query, not just sensitive parameters. */
  stripAllQuery?: boolean;
  /**
   * When a destination contains any token-like parameter, drop its entire
   * query instead of leaving safe parameters behind. Defaults to true.
   */
  stripQueryOnSensitive?: boolean;
}

export interface RedirectResult {
  status: number;
  headers: Record<string, string>;
  location?: string;
  body?: string;
  target?: string;
  relayUrl?: string;
  relayToken?: string;
  external?: boolean;
  reason?: string;
}

const DEFAULT_TARGET_PARAMS = [
  "url",
  "target",
  "to",
  "next",
  "redirect",
  "redirect_uri",
  "destination",
  "dest",
] as const;

const DEFAULT_REDIRECT_PATHS = [
  "/__referrer-redirect",
  "/redirect",
] as const;

function matchesRedirectPath(
  input: unknown,
  mountPath: string | undefined,
): boolean {
  const pathname = normalizePath(getRequestTarget(input));
  if (mountPath !== undefined) {
    return pathname === normalizePath(mountPath);
  }
  return DEFAULT_REDIRECT_PATHS.some(
    (candidate) => pathname === normalizePath(candidate),
  );
}

const REDIRECT_HEADERS: Record<string, string> = {
  "Referrer-Policy": REDIRECT_REFERRER_POLICY,
  "Cache-Control": "no-store",
};

function clean(value: string): string {
  return value.replace(/[\u0000-\u001f\u007f]/g, "").trim();
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

type UrlShape = "absolute" | "protocol-relative" | "relative";

function getShape(value: string): UrlShape {
  if (value.startsWith("//")) return "protocol-relative";
  return hasScheme(value) ? "absolute" : "relative";
}

function serializeShape(
  url: URL,
  shape: UrlShape,
  hadLeadingSlash: boolean,
): string {
  if (shape === "absolute") return url.href;
  if (shape === "protocol-relative") {
    return `//${url.host}${url.pathname}${url.search}${url.hash}`;
  }
  const pathname = hadLeadingSlash
    ? url.pathname
    : url.pathname.replace(/^\//, "");
  return `${pathname}${url.search}${url.hash}`;
}

function protocolAllowed(
  protocol: string,
  allowed: readonly string[],
): boolean {
  const normalizedProtocol = protocol.toLowerCase();
  return allowed.some(
    (item) => {
      const normalizedValue = String(item).toLowerCase();
      return (
        (normalizedValue.endsWith(":")
          ? normalizedValue
          : `${normalizedValue}:`) === normalizedProtocol
      );
    },
  );
}

function resultHeaders(extra: Record<string, string> = {}): Record<string, string> {
  return { ...REDIRECT_HEADERS, ...extra };
}

function requestBaseUrl(input: unknown, options: RedirectHandlerOptions): string {
  if (options.baseUrl !== undefined) return normalizeBase(options.baseUrl);

  if (input !== null && typeof input === "object") {
    const req = input as {
      url?: string;
      headers?: Record<string, string | string[] | undefined>;
      protocol?: string;
    };
    if (typeof req.url === "string" && /^https?:\/\//i.test(req.url)) {
      return req.url;
    }
    const hostValue = req.headers?.host;
    const host = Array.isArray(hostValue) ? hostValue[0] : hostValue;
    if (typeof host === "string" && host.length > 0) {
      const forwarded = req.headers?.["x-forwarded-proto"];
      const forwardedValue = Array.isArray(forwarded) ? forwarded[0] : forwarded;
      const protocol =
        req.protocol ??
        (typeof forwardedValue === "string" ? forwardedValue.split(",")[0] : undefined) ??
        "https";
      return `${protocol}://${host}`;
    }
  }

  return "https://portal.invalid";
}

function extractDestination(
  requestTarget: string,
  options: RedirectHandlerOptions,
  baseUrl: string,
): string | null {
  let parsed: URL;
  try {
    parsed = new URL(
      requestTarget.startsWith("//") ? `https:${requestTarget}` : requestTarget,
      baseUrl,
    );
  } catch {
    return null;
  }

  const params = options.targetParams ?? DEFAULT_TARGET_PARAMS;
  for (const name of params) {
    const value = parsed.searchParams.get(name);
    if (value !== null) return value;
  }
  return null;
}

/** Removes the full query string from a redirect destination. */
export function stripRedirectQuery(
  target: string | URL,
  options: SensitiveParamOptions & { keepHash?: boolean } = {},
): string {
  const raw = target instanceof URL ? target.href : clean(String(target));
  const shape = getShape(raw);
  const hadLeadingSlash = raw.startsWith("/");
  try {
    const parsed = new URL(
      raw.startsWith("//") ? `https:${raw}` : raw,
      "https://portal.invalid",
    );
    parsed.search = "";
    if (options.keepHash === true) {
      return serializeShape(parsed, shape, hadLeadingSlash);
    }
    const sanitized = new URL(stripSensitiveParams(parsed.href, options));
    return serializeShape(sanitized, shape, hadLeadingSlash);
  } catch {
    return stripSensitiveParams(raw, options);
  }
}

/** Sanitizes a destination before it is placed in a Location header. */
export function sanitizeRedirectTarget(
  target: string | URL,
  options: RedirectHandlerOptions = {},
): string | null {
  const rawInput = target instanceof URL ? target.href : String(target);
  if (/[\u0000-\u001f\u007f]/.test(rawInput)) return null;
  const raw = clean(rawInput);
  if (raw.length === 0 || raw.length > (options.maxTargetLength ?? 4_096)) {
    return null;
  }

  const shape = getShape(raw);
  const hadLeadingSlash = raw.startsWith("/");
  let parsed: URL;
  try {
    parsed = new URL(
      raw.startsWith("//") ? `https:${raw}` : raw,
      normalizeBase(options.baseUrl ?? "https://portal.invalid"),
    );
  } catch {
    return null;
  }

  const allowedProtocols = options.allowedProtocols ?? ["https:", "http:"];
  if (!protocolAllowed(parsed.protocol, allowedProtocols)) return null;
  if (parsed.username.length > 0 || parsed.password.length > 0) return null;

  if (shape !== "relative" && options.allowedOrigins !== undefined) {
    if (!isOriginAllowed(parsed.origin, options.allowedOrigins)) return null;
  }

  const hadSensitiveParams = hasSensitiveParams(parsed.href, options);
  let sanitized = stripSensitiveParams(parsed.href, {
    ...options,
    stripCredentials: true,
  });

  if (
    options.stripAllQuery === true ||
    (hadSensitiveParams && options.stripQueryOnSensitive !== false)
  ) {
    sanitized = stripRedirectQuery(sanitized, {
      ...options,
      keepHash: false,
    });
  }

  if (sanitized.length > (options.maxTargetLength ?? 4_096)) return null;

  if (shape !== "absolute") {
    try {
      return serializeShape(new URL(sanitized), shape, hadLeadingSlash);
    } catch {
      return null;
    }
  }

  return sanitized;
}

export const sanitizeRedirectUrl = sanitizeRedirectTarget;

function resolveDestination(
  requestTarget: string,
  options: RedirectHandlerOptions,
  baseUrl: string,
): {
  status: number;
  destination?: string;
  relayUrl?: string;
  relayToken?: string;
  external?: boolean;
  reason?: string;
} {
  const raw = extractDestination(requestTarget, options, baseUrl);
  if (raw === null) {
    return { status: 400, reason: "missing-target" };
  }

  const destination = sanitizeRedirectTarget(raw, {
    ...options,
    baseUrl,
    allowedOrigins: undefined,
  });
  if (destination === null) {
    return { status: 400, reason: "invalid-target" };
  }

  const destinationIsRelative =
    !hasScheme(destination) && !destination.startsWith("//");

  let parsed: URL;
  try {
    parsed = new URL(destination, baseUrl);
  } catch {
    return { status: 400, reason: "invalid-target" };
  }

  let requestOrigin: string;
  try {
    requestOrigin = new URL(
      requestTarget.startsWith("//") ? `https:${requestTarget}` : requestTarget,
      baseUrl,
    ).origin;
  } catch {
    requestOrigin = "https://portal.invalid";
  }

  const external = !destinationIsRelative && parsed.origin !== requestOrigin;
  if (!external) {
    return { status: options.statusCode ?? 302, destination, external };
  }

  if (
    options.allowedOrigins !== undefined &&
    !isOriginAllowed(parsed.origin, options.allowedOrigins)
  ) {
    return { status: 403, external: true, reason: "external-not-allowed" };
  }

  if (
    options.allowAnyExternal === false &&
    options.allowedOrigins === undefined
  ) {
    return { status: 403, external: true, reason: "external-not-allowed" };
  }

  const mode =
    options.externalMode ??
    (options.relay !== undefined ? "relay" : "direct");

  if (mode === "block") {
    return { status: 403, external: true, reason: "external-blocked" };
  }

  if (mode === "relay") {
    if (options.relay === undefined) {
      return { status: 503, external: true, reason: "relay-unavailable" };
    }
    const link = options.relay.createLink(destination);
    if (link === null) {
      return { status: 400, external: true, reason: "relay-rejected" };
    }
    return {
      status: options.statusCode ?? 302,
      destination,
      relayUrl: link.url,
      relayToken: link.token,
      external: true,
    };
  }

  return { status: options.statusCode ?? 302, destination, external: true };
}

/**
 * Pure redirect handler. The request's own query is never copied; the target
 * parameter is sanitized server-side and token-like parameters are removed.
 */
export function handleRedirectRequest(
  input: unknown,
  options: RedirectHandlerOptions = {},
): RedirectResult {
  const requestTarget = getRequestTarget(input);
  const baseUrl = requestBaseUrl(input, options);

  const pathname = normalizePath(requestTarget);
  const mountPath = normalizePath(options.mountPath ?? "/__referrer-redirect");
  if (options.mountPath !== undefined && pathname !== mountPath) {
    return { status: 404, headers: resultHeaders(), reason: "wrong-path" };
  }

  const method =
    typeof (input as { method?: unknown })?.method === "string"
      ? String((input as { method: string }).method).toUpperCase()
      : "GET";
  if (method !== "GET" && method !== "HEAD") {
    return {
      status: 405,
      headers: resultHeaders({ Allow: "GET, HEAD" }),
      body: "Method Not Allowed",
    };
  }

  const resolved = resolveDestination(requestTarget, options, baseUrl);
  if (resolved.destination === undefined) {
    const body =
      resolved.status === 403
        ? "Redirect destination is not allowed."
        : resolved.status === 503
          ? "Redirect relay is not configured."
          : "Invalid redirect target.";
    return {
      status: resolved.status,
      headers: resultHeaders({ "Content-Type": "text/plain; charset=utf-8" }),
      body,
      reason: resolved.reason,
    };
  }

  const location = resolved.relayUrl ?? resolved.destination;
  return {
    status: resolved.status,
    headers: resultHeaders({
      Location: location,
      "Content-Type": "text/plain; charset=utf-8",
    }),
    location,
    body: "Redirecting…",
    target: resolved.destination,
    relayUrl: resolved.relayUrl,
    relayToken: resolved.relayToken,
    external: resolved.external,
  };
}

/** Alias for callers that prefer "build". */
export const buildRedirectResponse = handleRedirectRequest;

/** Builds a fetch-style Response from a redirect result. */
export function createRedirectResponse(
  input: unknown,
  options: RedirectHandlerOptions = {},
): unknown {
  const result = handleRedirectRequest(input, options);
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
  result: RedirectResult,
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
 * Node/Express redirect handler. It only handles `mountPath`; other requests
 * fall through to `next`.
 */
export function createRedirectHandler(
  options: RedirectHandlerOptions = {},
): (req: unknown, res: unknown, next?: () => void) => boolean {
  return function redirectHandler(req, res, next) {
    applyReferrerPolicy(res, req, options);
    const requestTarget = getRequestTarget(req);
    if (!matchesRedirectPath(requestTarget, options.mountPath)) {
      if (typeof next === "function") next();
      return false;
    }

    const method =
      typeof (req as { method?: unknown })?.method === "string"
        ? String((req as { method: string }).method).toUpperCase()
        : "GET";
    const result = handleRedirectRequest(req, options);
    writeNodeResponse(res, result, method);
    return true;
  };
}

/** Alias used by portal wiring. */
export const createSafeRedirectHandler = createRedirectHandler;
export const redirectHandler = createRedirectHandler;
export const safeRedirect = createRedirectHandler;

/** Returns the pathname handled by the redirect endpoint. */
export function isRedirectPath(
  input: unknown,
  mountPath?: string,
): boolean {
  return matchesRedirectPath(input, mountPath);
}
