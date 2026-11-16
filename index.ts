export const LOW_ENTROPY_HINTS = Object.freeze([
  "Sec-CH-UA",
  "Sec-CH-UA-Mobile",
  "Sec-CH-UA-Platform",
] as const);

export type LowEntropyHint = (typeof LOW_ENTROPY_HINTS)[number];
export type ClientHintValues = Record<string, string>;
export type HeaderValue = string | readonly string[] | undefined | null;
export type HeaderSource =
  | Headers
  | Map<string, HeaderValue>
  | Record<string, HeaderValue>;

export interface PermissionAllowlist {
  /** The `*` source grants every origin. */
  any: boolean;
  /** The `self` source grants the top-level document origin. */
  self: boolean;
  /** Explicit origin sources from the allowlist. */
  origins: readonly string[];
}

export type ParsedPermissionsPolicy = Record<string, PermissionAllowlist>;

export type PermissionsPolicySource =
  | string
  | readonly string[]
  | Record<string, string | readonly string[]>;

export interface ClientHintsResponse {
  url: string | URL;
  headers?: HeaderSource;
  status?: number;
  /**
   * Set to `false` when the network stack knows that the response did not
   * travel over HTTPS. An HTTPS URL is otherwise sufficient.
   */
  secure?: boolean;
  /** Only top-level navigation responses may update Client Hints opt-ins. */
  isTopLevelNavigation?: boolean;
  /** Alias used by some network stacks. */
  isTopLevel?: boolean;
  /** Alias used by some network stacks. */
  topLevelNavigation?: boolean;
  /** Convenience alternative to a response header. */
  acceptCH?: HeaderValue;
  /** Convenience alternative to the `Permissions-Policy` header. */
  permissionsPolicy?: PermissionsPolicySource;
  request?: {
    url?: string | URL;
    isTopLevelNavigation?: boolean;
    isTopLevel?: boolean;
    topLevelNavigation?: boolean;
  };
}

export interface ClientHintRequest {
  url: string | URL;
  /**
   * The origin of the top-level document that initiated the request. If it is
   * omitted, the most recently observed top-level navigation is used.
   */
  topLevelOrigin?: string | URL;
  /** Alias for `topLevelOrigin`. */
  documentOrigin?: string | URL;
  /** Alias for `topLevelOrigin`. */
  initiatorOrigin?: string | URL;
  /** Alias for `topLevelOrigin` for callers that use `origin` as the initiator. */
  origin?: string | URL;
}

export interface ClientHintsOptions {
  hints?: ClientHintValues;
  /** Alias for `hints`. */
  values?: ClientHintValues;
  /**
   * Low-entropy hints are same-origin by default. Enable this to permit them
   * cross-origin when the document delegates the `ch-ua` feature.
   */
  sendLowEntropyCrossOrigin?: boolean;
}

interface DocumentClientHintsState {
  accepted: ReadonlySet<string>;
  permissionsPolicy: ParsedPermissionsPolicy;
}

const HINT_PREFIX = "sec-ch-";
const lowEntropyNames = new Set<string>(
  LOW_ENTROPY_HINTS.map((hint) => hint.toLowerCase()),
);

const canonicalHintNames: Readonly<Record<string, string>> = Object.freeze({
  "sec-ch-ua": "Sec-CH-UA",
  "sec-ch-ua-mobile": "Sec-CH-UA-Mobile",
  "sec-ch-ua-platform": "Sec-CH-UA-Platform",
  "sec-ch-ua-full-version": "Sec-CH-UA-Full-Version",
  "sec-ch-ua-full-version-list": "Sec-CH-UA-Full-Version-List",
  "sec-ch-ua-arch": "Sec-CH-UA-Arch",
  "sec-ch-ua-bitness": "Sec-CH-UA-Bitness",
  "sec-ch-ua-model": "Sec-CH-UA-Model",
  "sec-ch-ua-platform-version": "Sec-CH-UA-Platform-Version",
  "sec-ch-ua-wow64": "Sec-CH-UA-WoW64",
  "sec-ch-ua-form-factors": "Sec-CH-UA-Form-Factors",
  "sec-ch-device-memory": "Sec-CH-Device-Memory",
  "sec-ch-dpr": "Sec-CH-DPR",
  "sec-ch-viewport-width": "Sec-CH-Viewport-Width",
  "sec-ch-viewport-height": "Sec-CH-Viewport-Height",
  "sec-ch-width": "Sec-CH-Width",
  "sec-ch-downlink": "Sec-CH-Downlink",
  "sec-ch-ect": "Sec-CH-ECT",
  "sec-ch-rtt": "Sec-CH-RTT",
  "sec-ch-save-data": "Sec-CH-Save-Data",
  "sec-ch-prefers-color-scheme": "Sec-CH-Prefers-Color-Scheme",
  "sec-ch-prefers-reduced-motion": "Sec-CH-Prefers-Reduced-Motion",
  "sec-ch-prefers-reduced-transparency": "Sec-CH-Prefers-Reduced-Transparency",
  "sec-ch-prefers-contrast": "Sec-CH-Prefers-Contrast",
  "sec-ch-forced-colors": "Sec-CH-Forced-Colors",
});

const defaultAllowlist: PermissionAllowlist = Object.freeze({
  any: false,
  self: true,
  origins: Object.freeze([]),
});

export function isLowEntropyHint(hint: string): boolean {
  return lowEntropyNames.has(hint.trim().toLowerCase());
}

export function canonicalizeHintName(hint: string): string | null {
  const normalized = hint.trim().toLowerCase();
  if (!new RegExp(`^${HINT_PREFIX}[a-z0-9-]+$`).test(normalized)) {
    return null;
  }

  const canonical = canonicalHintNames[normalized];
  if (canonical) {
    return canonical;
  }

  const parts = normalized.split("-");
  return ["Sec", "CH", ...parts.slice(2).map(titleCase)].join("-");
}

/**
 * Maps a hint to its Permissions Policy feature, for example
 * `Sec-CH-UA-Model` to `ch-ua-model`.
 */
export function permissionsPolicyFeatureForHint(hint: string): string | null {
  const canonical = canonicalizeHintName(hint);
  if (!canonical) {
    return null;
  }
  return `ch-${canonical.slice("Sec-CH-".length).toLowerCase()}`;
}

export function parseAcceptCH(input: HeaderValue): string[] {
  const result: string[] = [];
  const seen = new Set<string>();

  for (const value of headerValues(input)) {
    for (const rawToken of value.split(",")) {
      const token = unquote(rawToken.trim());
      if (!token || token === "*") {
        continue;
      }
      const canonical = canonicalizeHintName(token);
      if (!canonical || seen.has(canonical.toLowerCase())) {
        continue;
      }
      seen.add(canonical.toLowerCase());
      result.push(canonical);
    }
  }

  return result;
}

/**
 * Parses a `Permissions-Policy` header or a policy record. Features that are
 * not present in the result use the browser default allowlist (`self`).
 */
export function parsePermissionsPolicy(
  input: PermissionsPolicySource | HeaderSource | undefined,
): ParsedPermissionsPolicy {
  const result: ParsedPermissionsPolicy = {};
  if (input === undefined || input === null) {
    return result;
  }

  if (typeof input === "string" || Array.isArray(input)) {
    parsePermissionsPolicyHeader(headerValues(input as HeaderValue).join(","), result);
    return result;
  }

  if (isHeadersLike(input) || input instanceof Map) {
    const header = getHeaderValues(input, "permissions-policy").join(",");
    parsePermissionsPolicyHeader(header, result);
    return result;
  }

  for (const [rawFeature, rawSources] of Object.entries(input)) {
    const feature = rawFeature.trim().toLowerCase();
    if (!feature) {
      continue;
    }
    for (const rawSource of headerValues(
      rawSources as string | readonly string[] | undefined,
    )) {
      for (const source of splitPolicySources(rawSource)) {
        addPolicySource(result, feature, source);
      }
    }
  }

  return result;
}

export class ClientHints {
  readonly #hints: Map<string, string>;
  readonly #sendLowEntropyCrossOrigin: boolean;
  readonly #documents = new Map<string, DocumentClientHintsState>();
  #activeTopLevelOrigin: string | null = null;

  constructor(options: ClientHintsOptions | ClientHintValues = {}) {
    const normalizedOptions: ClientHintsOptions = isClientHintsOptions(options)
      ? options
      : { hints: options };

    this.#hints = normalizeHintValues(
      normalizedOptions.hints ?? normalizedOptions.values ?? {},
    );
    this.#sendLowEntropyCrossOrigin =
      normalizedOptions.sendLowEntropyCrossOrigin === true;
  }

  /**
   * Records a response before any request is made from the resulting document.
   * Returns `true` only when the response was an HTTPS top-level navigation
   * response and its Client Hints declarations were accepted.
   */
  observeResponse(response: ClientHintsResponse): boolean {
    const responseURL = toURL(response.url ?? response.request?.url);
    if (!responseURL) {
      return false;
    }

    const topLevelOrigin = originOf(responseURL);
    if (!topLevelOrigin) {
      return false;
    }

    if (!isTopLevelNavigationResponse(response)) {
      return false;
    }

    this.#activeTopLevelOrigin = topLevelOrigin;

    // A downgrade or a malformed navigation must not leave an older opt-in in
    // place for the same origin.
    if (response.secure === false || responseURL.protocol !== "https:") {
      this.#documents.set(topLevelOrigin, {
        accepted: new Set<string>(),
        permissionsPolicy: {},
      });
      return false;
    }

    const acceptCH = parseAcceptCH([
      ...getHeaderValues(response.headers, "accept-ch"),
      ...headerValues(response.acceptCH),
    ]);
    const permissionsPolicy = mergePermissionsPolicies(
      parsePermissionsPolicy(
        getHeaderValues(response.headers, "permissions-policy").join(","),
      ),
      parsePermissionsPolicy(response.permissionsPolicy),
    );

    this.#documents.set(topLevelOrigin, {
      accepted: new Set<string>(acceptCH),
      permissionsPolicy,
    });
    return true;
  }

  /** Alias for `observeResponse`. */
  processResponse(response: ClientHintsResponse): boolean {
    return this.observeResponse(response);
  }

  /** Alias for `observeResponse`. */
  recordResponse(response: ClientHintsResponse): boolean {
    return this.observeResponse(response);
  }

  /** Forces a response to be treated as a top-level navigation response. */
  observeTopLevelResponse(response: ClientHintsResponse): boolean {
    return this.observeResponse({
      ...response,
      isTopLevelNavigation: true,
    });
  }

  /** Alias for `observeTopLevelResponse`. */
  observeNavigationResponse(response: ClientHintsResponse): boolean {
    return this.observeTopLevelResponse(response);
  }

  updateHints(hints: ClientHintValues): void {
    this.#hints.clear();
    for (const [name, value] of normalizeHintValues(hints)) {
      this.#hints.set(name, value);
    }
  }

  /**
   * Produces the Client Hints headers to add immediately before dispatching a
   * request. The returned object never mutates the caller's request.
   */
  getRequestHeaders(request: ClientHintRequest): Record<string, string> {
    const targetURL = toURL(request.url);
    if (!targetURL) {
      return {};
    }

    const targetOrigin = originOf(targetURL);
    if (!targetOrigin) {
      return {};
    }

    const topLevelOrigin = resolveTopLevelOrigin(
      request,
      this.#activeTopLevelOrigin,
    );
    if (!topLevelOrigin) {
      return {};
    }

    const documentState = this.#documents.get(topLevelOrigin);
    const sameOrigin = targetOrigin === topLevelOrigin;
    const headers: Record<string, string> = {};

    for (const [hint, value] of this.#hints) {
      if (!isLowEntropyHint(hint) && !documentState?.accepted.has(hint)) {
        continue;
      }

      if (
        !sameOrigin &&
        !this.#isCrossOriginHintAllowed(
          hint,
          documentState,
          topLevelOrigin,
          targetOrigin,
        )
      ) {
        continue;
      }

      headers[hint] = value;
    }

    return headers;
  }

  /** Alias for `getRequestHeaders`. */
  headersForRequest(request: ClientHintRequest): Record<string, string> {
    return this.getRequestHeaders(request);
  }

  /** Alias for `getRequestHeaders`. */
  getHeaders(request: ClientHintRequest): Record<string, string> {
    return this.getRequestHeaders(request);
  }

  /** Alias for `getRequestHeaders`. */
  headersFor(request: ClientHintRequest): Record<string, string> {
    return this.getRequestHeaders(request);
  }

  #isCrossOriginHintAllowed(
    hint: string,
    documentState: DocumentClientHintsState | undefined,
    topLevelOrigin: string,
    targetOrigin: string,
  ): boolean {
    const feature = permissionsPolicyFeatureForHint(hint);
    if (!feature) {
      return false;
    }

    if (isLowEntropyHint(hint)) {
      if (!this.#sendLowEntropyCrossOrigin) {
        return false;
      }
    } else if (!documentState?.accepted.has(hint)) {
      return false;
    }

    const allowlist =
      documentState?.permissionsPolicy[feature] ?? defaultAllowlist;
    return allowlistAllowsOrigin(allowlist, topLevelOrigin, targetOrigin);
  }
}

export function createClientHints(
  options: ClientHintsOptions | ClientHintValues = {},
): ClientHints {
  return new ClientHints(options);
}

/**
 * Convenience wrapper for a pre-request hook. The arguments may be supplied in
 * either order.
 */
export function attachClientHints(
  first: ClientHints | ClientHintRequest,
  second: ClientHints | ClientHintRequest,
): Record<string, string> {
  if (first instanceof ClientHints) {
    return first.getRequestHeaders(second as ClientHintRequest);
  }
  if (second instanceof ClientHints) {
    return second.getRequestHeaders(first as ClientHintRequest);
  }
  throw new TypeError("attachClientHints requires a ClientHints instance");
}

export const buildClientHintHeaders = attachClientHints;
export const getClientHintHeaders = attachClientHints;

export default ClientHints;

function normalizeHintValues(values: ClientHintValues): Map<string, string> {
  const result = new Map<string, string>();
  for (const [rawName, rawValue] of Object.entries(values)) {
    const name = canonicalizeHintName(rawName);
    if (!name || rawValue === undefined || rawValue === null) {
      continue;
    }
    const value = String(rawValue);
    if (value.length > 0) {
      result.set(name, value);
    }
  }
  return result;
}

function isClientHintsOptions(
  input: ClientHintsOptions | ClientHintValues,
): input is ClientHintsOptions {
  return (
    Object.prototype.hasOwnProperty.call(input, "hints") ||
    Object.prototype.hasOwnProperty.call(input, "values") ||
    Object.prototype.hasOwnProperty.call(input, "sendLowEntropyCrossOrigin")
  );
}

function isTopLevelNavigationResponse(response: ClientHintsResponse): boolean {
  if (response.isTopLevelNavigation !== undefined) {
    return response.isTopLevelNavigation === true;
  }
  if (response.isTopLevel !== undefined) {
    return response.isTopLevel === true;
  }
  if (response.topLevelNavigation !== undefined) {
    return response.topLevelNavigation === true;
  }
  if (response.request?.isTopLevelNavigation !== undefined) {
    return response.request.isTopLevelNavigation === true;
  }
  if (response.request?.isTopLevel !== undefined) {
    return response.request.isTopLevel === true;
  }
  if (response.request?.topLevelNavigation !== undefined) {
    return response.request.topLevelNavigation === true;
  }
  return false;
}

function resolveTopLevelOrigin(
  request: ClientHintRequest,
  activeTopLevelOrigin: string | null,
): string | null {
  const candidate =
    request.topLevelOrigin ??
    request.documentOrigin ??
    request.initiatorOrigin ??
    request.origin;
  if (candidate !== undefined) {
    const url = toURL(candidate);
    return url ? originOf(url) : null;
  }
  return activeTopLevelOrigin;
}

function allowlistAllowsOrigin(
  allowlist: PermissionAllowlist,
  topLevelOrigin: string,
  targetOrigin: string,
): boolean {
  if (allowlist.any) {
    return true;
  }
  if (allowlist.self && topLevelOrigin === targetOrigin) {
    return true;
  }
  return allowlist.origins.some((source) =>
    originSourceMatches(source, targetOrigin),
  );
}

function originSourceMatches(source: string, targetOrigin: string): boolean {
  if (source === targetOrigin) {
    return true;
  }

  const targetURL = toURL(targetOrigin);
  const match = /^(https?):\/\/\*\.([^/:]+)(?::(\*|\d+))?$/.exec(source);
  if (!match || !targetURL || match[1] !== targetURL.protocol.slice(0, -1)) {
    return false;
  }

  const [, , hostSuffix, port] = match;
  if (
    targetURL.hostname !== hostSuffix &&
    !targetURL.hostname.endsWith(`.${hostSuffix}`)
  ) {
    return false;
  }
  if (port && port !== "*" && port !== targetURL.port) {
    return false;
  }
  return true;
}

function mergePermissionsPolicies(
  first: ParsedPermissionsPolicy,
  second: ParsedPermissionsPolicy,
): ParsedPermissionsPolicy {
  const result: ParsedPermissionsPolicy = {};
  for (const source of [first, second]) {
    for (const [feature, allowlist] of Object.entries(source)) {
      const current = result[feature];
      if (!current) {
        result[feature] = {
          any: allowlist.any,
          self: allowlist.self,
          origins: [...allowlist.origins],
        };
        continue;
      }
      result[feature] = {
        any: current.any || allowlist.any,
        self: current.self || allowlist.self,
        origins: [...new Set([...current.origins, ...allowlist.origins])],
      };
    }
  }
  return result;
}

function parsePermissionsPolicyHeader(
  header: string,
  result: ParsedPermissionsPolicy,
): void {
  for (const entry of splitPolicyEntries(header)) {
    const equals = entry.indexOf("=");
    if (equals < 0) {
      continue;
    }

    const feature = entry.slice(0, equals).trim().toLowerCase();
    if (!feature) {
      continue;
    }

    let sources = entry.slice(equals + 1).trim();
    if (sources.startsWith("(") && sources.endsWith(")")) {
      sources = sources.slice(1, -1);
    }

    for (const source of splitPolicySources(sources)) {
      addPolicySource(result, feature, source);
    }
  }
}

function addPolicySource(
  result: ParsedPermissionsPolicy,
  rawFeature: string,
  rawSource: string,
): void {
  const feature = rawFeature.trim().toLowerCase();
  if (!feature) {
    return;
  }

  const allowlist = result[feature] ?? {
    any: false,
    self: false,
    origins: [],
  };
  const source = unquote(rawSource.trim()).toLowerCase();

  if (!source) {
    result[feature] = allowlist;
    return;
  }

  if (source === "*") {
    result[feature] = {
      any: true,
      self: allowlist.self,
      origins: allowlist.origins,
    };
    return;
  }

  if (source === "self") {
    result[feature] = {
      any: allowlist.any,
      self: true,
      origins: allowlist.origins,
    };
    return;
  }

  const sourceURL = toURL(source);
  if (!sourceURL || !originOf(sourceURL)) {
    result[feature] = {
      any: allowlist.any,
      self: allowlist.self,
      origins: allowlist.origins,
    };
    return;
  }

  const origin = originOf(sourceURL);
  if (!origin || allowlist.origins.includes(origin)) {
    result[feature] = allowlist;
    return;
  }

  result[feature] = {
    any: allowlist.any,
    self: allowlist.self,
    origins: [...allowlist.origins, origin],
  };
}

function splitPolicyEntries(input: string): string[] {
  return splitOutsideQuotes(input, ",");
}

function splitPolicySources(input: string): string[] {
  return splitOutsideQuotes(input, null);
}

function splitOutsideQuotes(
  input: string,
  commaSeparator: "," | null,
): string[] {
  const result: string[] = [];
  let current = "";
  let quoted = false;
  let depth = 0;

  for (let index = 0; index < input.length; index += 1) {
    const character = input[index];
    if (character === '"' && input[index - 1] !== "\\") {
      quoted = !quoted;
      current += character;
      continue;
    }
    if (!quoted) {
      if (character === "(") {
        depth += 1;
      } else if (character === ")") {
        depth = Math.max(0, depth - 1);
      } else if (
        (commaSeparator === "," && character === "," && depth === 0) ||
        (commaSeparator === null && /\s/.test(character) && depth === 0)
      ) {
        if (current.trim()) {
          result.push(current.trim());
        }
        current = "";
        continue;
      }
    }
    current += character;
  }

  if (current.trim()) {
    result.push(current.trim());
  }
  return result;
}

function getHeaderValues(
  headers: HeaderSource | undefined,
  name: string,
): string[] {
  if (!headers) {
    return [];
  }

  const wanted = name.toLowerCase();
  if (headers instanceof Map) {
    for (const [key, value] of headers) {
      if (String(key).toLowerCase() === wanted) {
        return headerValues(value);
      }
    }
    return [];
  }

  if (isHeadersLike(headers)) {
    return headerValues(headers.get(name) as HeaderValue);
  }

  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === wanted) {
      return headerValues(value);
    }
  }
  return [];
}

function isHeadersLike(
  input: HeaderSource | PermissionsPolicySource,
): input is Headers {
  return typeof (input as Headers).get === "function";
}

function headerValues(value: HeaderValue): string[] {
  if (value === undefined || value === null) {
    return [];
  }
  if (Array.isArray(value)) {
    return value
      .filter((item): item is string => typeof item === "string")
      .map((item) => item.trim())
      .filter(Boolean);
  }
  return String(value)
    .split("\n")
    .map((item) => item.trim())
    .filter(Boolean);
}

function toURL(value: string | URL | undefined): URL | null {
  if (value === undefined) {
    return null;
  }
  try {
    return value instanceof URL ? value : new URL(value);
  } catch {
    return null;
  }
}

function originOf(url: URL): string | null {
  try {
    return url.origin === "null" ? null : url.origin.toLowerCase();
  } catch {
    return null;
  }
}

function unquote(value: string): string {
  if (value.length >= 2 && value.startsWith('"') && value.endsWith('"')) {
    return value.slice(1, -1);
  }
  return value;
}

function titleCase(value: string): string {
  return value.length === 0
    ? value
    : `${value[0].toUpperCase()}${value.slice(1)}`;
}
