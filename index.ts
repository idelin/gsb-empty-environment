/**
 * Embeddable capability checks for a desktop IPC layer.
 *
 * The module is deliberately side-effect free: the host passes the window label,
 * the invoked permission, the webview origin and the paths supplied by the
 * page. No filesystem access is performed here, so it can be used by any
 * TypeScript IPC host.
 */

export type ScopeEntry = string | { path: string };

export interface PermissionDefinition {
  identifier: string;
  /**
   * `undefined` means "no path restriction". An explicit empty array means
   * that no path is allowed.
   */
  allow?: readonly ScopeEntry[];
  deny?: readonly ScopeEntry[];
}

export type PermissionEntry = string | PermissionDefinition;

export interface RemoteConfig {
  /**
   * URL glob patterns. `*` does not cross `/`; `**` does.
   */
  urls?: readonly string[];
}

export interface Capability {
  identifier?: string;
  /** Window label globs. `*` matches any label, including dynamically created ones. */
  windows: readonly string[];
  permissions?: readonly PermissionEntry[];
  remote?: RemoteConfig;
}

export interface CapabilityRequest {
  /** Label of the webview that made the IPC call. */
  windowLabel: string;
  /** Permission identifier, for example `fs:allow-read-text-file`. */
  permission: string;
  /**
   * Full URL of the calling webview. A missing origin is treated as a local
   * page, which is the common case for calls made by the host itself.
   */
  origin?: string | null;
  path?: string;
  paths?: readonly string[];
}

export type CapabilityDenialReason =
  | 'invalid-request'
  | 'no-matching-capability'
  | 'remote-origin-denied'
  | 'permission-not-granted'
  | 'path-required'
  | 'invalid-path'
  | 'path-denied'
  | 'path-not-allowed';

export interface CapabilityDecision {
  allowed: boolean;
  reason?: CapabilityDenialReason;
  message?: string;
  capabilityIdentifier?: string;
  permission?: string;
  path?: string;
  scope?: string;
  origin?: string;
}

export type PathPlatform = 'posix' | 'windows';

export interface CapabilityEngineOptions {
  /** Environment used for `$APPDATA`, `$HOME`, `%APPDATA%` and similar names. */
  env?: Record<string, string | undefined>;
  platform?: PathPlatform | 'auto';
  /**
   * Defaults to false on Windows and true elsewhere. Matching is also
   * normalized for separator and root comparisons.
   */
  caseSensitivePaths?: boolean;
  /** Override the default local-origin policy. */
  isLocalOrigin?: (origin: string) => boolean;
}

export interface NormalizedPath {
  root: string;
  segments: string[];
  value: string;
}

interface CompiledScope {
  raw: string;
  path: CompiledPath | null;
}

interface CompiledPermission {
  identifier: string;
  /** `null` means unrestricted; an array means at least one path must match. */
  allow: CompiledScope[] | null;
  deny: CompiledScope[];
}

interface CompiledCapability {
  identifier: string;
  windows: string[];
  /** `null` means local-only (no remote URLs configured). */
  remoteUrls: string[] | null;
  permissions: CompiledPermission[];
}

interface PathSegmentPattern {
  type: 'double' | 'segment';
  regex?: RegExp;
}

interface CompiledPath {
  root: string;
  segments: PathSegmentPattern[];
}

interface OriginInfo {
  kind: 'local' | 'remote' | 'invalid';
  value: string;
  parsed?: ParsedUrl;
}

interface ParsedUrl {
  scheme: string;
  host: string;
  port: string;
  path: string;
}

interface ParsedUrlPattern {
  scheme: string;
  host: string;
  /** null means the scheme's default port. */
  port: string | null;
  /** null means any path. */
  path: CompiledPath | null;
}

interface NodeProcess {
  env?: Record<string, string | undefined>;
  platform?: string;
}

const VARIABLE_PATTERN =
  /\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))|%([A-Za-z_][A-Za-z0-9_]*)%/g;

const DEFAULT_PORTS: Record<string, string> = {
  http: '80',
  https: '443',
  ws: '80',
  wss: '443',
};

/**
 * Checks a window label against the glob syntax used by capability `windows`.
 * `*` matches any text (including an empty string), `?` matches one character.
 */
export function matchWindowLabel(pattern: string, label: string): boolean {
  if (typeof pattern !== 'string' || typeof label !== 'string') {
    return false;
  }
  return globRegex(pattern).test(label);
}

/**
 * Expands `$NAME`, `${NAME}` and `%NAME%`. Unknown variables fail closed so a
 * typo cannot accidentally become a literal path.
 */
export function expandPathVariables(
  input: string,
  env: Record<string, string | undefined>,
): string | null {
  if (typeof input !== 'string') {
    return null;
  }
  let unknownVariable = false;
  const expanded = input.replace(
    VARIABLE_PATTERN,
    (_match, braced: string | undefined, bare: string | undefined, percent: string | undefined) => {
      const name = braced ?? bare ?? percent;
      const value = lookupEnv(env, name);
      if (value === undefined || value.length === 0 || value.includes('\0')) {
        unknownVariable = true;
        return '';
      }
      return value;
    },
  );
  return unknownVariable ? null : expanded;
}

/**
 * Expands variables and normalizes separators, repeated slashes and dot
 * segments. Relative paths are rejected: path scopes must be absolute.
 */
export function normalizePath(
  input: string,
  options: Pick<CapabilityEngineOptions, 'env' | 'platform'> = {},
): NormalizedPath | null {
  if (typeof input !== 'string' || input.length === 0 || input.includes('\0')) {
    return null;
  }

  const platform = resolvePlatform(options.platform);
  const env = options.env ?? buildEnvironment(undefined, platform);
  const expanded = expandPathVariables(input, env);
  if (expanded === null) {
    return null;
  }

  let value = expanded;
  if (value.includes('\0')) {
    return null;
  }
  if (value === '~' || value.startsWith('~/') || value.startsWith('~\\')) {
    const home = lookupEnv(env, 'HOME') ?? lookupEnv(env, 'USERPROFILE');
    if (home === undefined || home.length === 0) {
      return null;
    }
    value = home + value.slice(1);
  }

  value = value.replace(/\\/g, '/');

  let root: string;
  let body: string;

  const drive = /^([A-Za-z]):(?:\/|$)/.exec(value);
  if (drive) {
    root = `${drive[1].toUpperCase()}:`;
    body = value.slice(2);
  } else if (platform === 'windows' && value.startsWith('//')) {
    // UNC root: //server/share. On POSIX, repeated leading slashes are
    // collapsed below like any other repeated separator.
    const parts = value.slice(2).split('/');
    if (parts.length < 2 || parts[0].length === 0 || parts[1].length === 0) {
      return null;
    }
    root = `//${parts[0]}/${parts[1]}`;
    body = parts.slice(2).join('/');
  } else if (value.startsWith('/')) {
    root = '/';
    body = value;
  } else {
    return null;
  }

  const segments: string[] = [];
  for (const part of body.split('/')) {
    if (part.length === 0 || part === '.') {
      continue;
    }
    if (part === '..') {
      // Like path.resolve, `..` is clamped at the filesystem root.
      if (segments.length > 0) {
        segments.pop();
      }
      continue;
    }
    segments.push(part);
  }

  return {
    root,
    segments,
    value: joinNormalized(root, segments),
  };
}

/**
 * Lexically matches a path against a scope pattern. The path and the pattern
 * are expanded and normalized first. `**` crosses directories; `*` does not.
 */
export function matchPathPattern(
  pattern: string,
  path: string,
  options: Pick<
    CapabilityEngineOptions,
    'env' | 'platform' | 'caseSensitivePaths'
  > = {},
): boolean {
  const platform = resolvePlatform(options.platform);
  const caseSensitive = options.caseSensitivePaths ?? platform !== 'windows';
  const env = options.env ?? buildEnvironment(undefined, platform);
  const compiled = compilePathPattern(pattern, env, caseSensitive, platform);
  const normalized = normalizePath(path, { env, platform });
  return (
    compiled !== null &&
    normalized !== null &&
    matchCompiledPath(compiled, normalized, caseSensitive)
  );
}

export function isLocalOrigin(origin: string): boolean {
  const parsed = parseUrl(origin);
  if (parsed === null) {
    return false;
  }
  return isLocalParsedUrl(parsed);
}

/**
 * Matches a remote URL against a capability `remote.urls` pattern. Origin
 * components are compared case-insensitively. A pattern without a path applies
 * to every path on that origin.
 */
export function matchRemoteUrl(pattern: string, url: string): boolean {
  const compiled = parseUrlPattern(pattern);
  const parsed = parseUrl(url);
  if (compiled === null || parsed === null) {
    return false;
  }
  return matchParsedUrlPattern(compiled, parsed);
}

export class CapabilityEngine {
  private readonly capabilities: CompiledCapability[];
  private readonly env: Record<string, string | undefined>;
  private readonly platform: PathPlatform;
  private readonly caseSensitivePaths: boolean;
  private readonly localOriginCheck: (origin: string) => boolean;

  constructor(capabilities: readonly Capability[] = [], options: CapabilityEngineOptions = {}) {
    this.platform = resolvePlatform(options.platform);
    this.caseSensitivePaths =
      options.caseSensitivePaths ?? this.platform !== 'windows';
    this.env = buildEnvironment(options.env, this.platform);
    this.localOriginCheck = options.isLocalOrigin ?? isLocalOrigin;
    this.capabilities = capabilities.map((capability) =>
      compileCapability(capability, this.env, this.caseSensitivePaths, this.platform),
    );
  }

  /**
   * Evaluates one IPC call. A call may carry several paths (for example a
   * command that reads multiple files); every path must pass.
   */
  check(request: CapabilityRequest): CapabilityDecision {
    if (
      !isRecord(request) ||
      typeof request.windowLabel !== 'string' ||
      request.windowLabel.length === 0 ||
      typeof request.permission !== 'string' ||
      request.permission.length === 0
    ) {
      return deny('invalid-request', 'windowLabel and permission are required');
    }

    const windowCapabilities = this.capabilities.filter((capability) =>
      capability.windows.some((pattern) => matchWindowLabel(pattern, request.windowLabel)),
    );

    if (windowCapabilities.length === 0) {
      return deny(
        'no-matching-capability',
        `no capability matches window label "${request.windowLabel}"`,
      );
    }

    const origin = parseOrigin(request.origin, this.localOriginCheck);
    if (origin.kind === 'invalid') {
      return deny(
        'remote-origin-denied',
        `webview origin "${origin.value}" is not a valid URL`,
        { origin: origin.value },
      );
    }

    const eligibleCapabilities = windowCapabilities.filter((capability) =>
      originMayUseCapability(capability, origin),
    );
    if (eligibleCapabilities.length === 0) {
      return deny(
        'remote-origin-denied',
        `webview origin "${origin.value}" is not allowed by the matching capability`,
        { origin: origin.value },
      );
    }

    const permissionEntries = eligibleCapabilities.flatMap((capability) =>
      capability.permissions
        .filter((permission) => permission.identifier === request.permission)
        .map((permission) => ({ capability, permission })),
    );

    if (permissionEntries.length === 0) {
      return deny(
        'permission-not-granted',
        `permission "${request.permission}" is not granted to window "${request.windowLabel}"`,
      );
    }

    const rawPaths = collectRequestPaths(request);
    if (rawPaths === null) {
      return deny('invalid-request', 'path and paths must be strings');
    }

    if (rawPaths.length === 0) {
      const unrestricted = permissionEntries.find(
        ({ permission }) => permission.allow === null && permission.deny.length === 0,
      );
      if (unrestricted) {
        return allow(unrestricted);
      }
      return deny(
        'path-required',
        `permission "${request.permission}" is path-scoped but no path was supplied`,
      );
    }

    const normalizedPaths: NormalizedPath[] = [];
    for (const rawPath of rawPaths) {
      const normalized = normalizePath(rawPath, {
        env: this.env,
        platform: this.platform,
      });
      if (normalized === null) {
        return deny('invalid-path', `path "${rawPath}" could not be normalized`, {
          path: rawPath,
        });
      }
      normalizedPaths.push(normalized);
    }

    // Deny always wins, across every matching permission entry.
    for (const { capability, permission } of permissionEntries) {
      for (const scope of permission.deny) {
        if (scope.path === null) {
          return deny(
            'path-denied',
            `deny scope "${scope.raw}" could not be expanded and normalized`,
            {
              capabilityIdentifier: capability.identifier,
              permission: permission.identifier,
              scope: scope.raw,
            },
          );
        }
        for (const normalizedPath of normalizedPaths) {
          if (matchCompiledPath(scope.path, normalizedPath, this.caseSensitivePaths)) {
            return deny(
              'path-denied',
              `path "${normalizedPath.value}" is denied by scope "${scope.raw}"`,
              {
                capabilityIdentifier: capability.identifier,
                permission: permission.identifier,
                path: normalizedPath.value,
                scope: scope.raw,
              },
            );
          }
        }
      }
    }

    const unrestricted = permissionEntries.find(
      ({ permission }) => permission.allow === null,
    );
    if (unrestricted) {
      return allow(unrestricted);
    }

    // Allow scopes from all matching permission entries form a union. Every
    // requested path must be covered by at least one of them.
    let grantedBy = permissionEntries[0];
    for (const normalizedPath of normalizedPaths) {
      const grant = permissionEntries.find(({ permission }) =>
        permission.allow!.some(
          (scope) =>
            scope.path !== null &&
            matchCompiledPath(scope.path, normalizedPath, this.caseSensitivePaths),
        ),
      );
      if (grant === undefined) {
        return deny(
          'path-not-allowed',
          `path "${normalizedPath.value}" is outside the allow scopes of "${request.permission}"`,
          { path: normalizedPath.value, permission: request.permission },
        );
      }
      grantedBy = grant;
    }
    return allow(grantedBy);
  }

  /** Convenience wrapper for callers that only need a boolean. */
  isAllowed(request: CapabilityRequest): boolean {
    return this.check(request).allowed;
  }
}

export function createCapabilityEngine(
  capabilities: readonly Capability[],
  options: CapabilityEngineOptions = {},
): CapabilityEngine {
  return new CapabilityEngine(capabilities, options);
}

export function checkCapability(
  capabilities: readonly Capability[],
  request: CapabilityRequest,
  options: CapabilityEngineOptions = {},
): CapabilityDecision {
  return new CapabilityEngine(capabilities, options).check(request);
}

function compileCapability(
  capability: Capability,
  env: Record<string, string | undefined>,
  caseSensitivePaths: boolean,
  platform: PathPlatform,
): CompiledCapability {
  const windows = Array.isArray(capability?.windows)
    ? capability.windows.filter((value): value is string => typeof value === 'string')
    : [];

  const remoteUrls =
    capability?.remote !== undefined && Array.isArray(capability.remote.urls)
      ? capability.remote.urls.filter((value): value is string => typeof value === 'string')
      : null;

  const permissions = Array.isArray(capability?.permissions)
    ? capability.permissions
        .map((entry) => compilePermission(entry, env, caseSensitivePaths, platform))
        .filter((entry): entry is CompiledPermission => entry !== null)
    : [];

  return {
    identifier: typeof capability?.identifier === 'string' ? capability.identifier : '',
    windows,
    remoteUrls,
    permissions,
  };
}

function compilePermission(
  entry: PermissionEntry,
  env: Record<string, string | undefined>,
  caseSensitivePaths: boolean,
  platform: PathPlatform,
): CompiledPermission | null {
  if (typeof entry === 'string') {
    if (entry.length === 0) {
      return null;
    }
    return { identifier: entry, allow: null, deny: [] };
  }

  if (!isRecord(entry) || typeof entry.identifier !== 'string' || entry.identifier.length === 0) {
    return null;
  }

  const hasAllow = Object.prototype.hasOwnProperty.call(entry, 'allow');
  let allow: CompiledScope[] | null;
  if (!hasAllow) {
    allow = null;
  } else {
    allow = Array.isArray(entry.allow)
      ? compileScopes(entry.allow, env, caseSensitivePaths, platform)
      : [];
  }

  const deny = Array.isArray(entry.deny)
    ? compileScopes(entry.deny, env, caseSensitivePaths, platform)
    : [];

  return { identifier: entry.identifier, allow, deny };
}

function compileScopes(
  scopes: readonly ScopeEntry[],
  env: Record<string, string | undefined>,
  caseSensitivePaths: boolean,
  platform: PathPlatform,
): CompiledScope[] {
  const compiled: CompiledScope[] = [];
  for (const scope of scopes) {
    const raw =
      typeof scope === 'string'
        ? scope
        : isRecord(scope) && typeof scope.path === 'string'
          ? scope.path
          : null;
    if (raw === null || raw.length === 0) {
      continue;
    }
    compiled.push({
      raw,
      path: compilePathPattern(raw, env, caseSensitivePaths, platform),
    });
  }
  return compiled;
}

function compilePathPattern(
  raw: string,
  env: Record<string, string | undefined>,
  caseSensitivePaths: boolean,
  platform: PathPlatform,
): CompiledPath | null {
  const normalized = normalizePath(raw, { env, platform });
  if (normalized === null) {
    return null;
  }
  const segments: PathSegmentPattern[] = normalized.segments.map((segment) =>
    segment === '**'
      ? { type: 'double' }
      : { type: 'segment', regex: globSegmentRegex(segment, caseSensitivePaths) },
  );
  return { root: normalized.root, segments };
}

function matchCompiledPath(
  pattern: CompiledPath,
  path: NormalizedPath,
  caseSensitivePaths: boolean,
): boolean {
  if (
    (caseSensitivePaths
      ? pattern.root !== path.root
      : pattern.root.toLowerCase() !== path.root.toLowerCase())
  ) {
    return false;
  }
  return matchSegments(pattern.segments, path.segments, 0, 0, new Map());
}

function matchSegments(
  pattern: readonly PathSegmentPattern[],
  path: readonly string[],
  patternIndex: number,
  pathIndex: number,
  memo: Map<string, boolean>,
): boolean {
  const key = `${patternIndex}:${pathIndex}`;
  const cached = memo.get(key);
  if (cached !== undefined) {
    return cached;
  }

  let result: boolean;
  if (patternIndex === pattern.length) {
    result = pathIndex === path.length;
  } else {
    const segment = pattern[patternIndex];
    if (segment.type === 'double') {
      result =
        matchSegments(pattern, path, patternIndex + 1, pathIndex, memo) ||
        (pathIndex < path.length &&
          matchSegments(pattern, path, patternIndex, pathIndex + 1, memo));
    } else {
      result =
        pathIndex < path.length &&
        segment.regex!.test(path[pathIndex]) &&
        matchSegments(pattern, path, patternIndex + 1, pathIndex + 1, memo);
    }
  }
  memo.set(key, result);
  return result;
}

function collectRequestPaths(request: CapabilityRequest): string[] | null {
  const paths: string[] = [];
  if (request.path !== undefined) {
    if (typeof request.path !== 'string') {
      return null;
    }
    paths.push(request.path);
  }
  if (request.paths !== undefined) {
    if (!Array.isArray(request.paths)) {
      return null;
    }
    for (const path of request.paths) {
      if (typeof path !== 'string') {
        return null;
      }
      paths.push(path);
    }
  }
  return paths;
}

function parseOrigin(
  origin: string | null | undefined,
  localOriginCheck: (origin: string) => boolean,
): OriginInfo {
  if (origin === undefined || origin === null) {
    return { kind: 'local', value: '' };
  }
  if (typeof origin !== 'string') {
    return { kind: 'invalid', value: '' };
  }
  if (origin.trim().length === 0) {
    return { kind: 'local', value: '' };
  }
  const value = origin.trim();
  const parsed = parseUrl(value);
  if (parsed === null) {
    return { kind: 'invalid', value };
  }
  return {
    kind: localOriginCheck(value) ? 'local' : 'remote',
    value,
    parsed,
  };
}

function originMayUseCapability(capability: CompiledCapability, origin: OriginInfo): boolean {
  if (origin.kind === 'local') {
    return true;
  }
  if (origin.kind !== 'remote' || origin.parsed === undefined) {
    return false;
  }
  if (capability.remoteUrls === null) {
    return false;
  }
  return capability.remoteUrls.some((pattern) =>
    matchParsedUrlPattern(parseUrlPattern(pattern), origin.parsed!),
  );
}

function parseUrl(value: string): ParsedUrl | null {
  if (typeof value !== 'string' || value.trim().length === 0) {
    return null;
  }
  try {
    const url = new URL(value.trim());
    const scheme = url.protocol.replace(/:$/, '').toLowerCase();
    if (scheme.length === 0) {
      return null;
    }
    return {
      scheme,
      host: normalizeHost(url.hostname),
      port: url.port,
      path: url.pathname.length === 0 ? '/' : url.pathname,
    };
  } catch {
    return null;
  }
}

function parseUrlPattern(pattern: string): ParsedUrlPattern | null {
  if (typeof pattern !== 'string') {
    return null;
  }
  const match =
    /^([A-Za-z*][A-Za-z0-9+.-]*):\/\/([^/?#]*)([^?#]*)(?:\?[^#]*)?(?:#.*)?$/.exec(
      pattern.trim(),
    );
  if (match === null) {
    return null;
  }

  const scheme = match[1].toLowerCase();
  let authority = match[2];
  // Credentials in a pattern do not form part of the origin.
  const at = authority.lastIndexOf('@');
  if (at >= 0) {
    authority = authority.slice(at + 1);
  }
  if (authority.length === 0) {
    return null;
  }

  let host: string;
  let port: string | null = null;
  if (authority.startsWith('[')) {
    const end = authority.indexOf(']');
    if (end < 0) {
      return null;
    }
    host = authority.slice(0, end + 1);
    const rest = authority.slice(end + 1);
    if (rest.length > 0) {
      if (!rest.startsWith(':')) {
        return null;
      }
      port = rest.slice(1);
    }
  } else {
    const colon = authority.lastIndexOf(':');
    if (colon >= 0) {
      const candidate = authority.slice(colon + 1);
      if (candidate === '*' || /^[0-9]+$/.test(candidate)) {
        host = authority.slice(0, colon);
        port = candidate;
      } else {
        host = authority;
      }
    } else {
      host = authority;
    }
  }

  if (host.length === 0 || (port !== null && port.length === 0)) {
    return null;
  }

  const rawPath = match[3];
  const path = rawPath.length === 0 || rawPath === '/' ? null : normalizeUrlPath(rawPath);
  if (rawPath.length > 0 && path === null) {
    return null;
  }

  return {
    scheme,
    host: normalizeHost(host),
    port,
    path,
  };
}

function matchParsedUrlPattern(
  pattern: ParsedUrlPattern | null,
  parsed: ParsedUrl,
): boolean {
  if (pattern === null) {
    return false;
  }
  if (pattern.scheme !== '*' && pattern.scheme !== parsed.scheme) {
    return false;
  }
  if (!globRegex(pattern.host).test(parsed.host)) {
    return false;
  }
  if (pattern.port !== null && pattern.port !== '*') {
    const effectivePort = parsed.port || DEFAULT_PORTS[parsed.scheme] || '';
    if (pattern.port !== effectivePort) {
      return false;
    }
  } else if (pattern.port === null && parsed.port.length > 0) {
    const defaultPort = DEFAULT_PORTS[parsed.scheme];
    if (defaultPort === undefined || parsed.port !== defaultPort) {
      return false;
    }
  }
  if (pattern.path !== null) {
    const parsedPath = normalizeUrlPath(parsed.path);
    if (parsedPath === null) {
      return false;
    }
    return matchCompiledPath(pattern.path, parsedPath, true);
  }
  return true;
}

function normalizeUrlPath(raw: string): CompiledPath | null {
  if (!raw.startsWith('/')) {
    return null;
  }
  const segments: string[] = [];
  for (const part of raw.split('/')) {
    if (part.length === 0 || part === '.') {
      continue;
    }
    if (part === '..') {
      if (segments.length > 0) {
        segments.pop();
      }
      continue;
    }
    segments.push(part);
  }
  return {
    root: '/',
    segments: segments.map((segment): PathSegmentPattern =>
      segment === '**'
        ? { type: 'double' }
        : { type: 'segment', regex: globSegmentRegex(segment, true) },
    ),
  };
}

function isLocalParsedUrl(parsed: ParsedUrl): boolean {
  if (parsed.scheme === 'file') {
    return true;
  }
  if (parsed.scheme === 'tauri' || parsed.scheme === 'asset') {
    return parsed.host === 'localhost' || parsed.host === '';
  }
  if (parsed.scheme === 'http' || parsed.scheme === 'https') {
    return (
      parsed.host === 'tauri.localhost' ||
      parsed.host === 'localhost' ||
      parsed.host === '127.0.0.1' ||
      parsed.host === '[::1]' ||
      parsed.host === '::1'
    );
  }
  return false;
}

function normalizeHost(host: string): string {
  return host.toLowerCase().replace(/\.$/, '');
}

function globSegmentRegex(segment: string, caseSensitive: boolean): RegExp {
  let source = '^';
  for (const character of segment) {
    if (character === '*') {
      source += '[^/]*';
    } else if (character === '?') {
      source += '[^/]';
    } else {
      source += escapeRegExp(character);
    }
  }
  // `$` would also match just before a trailing newline. Fail closed on the
  // real end of the input instead.
  source += '(?![\\s\\S])';
  return new RegExp(source, caseSensitive ? '' : 'i');
}

function globRegex(pattern: string): RegExp {
  let source = '^';
  for (const character of pattern) {
    if (character === '*') {
      source += '.*';
    } else if (character === '?') {
      source += '.';
    } else {
      source += escapeRegExp(character);
    }
  }
  source += '(?![\\s\\S])';
  return new RegExp(source);
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function joinNormalized(root: string, segments: readonly string[]): string {
  if (segments.length === 0) {
    return root === '/' ? '/' : `${root}/`;
  }
  if (root === '/') {
    return `/${segments.join('/')}`;
  }
  return `${root}/${segments.join('/')}`;
}

function lookupEnv(
  env: Record<string, string | undefined>,
  name: string,
): string | undefined {
  if (Object.prototype.hasOwnProperty.call(env, name)) {
    const exact = env[name];
    if (exact !== undefined) {
      return exact;
    }
  }
  const lowerName = name.toLowerCase();
  for (const key of Object.keys(env)) {
    if (key.toLowerCase() === lowerName) {
      const value = env[key];
      if (value !== undefined) {
        return value;
      }
    }
  }
  return undefined;
}

function getNodeProcess(): NodeProcess | undefined {
  return (globalThis as { process?: NodeProcess }).process;
}

function buildEnvironment(
  overrides: Record<string, string | undefined> | undefined,
  platform: PathPlatform,
): Record<string, string | undefined> {
  const nodeProcess = getNodeProcess();
  const env: Record<string, string | undefined> = {
    ...(nodeProcess?.env ?? {}),
    ...(overrides ?? {}),
  };

  const home = env.HOME ?? env.USERPROFILE;
  if (home !== undefined && home.length > 0) {
    env.HOME ??= home;
  }

  const os = nodeProcess?.platform;
  if (env.APPDATA === undefined && home !== undefined && home.length > 0) {
    env.APPDATA =
      platform === 'windows'
        ? `${home}\\AppData\\Roaming`
        : os === 'darwin'
          ? `${home}/Library/Application Support`
          : `${home}/.local/share`;
  }
  if (env.APPCONFIG === undefined && home !== undefined && home.length > 0) {
    env.APPCONFIG =
      platform === 'windows'
        ? `${home}\\AppData\\Roaming`
        : os === 'darwin'
          ? `${home}/Library/Application Support`
          : `${home}/.config`;
  }
  if (env.LOCALAPPDATA === undefined && platform === 'windows' && env.USERPROFILE !== undefined) {
    env.LOCALAPPDATA = `${env.USERPROFILE}\\AppData\\Local`;
  }
  return env;
}

function resolvePlatform(platformOption: PathPlatform | 'auto' | undefined): PathPlatform {
  if (platformOption === 'posix' || platformOption === 'windows') {
    return platformOption;
  }
  return getNodeProcess()?.platform === 'win32' ? 'windows' : 'posix';
}

function allow(entry: {
  capability: CompiledCapability;
  permission: CompiledPermission;
}): CapabilityDecision {
  return {
    allowed: true,
    capabilityIdentifier: entry.capability.identifier,
    permission: entry.permission.identifier,
  };
}

function deny(
  reason: CapabilityDenialReason,
  message: string,
  details: Partial<CapabilityDecision> = {},
): CapabilityDecision {
  return { allowed: false, reason, message, ...details };
}

function isRecord(value: unknown): value is Record<string, any> {
  return typeof value === 'object' && value !== null;
}
