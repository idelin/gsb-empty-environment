/**
 * Safe redirect construction for the login flow.
 *
 * The only supported way to produce a Location header is
 * `createRedirectResponse()`. It validates the untrusted `next` value before
 * parsing it, resolves it against the configured site, normalizes the result,
 * and then creates a response whose Location header is set exactly once.
 */

export type RedirectStatus = 301 | 302 | 303 | 307 | 308;

export type HeaderValue = string | number;

export type HeaderInput =
  | Record<string, HeaderValue>
  | Iterable<readonly [string, HeaderValue]>;

export interface RedirectOptions {
  /** Absolute URL of the login service. Relative targets are resolved against it. */
  baseUrl?: string | URL;
  /** Absolute origins (or absolute URLs) that may be used as redirect targets. */
  allowedOrigins?: string | URL | Iterable<string | URL>;
  /** Alias for `allowedOrigins`. Entries may be origins or absolute URL prefixes. */
  allowlist?: string | URL | Iterable<string | URL>;
  /** Backward-compatible alias for origins-only allowlisting. */
  allowedHosts?: string | Iterable<string>;
  /** Safe destination used when `next` is missing or invalid. Defaults to `/`. */
  fallback?: string;
  /** Redirect status. Only 301, 302, 303, 307, and 308 are accepted. */
  status?: RedirectStatus;
  /** Additional response headers, validated before they are attached. */
  headers?: HeaderInput;
  /** Optional response body. Content-Length is always derived from it. */
  body?: string;
  /** Maximum accepted length of the raw `next` value. Defaults to 8192. */
  maxLength?: number;
}

export interface RedirectRequest extends RedirectOptions {
  /** Untrusted value from the `next` query parameter. */
  next?: string | null;
}

export type RedirectPolicy = RedirectOptions;
export type RedirectConfig = RedirectOptions;

export type RedirectOptionsInput =
  | RedirectOptions
  | string
  | URL
  | Iterable<string | URL>;

export interface RedirectResponse {
  status: RedirectStatus;
  /** Convenience alias used by several Node HTTP APIs. */
  statusCode: RedirectStatus;
  statusText: string;
  headers: Readonly<Record<string, string>>;
  body: string;
}

export type RedirectResult = RedirectResponse;

export type RedirectErrorCode =
  | "INVALID_OPTIONS"
  | "INVALID_TARGET"
  | "INVALID_STATUS"
  | "INVALID_HEADER_NAME"
  | "INVALID_HEADER_VALUE"
  | "DUPLICATE_HEADER"
  | "DUPLICATE_LOCATION"
  | "INVALID_RESPONSE";

export class RedirectError extends Error {
  readonly code: RedirectErrorCode;

  constructor(code: RedirectErrorCode, message: string) {
    super(message);
    this.name = "RedirectError";
    this.code = code;
  }
}

export interface HttpResponseLike {
  statusCode?: number;
  statusMessage?: string;
  setHeader?(name: string, value: string): unknown;
  removeHeader?(name: string): void;
  getHeaderNames?(): string[];
  writeHead?(
    statusCode: number,
    headers?: Record<string, string>,
  ): unknown;
  end?(chunk?: string): unknown;
}

export interface LoginRequestLike {
  url?: string;
  headers?: { host?: string | string[] };
  /** Optional pre-parsed query object, for Express/Fastify-style adapters. */
  query?: URLSearchParams | {
    next?: string | string[] | null | undefined;
  };
}

const REDIRECT_STATUS_TEXT: Record<RedirectStatus, string> = {
  301: "Moved Permanently",
  302: "Found",
  303: "See Other",
  307: "Temporary Redirect",
  308: "Permanent Redirect",
};

const DEFAULT_BASE_URL = "https://login.invalid/";
const DEFAULT_FALLBACK = "/";
const DEFAULT_MAX_LENGTH = 8192;

const SCHEME_RE = /^[A-Za-z][A-Za-z0-9+.-]*:/;
const HEADER_NAME_RE = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

/** C0 controls, DEL, C1 controls, Unicode line/paragraph separators and BOM. */
const FORBIDDEN_TEXT_RE =
  /[\u0000-\u001f\u007f-\u009f\u2028\u2029\ufeff]/u;
const PERCENT_TRIPLET_RE = /%[0-9A-Fa-f]{2}/;
const ENCODED_BACKSLASH_RE = /%5c/iu;

const PATH_ALLOWED = new Set(
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~!$&'()*+,;=:@/",
);
const QUERY_ALLOWED = new Set(
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~!$&'()*+,;=:@/?",
);
const UNRESERVED = new Set(
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~",
);

interface AllowRule {
  origin: string;
  pathPrefix?: string;
}

interface ResolvedPolicy {
  baseUrl: URL;
  rules: AllowRule[];
  fallback: string;
  status: RedirectStatus;
  maxLength: number;
}

interface ParsedTarget {
  url: URL;
  absolute: boolean;
}

function hasOwn(object: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(object, key);
}

function coerceOptions(
  value: RedirectOptionsInput | undefined,
): RedirectOptions {
  if (value === undefined || value === null) {
    return {};
  }
  if (typeof value === "string") {
    return { baseUrl: value };
  }
  if (value instanceof URL) {
    return { baseUrl: value };
  }
  if (
    typeof value === "object" &&
    !hasOwn(value, "baseUrl") &&
    !hasOwn(value, "allowedOrigins") &&
    !hasOwn(value, "allowlist") &&
    !hasOwn(value, "allowedHosts") &&
    typeof (value as { [Symbol.iterator]?: unknown })[Symbol.iterator] ===
      "function"
  ) {
    return { allowlist: value as Iterable<string | URL> };
  }
  return value as RedirectOptions;
}

function isRedirectStatus(value: unknown): value is RedirectStatus {
  return (
    value === 301 ||
    value === 302 ||
    value === 303 ||
    value === 307 ||
    value === 308
  );
}

function parseBaseUrl(value: string | URL | undefined): URL {
  try {
    const url = value instanceof URL ? new URL(value.href) : new URL(value ?? DEFAULT_BASE_URL);
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      throw new RedirectError(
        "INVALID_OPTIONS",
        "baseUrl must use http or https",
      );
    }
    if (url.username !== "" || url.password !== "") {
      throw new RedirectError(
        "INVALID_OPTIONS",
        "baseUrl must not contain credentials",
      );
    }
    return url;
  } catch (error) {
    if (error instanceof RedirectError) {
      throw error;
    }
    throw new RedirectError("INVALID_OPTIONS", "baseUrl is not a valid URL");
  }
}

function canonicalPath(value: string): string {
  return removeDotSegments(canonicalizeComponent(value, PATH_ALLOWED));
}

function parseAllowRule(
  value: string | URL,
  defaultProtocol: "http:" | "https:" = "https:",
): AllowRule {
  let input = value instanceof URL ? value.href : String(value).trim();

  if (input === "" || input.includes("*")) {
    throw new RedirectError(
      "INVALID_OPTIONS",
      "allowlist entries must be concrete origins or absolute URLs",
    );
  }
  if (!SCHEME_RE.test(input)) {
    input = `${defaultProtocol}//${input}`;
  }

  let url: URL;
  try {
    url = new URL(input);
  } catch {
    throw new RedirectError(
      "INVALID_OPTIONS",
      "allowlist entry is not a valid URL",
    );
  }

  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new RedirectError(
      "INVALID_OPTIONS",
      "allowlist entries must use http or https",
    );
  }
  if (url.username !== "" || url.password !== "") {
    throw new RedirectError(
      "INVALID_OPTIONS",
      "allowlist entries must not contain credentials",
    );
  }

  const path =
    url.pathname === "" || url.pathname === "/"
      ? undefined
      : canonicalPath(url.pathname);

  return { origin: url.origin, pathPrefix: path };
}

function addAllowRules(
  target: AllowRule[],
  values: string | URL | Iterable<string | URL> | undefined,
  defaultProtocol: "http:" | "https:" = "https:",
): void {
  if (values === undefined) {
    return;
  }
  if (typeof values === "string" || values instanceof URL) {
    target.push(parseAllowRule(values, defaultProtocol));
    return;
  }
  for (const value of values) {
    target.push(parseAllowRule(value, defaultProtocol));
  }
}

function resolvePolicy(options: RedirectOptionsInput): ResolvedPolicy {
  options = coerceOptions(options);

  const baseUrl = parseBaseUrl(options.baseUrl);
  const rules: AllowRule[] = [{ origin: baseUrl.origin }];

  const defaultProtocol = baseUrl.protocol === "http:" ? "http:" : "https:";
  addAllowRules(rules, options.allowedOrigins, defaultProtocol);
  addAllowRules(rules, options.allowlist, defaultProtocol);

  if (options.allowedHosts !== undefined) {
    addAllowRules(rules, options.allowedHosts, defaultProtocol);
  }

  const fallback = validateFallback(
    options.fallback ?? DEFAULT_FALLBACK,
    baseUrl,
    rules,
  );

  const status = options.status ?? 302;
  if (!isRedirectStatus(status)) {
    throw new RedirectError(
      "INVALID_STATUS",
      "status must be 301, 302, 303, 307, or 308",
    );
  }

  const maxLength = options.maxLength ?? DEFAULT_MAX_LENGTH;
  if (!Number.isSafeInteger(maxLength) || maxLength <= 0) {
    throw new RedirectError(
      "INVALID_OPTIONS",
      "maxLength must be a positive integer",
    );
  }

  return { baseUrl, rules, fallback, status, maxLength };
}

function validateFallback(
  value: string,
  baseUrl: URL,
  rules: AllowRule[],
): string {
  try {
    assertSafeRawTarget(value, DEFAULT_MAX_LENGTH);
    const target = parseTarget(value, baseUrl, true);
    if (!isAllowedTarget(target, rules)) {
      throw new Error("fallback is not same-site or allowlisted");
    }
    const location = serializeTarget(target, target.absolute);
    if (location.startsWith("//")) {
      throw new Error("network-path reference");
    }
    return location;
  } catch (error) {
    const reason = error instanceof Error ? error.message : "invalid value";
    throw new RedirectError(
      "INVALID_OPTIONS",
      `fallback must be a safe same-site path: ${reason}`,
    );
  }
}

/**
 * Validate the raw value before URL parsing. WHATWG URL parsing silently
 * removes tabs/newlines, so doing this afterwards would be too late.
 */
function assertSafeRawTarget(value: string, maxLength: number): void {
  if (typeof value !== "string") {
    throw new RedirectError("INVALID_TARGET", "redirect target must be a string");
  }
  if (value === "" || value.length > maxLength) {
    throw new RedirectError("INVALID_TARGET", "redirect target is empty or too long");
  }
  if (value.trim() !== value) {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target must not have leading or trailing whitespace",
    );
  }
  const wellFormed = (value as string & { isWellFormed?: () => boolean })
    .isWellFormed;
  if (typeof wellFormed === "function" && !wellFormed.call(value)) {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target contains an invalid Unicode sequence",
    );
  }
  if (FORBIDDEN_TEXT_RE.test(value) || value.includes("\\")) {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target contains a control character or backslash",
    );
  }
  if (value.startsWith("//")) {
    throw new RedirectError(
      "INVALID_TARGET",
      "protocol-relative redirect targets are not allowed",
    );
  }

  for (let index = 0; index < value.length; index += 1) {
    if (value.charCodeAt(index) !== 0x25) {
      continue;
    }
    const hex = value.slice(index + 1, index + 3);
    if (!/^[0-9A-Fa-f]{2}$/.test(hex)) {
      throw new RedirectError(
        "INVALID_TARGET",
        "redirect target contains a bare percent sign",
      );
    }
    index += 2;
  }

  if (ENCODED_BACKSLASH_RE.test(value)) {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target must not contain an encoded backslash",
    );
  }

  let decoded: string;
  try {
    decoded = decodeURIComponent(value);
  } catch {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target contains invalid percent-encoded UTF-8",
    );
  }

  if (FORBIDDEN_TEXT_RE.test(decoded) || decoded.includes("\\")) {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target decodes to a control character or backslash",
    );
  }
  if (decoded.startsWith("//")) {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target decodes to a protocol-relative URL",
    );
  }
  if (
    /^https?:\/\//i.test(decoded) &&
    !/^https?:\/\//i.test(value)
  ) {
    throw new RedirectError(
      "INVALID_TARGET",
      "absolute redirect target hides its authority separator with encoding",
    );
  }
  if (PERCENT_TRIPLET_RE.test(decoded)) {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target contains multiple layers of percent encoding",
    );
  }
}

function parseTarget(
  value: string,
  baseUrl: URL,
  allowAbsolute: boolean,
): ParsedTarget {
  const absolute = SCHEME_RE.test(value);

  if (absolute) {
    const scheme = value.slice(0, value.indexOf(":")).toLowerCase();
    if (scheme !== "http" && scheme !== "https") {
      throw new RedirectError(
        "INVALID_TARGET",
        "redirect target uses a disallowed URL scheme",
      );
    }
  } else if (value.startsWith("//")) {
    throw new RedirectError(
      "INVALID_TARGET",
      "protocol-relative redirect targets are not allowed",
    );
  }

  let url: URL;
  try {
    url = new URL(value, baseUrl);
  } catch {
    throw new RedirectError("INVALID_TARGET", "redirect target is not a valid URL");
  }

  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target uses a disallowed URL scheme",
    );
  }
  if (url.username !== "" || url.password !== "") {
    throw new RedirectError(
      "INVALID_TARGET",
      "redirect target must not contain credentials",
    );
  }
  if (absolute && !allowAbsolute) {
    throw new RedirectError(
      "INVALID_TARGET",
      "absolute redirect targets are not allowed here",
    );
  }

  return { url, absolute };
}

function pathMatchesPrefix(pathname: string, prefix: string): boolean {
  if (pathname === prefix) {
    return true;
  }
  if (!pathname.startsWith(prefix)) {
    return false;
  }
  return prefix.endsWith("/") || pathname.charAt(prefix.length) === "/";
}

function isAllowedTarget(target: ParsedTarget, rules: AllowRule[]): boolean {
  const { url, absolute } = target;

  if (!absolute) {
    return true;
  }

  const encodedPathSeparator = /%(?:2f|5c)/iu.test(url.pathname);

  for (const rule of rules) {
    if (url.origin !== rule.origin) {
      continue;
    }
    if (rule.pathPrefix === undefined) {
      return true;
    }
    if (encodedPathSeparator) {
      continue;
    }
    const pathname = canonicalPath(url.pathname);
    if (pathMatchesPrefix(pathname, rule.pathPrefix)) {
      return true;
    }
  }

  return false;
}

function removeDotSegments(input: string): string {
  let path = input;
  let output = "";

  while (path.length > 0) {
    if (path.startsWith("../")) {
      path = path.slice(3);
    } else if (path.startsWith("./")) {
      path = path.slice(2);
    } else if (path.startsWith("/./")) {
      path = `/${path.slice(3)}`;
    } else if (path === "/.") {
      path = "/";
    } else if (path.startsWith("/../")) {
      path = `/${path.slice(4)}`;
      output = removeLastSegment(output);
    } else if (path === "/..") {
      path = "/";
      output = removeLastSegment(output);
    } else if (path === "." || path === "..") {
      path = "";
    } else {
      const nextSlash = path.indexOf("/", path.startsWith("/") ? 1 : 0);
      if (nextSlash === -1) {
        output += path;
        path = "";
      } else {
        output += path.slice(0, nextSlash);
        path = path.slice(nextSlash);
      }
    }
  }

  return output;
}

function removeLastSegment(output: string): string {
  const slash = output.lastIndexOf("/");
  return slash < 0 ? "" : output.slice(0, slash);
}

function percentDecodeUnreserved(value: string): string | undefined {
  const hex = value.slice(1);
  const decoded = String.fromCharCode(Number.parseInt(hex, 16));
  return UNRESERVED.has(decoded) ? decoded : undefined;
}

function canonicalizeComponent(value: string, allowed: Set<string>): string {
  let output = "";

  for (let index = 0; index < value.length; ) {
    const codePoint = value.codePointAt(index);
    if (codePoint === undefined) {
      break;
    }
    const character = String.fromCodePoint(codePoint);

    if (character === "%") {
      const hex = value.slice(index + 1, index + 3);
      if (!/^[0-9A-Fa-f]{2}$/.test(hex)) {
        throw new RedirectError(
          "INVALID_TARGET",
          "redirect target contains a bare percent sign",
        );
      }
      const decoded = percentDecodeUnreserved(`%${hex}`);
      output += decoded ?? `%${hex.toUpperCase()}`;
      index += 2;
    } else if (codePoint < 0x80 && allowed.has(character)) {
      output += character;
    } else {
      output += encodeURIComponent(character);
    }

    index += character.length;
  }

  return output;
}

function serializeTarget(target: ParsedTarget, absolute: boolean): string {
  const { url } = target;
  let pathname = canonicalPath(url.pathname);

  if (pathname === "") {
    pathname = "/";
  }
  if (pathname.startsWith("//")) {
    throw new RedirectError(
      "INVALID_TARGET",
      "normalized redirect target is a network-path reference",
    );
  }

  const query = url.search === "" ? "" : `?${canonicalizeComponent(url.search.slice(1), QUERY_ALLOWED)}`;
  const hash = url.hash === "" ? "" : `#${canonicalizeComponent(url.hash.slice(1), QUERY_ALLOWED)}`;

  if (!absolute) {
    return `${pathname}${query}${hash}`;
  }

  return `${url.protocol}//${url.host}${pathname}${query}${hash}`;
}

/**
 * Returns a validated, RFC 3986-normalized Location value.
 *
 * Invalid targets are replaced with the configured fallback. The fallback is
 * configuration, not user input, and is validated when the policy is built.
 */
export function buildRedirectLocation(
  next: string | null | undefined,
  options: RedirectOptionsInput = {},
): string {
  const policy = resolvePolicy(options);
  return buildLocationWithPolicy(next, policy);
}

function resolveLocationWithPolicy(
  next: string | null | undefined,
  policy: ResolvedPolicy,
): string {
  if (next === undefined || next === null) {
    throw new RedirectError("INVALID_TARGET", "redirect target is missing");
  }

  assertSafeRawTarget(next, policy.maxLength);
  const target = parseTarget(next, policy.baseUrl, true);
  if (!isAllowedTarget(target, policy.rules)) {
    throw new RedirectError(
      "INVALID_TARGET",
      "absolute redirect target is not on the allowlist",
    );
  }
  return serializeTarget(target, target.absolute);
}

function buildLocationWithPolicy(
  next: string | null | undefined,
  policy: ResolvedPolicy,
): string {
  if (next === undefined || next === null) {
    return policy.fallback;
  }

  try {
    return resolveLocationWithPolicy(next, policy);
  } catch {
    return policy.fallback;
  }
}

/** Alias that makes the security intent explicit at call sites. */
export const normalizeRedirectTarget = buildRedirectLocation;
export const safeRedirectLocation = buildRedirectLocation;

/**
 * Strict variant of `buildRedirectLocation`: invalid targets throw instead of
 * silently falling back.
 */
export function assertSafeRedirectTarget(
  next: string | null | undefined,
  options: RedirectOptionsInput = {},
): string {
  const policy = resolvePolicy(options);
  return resolveLocationWithPolicy(next, policy);
}

export const validateRedirectTarget = assertSafeRedirectTarget;

export function isValidHeaderName(name: string): boolean {
  return typeof name === "string" && HEADER_NAME_RE.test(name);
}

export function isValidHeaderValue(value: string): boolean {
  return (
    typeof value === "string" &&
    /^[\x20-\x7e\xa0-\xff]*$/.test(value) &&
    !FORBIDDEN_TEXT_RE.test(value) &&
    !value.includes("\r") &&
    !value.includes("\n")
  );
}

export function validateHeaderName(name: string): string {
  if (!isValidHeaderName(name)) {
    throw new RedirectError(
      "INVALID_HEADER_NAME",
      `invalid HTTP header name: ${JSON.stringify(name)}`,
    );
  }
  return name;
}

export function validateHeaderValue(name: string, value: unknown): string {
  const text = typeof value === "number" ? String(value) : value;
  if (typeof text !== "string" || !isValidHeaderValue(text)) {
    throw new RedirectError(
      "INVALID_HEADER_VALUE",
      `invalid value for HTTP header ${JSON.stringify(name)}`,
    );
  }
  return text;
}

function headerEntries(headers: HeaderInput): Array<[string, string]> {
  if (headers === null || typeof headers !== "object") {
    throw new RedirectError("INVALID_OPTIONS", "headers must be an object or iterable");
  }

  const entries: Array<[string, string]> = [];

  if (typeof (headers as { forEach?: unknown }).forEach === "function" &&
      typeof (headers as { entries?: unknown }).entries === "function" &&
      !Array.isArray(headers)) {
    (headers as unknown as {
      forEach(callback: (value: unknown, key: unknown) => void): void;
    })
      .forEach((value, key) => {
        entries.push([String(key), validateHeaderValue(String(key), value)]);
      });
    return entries;
  }

  if (typeof (headers as Iterable<readonly [string, HeaderValue]>)[Symbol.iterator] === "function") {
    for (const pair of headers as Iterable<readonly [string, HeaderValue]>) {
      if (!Array.isArray(pair) || pair.length !== 2) {
        throw new RedirectError(
          "INVALID_OPTIONS",
          "header iterables must contain [name, value] pairs",
        );
      }
      entries.push([String(pair[0]), validateHeaderValue(String(pair[0]), pair[1])]);
    }
    return entries;
  }

  for (const [name, value] of Object.entries(headers)) {
    entries.push([name, validateHeaderValue(name, value)]);
  }
  return entries;
}

/**
 * Validate a header collection and return a normalized copy. Header names are
 * compared case-insensitively; duplicate names are rejected.
 */
export function validateHeaders(
  headers: HeaderInput | undefined,
): Record<string, string> {
  if (headers === undefined) {
    return {};
  }

  const seen = new Set<string>();
  const output: Record<string, string> = {};

  for (const [rawName, rawValue] of headerEntries(headers)) {
    const name = validateHeaderName(rawName);
    const value = validateHeaderValue(name, rawValue);
    const key = name.toLowerCase();
    if (seen.has(key)) {
      if (key === "location") {
        throw new RedirectError(
          "DUPLICATE_LOCATION",
          "a response may contain only one Location header",
        );
      }
      throw new RedirectError(
        "DUPLICATE_HEADER",
        `duplicate HTTP header: ${name}`,
      );
    }
    seen.add(key);
    setHeaderValue(output, name, value);
  }

  return output;
}

function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length;
}

function setHeaderValue(
  headers: Record<string, string>,
  name: string,
  value: string,
): void {
  Object.defineProperty(headers, name, {
    value,
    enumerable: true,
    configurable: true,
    writable: true,
  });
}

function createResponse(
  location: string,
  status: RedirectStatus,
  extras: HeaderInput | undefined,
  body: string,
): RedirectResponse {
  if (typeof body !== "string") {
    throw new RedirectError("INVALID_OPTIONS", "body must be a string");
  }

  const headers: Record<string, string> = {};
  const seen = new Set<string>();

  setHeaderValue(
    headers,
    "Location",
    validateHeaderValue("Location", location),
  );
  seen.add("location");

  for (const [name, value] of headerEntries(extras ?? {})) {
    const validName = validateHeaderName(name);
    const validValue = validateHeaderValue(validName, value);
    const key = validName.toLowerCase();

    if (key === "location") {
      throw new RedirectError(
        "DUPLICATE_LOCATION",
        "Location is owned by the redirect builder and cannot be supplied again",
      );
    }
    if (key === "transfer-encoding") {
      throw new RedirectError(
        "INVALID_HEADER_VALUE",
        "Transfer-Encoding is not allowed because the redirect body is length-delimited",
      );
    }
    if (seen.has(key)) {
      throw new RedirectError(
        "DUPLICATE_HEADER",
        `duplicate HTTP header: ${validName}`,
      );
    }

    if (key === "content-length") {
      const declared = Number(validValue);
      if (!Number.isSafeInteger(declared) || declared !== utf8Length(body)) {
        throw new RedirectError(
          "INVALID_HEADER_VALUE",
          "Content-Length does not match the response body",
        );
      }
      setHeaderValue(headers, "Content-Length", String(utf8Length(body)));
    } else if (key === "content-type") {
      setHeaderValue(headers, "Content-Type", validValue);
    } else {
      setHeaderValue(headers, validName, validValue);
    }
    seen.add(key);
  }

  if (body.length > 0 && !seen.has("content-type")) {
    setHeaderValue(headers, "Content-Type", "text/plain; charset=utf-8");
    seen.add("content-type");
  }
  if (!seen.has("content-length")) {
    setHeaderValue(headers, "Content-Length", String(utf8Length(body)));
  }

  return {
    status,
    statusCode: status,
    statusText: REDIRECT_STATUS_TEXT[status] ?? "Redirect",
    headers,
    body,
  };
}

/**
 * Construct a redirect response. This is the only API that sets Location.
 */
export function createRedirectResponse(
  next: string | null | undefined,
  options?: RedirectOptionsInput,
): RedirectResponse;
export function createRedirectResponse(
  request: RedirectRequest,
): RedirectResponse;
export function createRedirectResponse(
  nextOrRequest: string | null | undefined | RedirectRequest,
  maybeOptions: RedirectOptionsInput = {},
): RedirectResponse {
  let next: string | null | undefined;
  let optionsInput: RedirectOptionsInput;

  if (
    nextOrRequest !== null &&
    typeof nextOrRequest === "object" &&
    !(nextOrRequest instanceof URL)
  ) {
    const request = nextOrRequest as RedirectRequest;
    next = request.next;
    optionsInput = request;
  } else {
    next = nextOrRequest as string | null | undefined;
    optionsInput = maybeOptions;
  }

  const options = coerceOptions(optionsInput);
  const policy = resolvePolicy(options);
  const location = buildLocationWithPolicy(next, policy);

  let body = options.body ?? "";
  if (typeof body !== "string") {
    throw new RedirectError("INVALID_OPTIONS", "body must be a string");
  }

  return createResponse(location, policy.status, options.headers, body);
}

/** Alias kept for call sites that use the component's purpose as its name. */
export const buildRedirect = createRedirectResponse;
export const createRedirect = createRedirectResponse;
export const createLoginRedirect = createRedirectResponse;
export const buildRedirectResponse = createRedirectResponse;
export const safeRedirect = createRedirectResponse;

/**
 * Validate a constructed response immediately before it is written. This
 * catches accidental mutation between construction and send time.
 */
export function validateRedirectResponse(
  response: Pick<RedirectResponse, "status" | "headers" | "body">,
): void {
  if (!isRedirectStatus(response.status)) {
    throw new RedirectError(
      "INVALID_RESPONSE",
      "response status is not a redirect status",
    );
  }
  if (typeof response.body !== "string") {
    throw new RedirectError("INVALID_RESPONSE", "response body must be a string");
  }

  const validated = validateHeaders(response.headers);
  if (Object.keys(validated).some((name) => name.toLowerCase() === "transfer-encoding")) {
    throw new RedirectError(
      "INVALID_RESPONSE",
      "redirect responses must not use Transfer-Encoding",
    );
  }
  const locationKeys = Object.keys(validated).filter(
    (name) => name.toLowerCase() === "location",
  );
  if (locationKeys.length !== 1) {
    throw new RedirectError(
      locationKeys.length === 0 ? "INVALID_RESPONSE" : "DUPLICATE_LOCATION",
      "redirect response must contain exactly one Location header",
    );
  }

  const location = validated[locationKeys[0]];
  if (location === undefined) {
    throw new RedirectError(
      "INVALID_RESPONSE",
      "redirect response must contain a Location header",
    );
  }
  try {
    assertSafeRawTarget(location, DEFAULT_MAX_LENGTH);
    if (
      !location.startsWith("/") &&
      !/^https?:\/\//i.test(location)
    ) {
      throw new RedirectError(
        "INVALID_RESPONSE",
        "Location must be a same-site path or an absolute http(s) URL",
      );
    }
  } catch (error) {
    if (error instanceof RedirectError && error.code === "INVALID_RESPONSE") {
      throw error;
    }
    throw new RedirectError(
      "INVALID_RESPONSE",
      "Location contains unsafe characters",
    );
  }

  const lengthKey = Object.keys(validated).find(
    (name) => name.toLowerCase() === "content-length",
  );
  if (lengthKey !== undefined) {
    const declared = Number(validated[lengthKey]);
    if (!Number.isSafeInteger(declared) || declared !== utf8Length(response.body)) {
      throw new RedirectError(
        "INVALID_RESPONSE",
        "Content-Length does not match the response body",
      );
    }
  }
}

function removeExistingLocation(response: HttpResponseLike): void {
  if (typeof response.removeHeader !== "function") {
    return;
  }
  if (typeof response.getHeaderNames === "function") {
    for (const name of response.getHeaderNames()) {
      if (name.toLowerCase() === "location") {
        response.removeHeader(name);
      }
    }
    return;
  }
  response.removeHeader("Location");
}

function isRedirectResponse(value: unknown): value is RedirectResponse {
  return (
    value !== null &&
    typeof value === "object" &&
    typeof (value as { status?: unknown }).status === "number" &&
    typeof (value as { body?: unknown }).body === "string" &&
    "headers" in value &&
    !("setHeader" in value)
  );
}

/**
 * Send a constructed redirect through a Node-compatible response object.
 * Headers are re-validated here, immediately before transmission.
 */
export function sendRedirect(
  response: HttpResponseLike,
  redirect: RedirectResponse,
): void;
export function sendRedirect(
  redirect: RedirectResponse,
  response: HttpResponseLike,
): void;
export function sendRedirect(
  response: HttpResponseLike,
  next: string | null | undefined | RedirectRequest,
  options?: RedirectOptionsInput,
): void;
export function sendRedirect(
  first: HttpResponseLike | RedirectResponse,
  second: RedirectResponse | string | null | undefined | RedirectRequest,
  options?: RedirectOptionsInput,
): void {
  let response: HttpResponseLike;
  let redirect: RedirectResponse;

  if (
    second !== null &&
    typeof second === "object" &&
    !(second instanceof URL) &&
    ("setHeader" in second || "writeHead" in second)
  ) {
    response = second as HttpResponseLike;
    redirect = first as RedirectResponse;
  } else {
    response = first as HttpResponseLike;
    if (isRedirectResponse(second)) {
      redirect = second;
    } else if (
      typeof second === "string" ||
      second === null ||
      second === undefined
    ) {
      redirect = createRedirectResponse(second, options);
    } else {
      redirect = createRedirectResponse(second);
    }
  }

  if (response === null || typeof response !== "object") {
    throw new RedirectError("INVALID_OPTIONS", "response object is required");
  }
  validateRedirectResponse(redirect);
  const headers = validateHeaders(redirect.headers);

  removeExistingLocation(response);

  if (typeof response.setHeader === "function") {
    response.statusCode = redirect.status;
    if (typeof response.statusMessage === "string" || "statusMessage" in response) {
      response.statusMessage = redirect.statusText;
    }
    for (const [name, value] of Object.entries(headers)) {
      response.setHeader(name, value);
    }
    if (typeof response.end === "function") {
      response.end(redirect.body);
    }
    return;
  }

  if (typeof response.writeHead === "function") {
    response.writeHead(redirect.status, headers);
    if (typeof response.end === "function") {
      response.end(redirect.body);
    }
    return;
  }

  throw new RedirectError(
    "INVALID_OPTIONS",
    "response object must expose setHeader or writeHead",
  );
}

export const writeRedirect = sendRedirect;

/**
 * Optional login handler: reads `next` from the request query and writes one
 * validated redirect response.
 */
export function createLoginRedirectHandler(
  optionsInput: RedirectOptionsInput = {},
): (request: LoginRequestLike, response: HttpResponseLike) => void {
  const options = coerceOptions(optionsInput);
  const policy = resolvePolicy(options);

  return (request, response) => {
    const next = readNextParameter(request, policy.baseUrl);

    const location = buildLocationWithPolicy(next, policy);
    const body = options.body ?? "";
    sendRedirect(
      response,
      createResponse(location, policy.status, options.headers, body),
    );
  };
}

function readNextParameter(
  request: LoginRequestLike,
  baseUrl: URL,
): string | null {
  if (request.query !== undefined) {
    if (request.query instanceof URLSearchParams) {
      const values = request.query.getAll("next");
      if (values.length === 1) {
        return values[0] ?? null;
      }
      if (values.length > 1) {
        return null;
      }
    }
    if (request.query !== null && typeof request.query === "object") {
      const value = request.query.next;
      if (typeof value === "string") {
        return value;
      }
      if (Array.isArray(value)) {
        const first = value[0];
        return value.length === 1 && typeof first === "string" ? first : null;
      }
    }
  }

  try {
    const requestUrl = new URL(request.url ?? "/", baseUrl);
    const values = requestUrl.searchParams.getAll("next");
    return values.length === 1 ? (values[0] ?? null) : null;
  } catch {
    return null;
  }
}

export default {
  buildRedirectLocation,
  normalizeRedirectTarget,
  safeRedirectLocation,
  createRedirectResponse,
  buildRedirect,
  createRedirect,
  createLoginRedirect,
  buildRedirectResponse,
  safeRedirect,
  assertSafeRedirectTarget,
  validateRedirectTarget,
  validateRedirectResponse,
  validateHeaderName,
  validateHeaderValue,
  validateHeaders,
  isValidHeaderName,
  isValidHeaderValue,
  sendRedirect,
  writeRedirect,
  createLoginRedirectHandler,
  RedirectError,
};
