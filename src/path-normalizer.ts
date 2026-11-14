/**
 * One canonical path model for gateway authorization and backend routing.
 *
 * The important invariant is that a request target is parsed exactly once.
 * Authorization and routing both consume the resulting NormalizedPath.  In
 * particular, a matrix parameter is never removed from the path that is
 * authorized: it remains attached to its segment, while the routing path is
 * the path the backend sees after applying its matrix-parameter rule.
 */

export type PathErrorCode =
  | "INVALID_TARGET"
  | "INVALID_PERCENT_ENCODING"
  | "DOUBLE_ENCODED_DELIMITER"
  | "INVALID_CHARACTER"
  | "EMPTY_MATRIX_SEGMENT"
  | "MATRIX_ON_DOT_SEGMENT"
  | "AMBIGUOUS_SEGMENT";

export class PathNormalizationError extends Error {
  readonly code: PathErrorCode;
  readonly target: string;
  readonly offset?: number;

  constructor(
    code: PathErrorCode,
    message: string,
    target: string,
    offset?: number,
  ) {
    super(message);
    this.name = "PathNormalizationError";
    this.code = code;
    this.target = target;
    this.offset = offset;
  }
}

export interface MatrixParameter {
  /** Decoded parameter name, before the first "=". */
  readonly name: string;
  /** Decoded value, or null when the parameter has no "=". */
  readonly value: string | null;
  /** The decoded parameter text exactly as it appeared after the ";". */
  readonly raw: string;
}

export interface PathSegment {
  /** Zero-based position in the canonical path. */
  readonly index: number;
  /**
   * Decoded, case-folded, trailing-dot-normalized segment name.  This is the
   * identifier used for authorization and for the backend-facing routing path.
   */
  readonly name: string;
  /** Decoded name before case/trailing-dot normalization. */
  readonly decodedName: string;
  /** True when the original request segment contained a ";". */
  readonly hasMatrix: boolean;
  /** Decoded text after the first ";" (empty when hasMatrix is false). */
  readonly matrixText: string;
  /** Parsed matrix parameters. Empty strings are preserved as empty entries. */
  readonly matrix: readonly MatrixParameter[];
  /**
   * Canonical segment including the matrix text.  This is the segment used by
   * authorization and by the canonical `path`; the ";" is never dropped.
   */
  readonly canonical: string;
}

export interface NormalizedPath {
  /** The request target as supplied to the normalizer. */
  readonly raw: string;
  /**
   * Canonical decoded path with matrix parameters retained.  This is the
   * authorization path and the path applications should log.
   */
  readonly path: string;
  /**
   * Canonical decoded path with matrix parameters removed.  This is the path a
   * matrix-parameter-ignoring backend resolves.  It is used only together with
   * the segment metadata above so that a ";" on an authorization boundary is
   * still rejected.
   */
  readonly resourcePath: string;
  readonly segments: readonly PathSegment[];
  /** Segment identifier names only (matrix text removed). */
  readonly segmentNames: readonly string[];
  /** True when a non-root path ended in "/". */
  readonly trailingSlash: boolean;
  /** Number of empty path segments collapsed into a single separator. */
  readonly collapsedEmptySegments: number;
  /** True when the target was absolute-form (scheme://authority/path). */
  readonly absoluteForm: boolean;
  /** True when ".." tried to pop above the root. */
  readonly clampedParentSegments: number;
  /** True when the canonical path contains any matrix parameter. */
  readonly hasMatrix: boolean;
  /** The matrix-bearing segments, in path order. */
  readonly matrixSegments: readonly PathSegment[];
}

export interface AuthorizationPolicy {
  /** Prefixes are segment-aware; "/api/tenant/" does not authorize "/api/tenantx". */
  readonly allowedPrefixes?: readonly string[];
  /** Exact canonical resource paths. */
  readonly allowedPaths?: readonly string[];
}

export interface RouteDefinition {
  /** Static backend-facing path, e.g. "/api/tenant/secret". */
  readonly path: string;
  /** Logical resource identifier returned to the application. */
  readonly resource: string;
  readonly methods?: readonly string[];
}

export interface RouteMatch {
  readonly route: RouteDefinition;
  readonly resource: string;
  /** Canonical backend-facing path (matrix parameters excluded). */
  readonly path: string;
  /** Canonical authorization path (matrix parameters retained). */
  readonly requestPath: string;
  readonly normalized: NormalizedPath;
}

export interface AuthorizationDecision {
  readonly allowed: boolean;
  readonly status: number;
  readonly body: string;
  readonly headers: Readonly<Record<string, string>>;
  readonly normalized?: NormalizedPath;
  /** Present only when allowed. */
  readonly path?: string;
  /** Present only when allowed. */
  readonly resourcePath?: string;
}

export interface GatewayRequest {
  readonly method?: string;
  readonly target: string;
}

export interface GatewayOptions {
  readonly policy?: AuthorizationPolicy;
  /** Convenience aliases when a gateway has only one policy object. */
  readonly allowedPrefixes?: readonly string[];
  readonly allowedPaths?: readonly string[];
  readonly routes?: readonly RouteDefinition[];
}

export interface GatewayResponse extends AuthorizationDecision {
  readonly route?: RouteDefinition | null;
}

const EMPTY_HEADERS: Readonly<Record<string, string>> = Object.freeze({});
const DENIAL_HEADERS: Readonly<Record<string, string>> = Object.freeze({
  "content-type": "text/plain; charset=utf-8",
});

/**
 * The only denial returned by the gateway.  Every normalization, policy and
 * route failure has the same shape, so clients cannot use the response to
 * distinguish "bad path", "wrong tenant" and "route not found".
 */
export const UNIFORM_DENIAL = Object.freeze({
  allowed: false as const,
  status: 403,
  body: "Forbidden",
  headers: DENIAL_HEADERS,
});

function fail(
  code: PathErrorCode,
  message: string,
  target: string,
  offset?: number,
): never {
  throw new PathNormalizationError(code, message, target, offset);
}

function isNormalizedPath(value: unknown): value is NormalizedPath {
  return (
    typeof value === "object" &&
    value !== null &&
    "path" in value &&
    "resourcePath" in value &&
    "segments" in value &&
    typeof (value as { path?: unknown }).path === "string" &&
    typeof (value as { resourcePath?: unknown }).resourcePath === "string" &&
    Array.isArray((value as { segments?: unknown }).segments)
  );
}

function findBadPercentEncoding(value: string): number {
  for (let index = 0; index < value.length; index += 1) {
    if (value[index] !== "%") {
      continue;
    }
    const first = value[index + 1];
    const second = value[index + 2];
    if (
      first === undefined ||
      second === undefined ||
      !/[0-9a-f]/i.test(first) ||
      !/[0-9a-f]/i.test(second)
    ) {
      return index;
    }
    index += 2;
  }
  return -1;
}

function decodePathOnce(value: string, target: string): string {
  const badPercent = findBadPercentEncoding(value);
  if (badPercent !== -1) {
    fail(
      "INVALID_PERCENT_ENCODING",
      "The request target contains an invalid percent-encoded octet.",
      target,
      badPercent,
    );
  }

  let decoded: string;
  try {
    // Decode exactly once.  Decoding the path before splitting it is
    // intentional: %2F and %5C become separators in the same model the
    // backend uses, and %3B becomes a real matrix delimiter.
    decoded = decodeURIComponent(value);
  } catch {
    fail(
      "INVALID_PERCENT_ENCODING",
      "The request target contains malformed percent encoding.",
      target,
    );
  }

  // A residual encoded delimiter means the target was encoded twice.  A
  // downstream decoder could turn %252F into a separator, so refuse it now
  // rather than letting two components disagree about the path.
  if (/%(?:2f|5c|3b|2e|3f|23|25)/i.test(decoded)) {
    fail(
      "DOUBLE_ENCODED_DELIMITER",
      "The request target contains a double-encoded path delimiter.",
      target,
    );
  }

  if (/[\u0000-\u001f\u007f]/.test(decoded)) {
    fail(
      "INVALID_CHARACTER",
      "The request target contains a control character.",
      target,
    );
  }

  return decoded;
}

function stripQueryAndFragment(value: string): string {
  const queryIndex = value.indexOf("?");
  const fragmentIndex = value.indexOf("#");
  let end = value.length;
  if (queryIndex !== -1) {
    end = queryIndex;
  }
  if (fragmentIndex !== -1 && fragmentIndex < end) {
    end = fragmentIndex;
  }
  return value.slice(0, end);
}

function extractOriginPath(target: string): {
  value: string;
  absoluteForm: boolean;
} {
  if (typeof target !== "string") {
    fail("INVALID_TARGET", "The request target must be a string.", String(target));
  }
  if (target.length === 0) {
    fail("INVALID_TARGET", "The request target is empty.", target);
  }
  if (/[\u0000-\u001f\u007f]/.test(target)) {
    fail(
      "INVALID_CHARACTER",
      "The request target contains a control character.",
      target,
    );
  }

  let path = target;
  let absoluteForm = false;

  const absoluteMatch = /^[A-Za-z][A-Za-z0-9+.-]*:\/\//.exec(path);
  if (absoluteMatch !== null) {
    absoluteForm = true;
    const authorityAndPath = path.slice(absoluteMatch[0].length);
    const pathStart = authorityAndPath.search(/[/?#]/);
    if (pathStart === -1) {
      path = "/";
    } else if (authorityAndPath[pathStart] === "/") {
      path = authorityAndPath.slice(pathStart);
    } else {
      // A query or fragment directly after the authority means the resource
      // is the root path.
      path = "/";
    }
  } else if (path === "*") {
    // Asterisk-form is valid only for OPTIONS.  It has no tenant/resource
    // semantics, so it cannot be authorized by a path policy.
    fail("INVALID_TARGET", "Asterisk-form request targets are not supported.", target);
  }

  path = stripQueryAndFragment(path);
  if (path.length === 0) {
    path = "/";
  } else if (!path.startsWith("/")) {
    // Origin-form paths must start with "/".  Normalizing a relative target
    // to the same root-relative model keeps malformed clients from creating a
    // second path vocabulary.
    path = `/${path}`;
  }

  return { value: path, absoluteForm };
}

function splitMatrix(segmentText: string): {
  decodedName: string;
  hasMatrix: boolean;
  matrixText: string;
  matrix: readonly MatrixParameter[];
} {
  const semicolon = segmentText.indexOf(";");
  if (semicolon === -1) {
    return {
      decodedName: segmentText,
      hasMatrix: false,
      matrixText: "",
      matrix: Object.freeze([]),
    };
  }

  const decodedName = segmentText.slice(0, semicolon);
  const matrixText = segmentText.slice(semicolon + 1);
  const matrix = matrixText.split(";").map((raw) => {
    const equals = raw.indexOf("=");
    return Object.freeze({
      name: equals === -1 ? raw : raw.slice(0, equals),
      value: equals === -1 ? null : raw.slice(equals + 1),
      raw,
    });
  });

  return {
    decodedName,
    hasMatrix: true,
    matrixText,
    matrix: Object.freeze(matrix),
  };
}

function canonicalSegmentName(decodedName: string, target: string): string {
  const unicodeName = decodedName.normalize("NFC");
  if (unicodeName === "." || unicodeName === "..") {
    return unicodeName;
  }

  // Backends that ignore trailing dots (notably Windows-backed frameworks)
  // would otherwise make "secret." and "secret" the same resource while an
  // authorization check sees only one of them.
  const withoutTrailingDots = unicodeName.replace(/\.+$/u, "");
  if (withoutTrailingDots.length === 0) {
    fail(
      "AMBIGUOUS_SEGMENT",
      "A path segment consisting only of dots is ambiguous.",
      target,
    );
  }
  return withoutTrailingDots.toLowerCase();
}

function serializePath(
  segments: readonly PathSegment[],
  trailingSlash: boolean,
  includeMatrix: boolean,
): string {
  const body = segments
    .map((segment) => (includeMatrix ? segment.canonical : segment.name))
    .join("/");
  if (body.length === 0) {
    return "/";
  }
  return `/${body}${trailingSlash ? "/" : ""}`;
}

/**
 * Parse a request target into the one path model shared by authorization and
 * routing.
 *
 * Rules, in order:
 * 1. Absolute-form targets are reduced to their origin path; query and
 *    fragment text are not part of the resource path.
 * 2. Percent encoding is decoded exactly once.  Encoded "/" and "\" become
 *    separators, and encoded ";" becomes a matrix delimiter.
 * 3. "\" is treated as "/" so the model does not depend on the backend OS.
 * 4. Empty and duplicate segments collapse; a non-root trailing slash is
 *    retained as a flag.
 * 5. "." and ".." are resolved after decoding.
 * 6. Trailing dots are removed, and segment names are case-folded.
 * 7. Matrix text after the first ";" is kept attached to the segment.
 *
 * Throws PathNormalizationError when the target cannot be represented by a
 * single unambiguous model.
 */
export function normalizeRequestTarget(target: string): NormalizedPath {
  const { value, absoluteForm } = extractOriginPath(target);
  const decoded = decodePathOnce(value, target);
  const slashPath = decoded.replace(/\\/gu, "/");
  const startsWithSlash = slashPath.startsWith("/");
  const parts = slashPath.split("/");
  if (startsWithSlash) {
    parts.shift();
  }

  let trailingSlash = false;
  const segments: PathSegment[] = [];
  let collapsedEmptySegments = 0;
  let clampedParentSegments = 0;

  for (let index = 0; index < parts.length; index += 1) {
    const part = parts[index]!;
    const isLast = index === parts.length - 1;

    if (part === "") {
      if (isLast && segments.length > 0) {
        trailingSlash = true;
      } else if (!(isLast && segments.length === 0 && parts.length === 1)) {
        // This includes repeated leading slashes (the first separator is
        // removed above), repeated internal slashes, and repeated trailing
        // slashes.  A single root "/" is not an empty segment.
        collapsedEmptySegments += 1;
      }
      continue;
    }

    const { decodedName, hasMatrix, matrixText, matrix } = splitMatrix(part);
    if (hasMatrix && decodedName === "") {
      fail(
        "EMPTY_MATRIX_SEGMENT",
        "A matrix parameter cannot be attached to an empty path segment.",
        target,
      );
    }

    const name = canonicalSegmentName(decodedName, target);
    if (name === "." || name === "..") {
      if (hasMatrix) {
        fail(
          "MATRIX_ON_DOT_SEGMENT",
          "A dot segment cannot carry matrix parameters.",
          target,
        );
      }
      if (name === ".") {
        continue;
      }
      if (segments.length > 0) {
        segments.pop();
      } else {
        clampedParentSegments += 1;
      }
      continue;
    }

    const canonical = hasMatrix ? `${name};${matrixText}` : name;
    segments.push(
      Object.freeze({
        index: segments.length,
        name,
        decodedName,
        hasMatrix,
        matrixText,
        matrix,
        canonical,
      }),
    );
  }

  if (segments.length === 0) {
    trailingSlash = false;
  }

  // Dot-segment resolution can remove earlier entries.  Re-index the final
  // list so `index` always describes the canonical position.
  const canonicalSegments = segments.map((segment, index) =>
    segment.index === index
      ? segment
      : Object.freeze({ ...segment, index }),
  );

  const path = serializePath(canonicalSegments, trailingSlash, true);
  const resourcePath = serializePath(canonicalSegments, trailingSlash, false);
  const segmentNames = Object.freeze(
    canonicalSegments.map((segment) => segment.name),
  );
  const matrixSegments = Object.freeze(
    canonicalSegments.filter((segment) => segment.hasMatrix),
  );

  return Object.freeze({
    raw: target,
    path,
    resourcePath,
    segments: Object.freeze(canonicalSegments),
    segmentNames,
    trailingSlash,
    collapsedEmptySegments,
    absoluteForm,
    clampedParentSegments,
    hasMatrix: matrixSegments.length > 0,
    matrixSegments,
  });
}

/** Alias used by applications that think in terms of path, not target. */
export const normalizePath = normalizeRequestTarget;
/** Short alias for embedding in small gateways. */
export const normalize = normalizeRequestTarget;
/** Alternate descriptive aliases. */
export const canonicalizePath = normalizeRequestTarget;
export const parseRequestTarget = normalizeRequestTarget;
export const normalizeRequestPath = normalizeRequestTarget;
export const parsePath = normalizeRequestTarget;
export const canonicalizeRequestTarget = normalizeRequestTarget;

/** Return just the canonical authorization path string. */
export const normalizePathString = (target: string): string =>
  normalizeRequestTarget(target).path;

function normalizedPrefix(prefix: string): NormalizedPath {
  const normalized = normalizeRequestTarget(prefix);
  if (normalized.hasMatrix) {
    fail(
      "AMBIGUOUS_SEGMENT",
      "Authorization prefixes cannot contain matrix parameters.",
      prefix,
    );
  }
  return normalized;
}

function prefixMatches(
  request: NormalizedPath,
  prefix: NormalizedPath,
): boolean {
  const requiredSegments = prefix.segmentNames.length;
  if (request.segmentNames.length < requiredSegments) {
    return false;
  }
  if (request.segmentNames.length === requiredSegments &&
    prefix.trailingSlash &&
    !request.trailingSlash
  ) {
    return false;
  }

  for (let index = 0; index < requiredSegments; index += 1) {
    if (request.segmentNames[index] !== prefix.segmentNames[index]) {
      return false;
    }
  }

  // A matrix parameter attached to any segment of the authorization prefix
  // changes the segment's identity.  This is the
  // "/api/tenant;admin/secret" bypass: the backend-facing resourcePath
  // would start with /api/tenant/, but the authorized segment is not
  // "tenant".  Refuse it instead of silently discarding the ";".
  for (let index = 0; index < requiredSegments; index += 1) {
    if (request.segments[index]!.hasMatrix) {
      return false;
    }
  }

  return true;
}

function exactPathMatches(
  request: NormalizedPath,
  allowed: NormalizedPath,
): boolean {
  // Exact authorization compares full canonical paths with matrix text
  // retained.  A trailing matrix parameter therefore cannot turn a different
  // authorization path into the allowed path.
  return request.path === allowed.path;
}

/**
 * Authorize an already-normalized request.  Passing a NormalizedPath (rather
 * than a raw target) makes it impossible for the caller to accidentally
 * re-parse the request with a different path model.
 */
export function authorizeNormalizedPath(
  request: NormalizedPath,
  policy: AuthorizationPolicy,
): AuthorizationDecision {
  const prefixes = (policy.allowedPrefixes ?? []).map(normalizedPrefix);
  const allowedPaths = (policy.allowedPaths ?? []).map(normalizedPrefix);

  const allowed =
    prefixes.some((prefix) => prefixMatches(request, prefix)) ||
    allowedPaths.some((allowed) => exactPathMatches(request, allowed));

  if (!allowed) {
    return UNIFORM_DENIAL;
  }

  return Object.freeze({
    allowed: true,
    status: 200,
    body: "",
    headers: EMPTY_HEADERS,
    normalized: request,
    path: request.path,
    resourcePath: request.resourcePath,
  });
}

/**
 * Normalize and authorize a raw request target.  Invalid targets are mapped to
 * the same denial as an unauthorized resource.
 */
export function authorizeRequest(
  target: string | NormalizedPath,
  policy: AuthorizationPolicy,
): AuthorizationDecision {
  let normalized: NormalizedPath;
  if (isNormalizedPath(target)) {
    normalized = target;
    return authorizeNormalizedPath(normalized, policy);
  }
  try {
    normalized = normalizeRequestTarget(target);
  } catch {
    return UNIFORM_DENIAL;
  }
  return authorizeNormalizedPath(normalized, policy);
}

/** Boolean convenience wrapper. */
export const isAuthorized = (
  target: string | NormalizedPath,
  policy: AuthorizationPolicy,
): boolean => authorizeRequest(target, policy).allowed;

/** Compatibility alias. */
export const authorize = authorizeRequest;
export const authorizePath = authorizeRequest;
export const isPathAuthorized = isAuthorized;

/**
 * Resolve a route from the same NormalizedPath used for authorization.  The
 * route lookup uses the backend-facing resourcePath (matrix parameters are
 * ignored by that backend), while the authorization path remains available on
 * the result for auditing.
 */
export function matchRoute(
  request: NormalizedPath | string,
  routes: readonly RouteDefinition[],
  method?: string,
): RouteMatch | null {
  let normalized: NormalizedPath;
  if (typeof request === "string") {
    normalized = normalizeRequestTarget(request);
  } else {
    normalized = request;
  }

  const upperMethod = method?.toUpperCase();
  for (const route of routes) {
    const routePath = normalizeRequestTarget(route.path);
    if (routePath.hasMatrix) {
      fail(
        "AMBIGUOUS_SEGMENT",
        "A static route cannot contain matrix parameters.",
        route.path,
      );
    }
    if (normalized.resourcePath !== routePath.resourcePath) {
      continue;
    }
    if (
      route.methods !== undefined &&
      upperMethod !== undefined &&
      !route.methods.some((candidate) => candidate.toUpperCase() === upperMethod)
    ) {
      continue;
    }
    return Object.freeze({
      route,
      resource: route.resource,
      path: normalized.resourcePath,
      requestPath: normalized.path,
      normalized,
    });
  }
  return null;
}

export const resolveRoute = matchRoute;
export const resolveRequestRoute = matchRoute;

function resolveGatewayOptions(options: GatewayOptions): {
  policy: AuthorizationPolicy;
  routes: readonly RouteDefinition[];
} {
  const policy: AuthorizationPolicy = options.policy ?? {
    allowedPrefixes: options.allowedPrefixes ?? [],
    allowedPaths: options.allowedPaths ?? [],
  };
  return {
    policy,
    routes: options.routes ?? [],
  };
}

/**
 * Process a gateway request.  The target is normalized once, then the same
 * object is passed to authorization and route matching.  All failure modes
 * return UNIFORM_DENIAL, deliberately without the normalized path.
 */
export function handleRequest(
  request: GatewayRequest | string | NormalizedPath,
  options: GatewayOptions,
): GatewayResponse {
  let normalized: NormalizedPath;
  let method: string | undefined;
  if (isNormalizedPath(request)) {
    normalized = request;
  } else {
    const target = typeof request === "string" ? request : request.target;
    method = typeof request === "string" ? undefined : request.method;
    try {
      normalized = normalizeRequestTarget(target);
    } catch {
      return UNIFORM_DENIAL;
    }
  }

  const resolvedOptions = resolveGatewayOptions(options);
  const decision = authorizeNormalizedPath(normalized, resolvedOptions.policy);
  if (!decision.allowed) {
    return UNIFORM_DENIAL;
  }

  let route: RouteDefinition | null = null;
  if (options.routes !== undefined) {
    let matched: RouteMatch | null;
    try {
      matched = matchRoute(normalized, resolvedOptions.routes, method);
    } catch {
      return UNIFORM_DENIAL;
    }
    if (matched === null) {
      return UNIFORM_DENIAL;
    }
    route = matched.route;
  }

  return Object.freeze({
    allowed: true,
    status: 200,
    body: "",
    headers: EMPTY_HEADERS,
    normalized,
    path: normalized.path,
    resourcePath: normalized.resourcePath,
    route,
  });
}

export interface Gateway {
  readonly policy: AuthorizationPolicy;
  readonly routes: readonly RouteDefinition[];
  normalize(target: string): NormalizedPath;
  authorize(target: string | NormalizedPath): AuthorizationDecision;
  handle(request: GatewayRequest | string | NormalizedPath): GatewayResponse;
}

/** Create a small embeddable gateway facade. */
export function createGateway(options: GatewayOptions): Gateway {
  const resolvedOptions = resolveGatewayOptions(options);
  const policy = Object.freeze({
    allowedPrefixes: Object.freeze([
      ...(resolvedOptions.policy.allowedPrefixes ?? []),
    ]),
    allowedPaths: Object.freeze([
      ...(resolvedOptions.policy.allowedPaths ?? []),
    ]),
  });
  const routes = Object.freeze([...resolvedOptions.routes]);

  return Object.freeze({
    policy,
    routes,
    normalize: normalizeRequestTarget,
    authorize: (target: string | NormalizedPath) => authorizeRequest(target, policy),
    handle: (request: GatewayRequest | string | NormalizedPath) =>
      handleRequest(request, { policy, routes }),
  });
}

/** Class form for dependency injection in TypeScript gateways. */
export class PathNormalizer {
  readonly policy: AuthorizationPolicy;
  readonly routes: readonly RouteDefinition[];

  constructor(options: GatewayOptions) {
    const resolvedOptions = resolveGatewayOptions(options);
    this.policy = resolvedOptions.policy;
    this.routes = Object.freeze([...resolvedOptions.routes]);
  }

  normalize(target: string): NormalizedPath {
    return normalizeRequestTarget(target);
  }

  authorize(target: string | NormalizedPath): AuthorizationDecision {
    return authorizeRequest(target, this.policy);
  }

  route(
    request: NormalizedPath | string,
    method?: string,
  ): RouteMatch | null {
    return matchRoute(request, this.routes, method);
  }

  handle(request: GatewayRequest | string | NormalizedPath): GatewayResponse {
    return handleRequest(request, {
      policy: this.policy,
      routes: this.routes,
    });
  }
}

/** Convenience wrapper for callers that want both decisions at once. */
export function authorizeAndResolve(
  request: GatewayRequest | string | NormalizedPath,
  options: GatewayOptions,
): GatewayResponse {
  return handleRequest(request, options);
}

export const processRequest = handleRequest;

const defaultExport = Object.freeze({
  normalizeRequestTarget,
  normalizePath,
  normalizePathString,
  normalize,
  canonicalizePath,
  parseRequestTarget,
  normalizeRequestPath,
  parsePath,
  canonicalizeRequestTarget,
  authorizeRequest,
  authorizeNormalizedPath,
  authorize,
  authorizePath,
  isAuthorized,
  isPathAuthorized,
  matchRoute,
  resolveRoute,
  resolveRequestRoute,
  handleRequest,
  processRequest,
  createGateway,
  authorizeAndResolve,
  PathNormalizer,
  PathNormalizationError,
  UNIFORM_DENIAL,
});

export default defaultExport;
