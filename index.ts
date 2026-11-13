import { createHash, timingSafeEqual } from "node:crypto";

/**
 * An import-map entry can be either a plain URL or an object carrying the
 * integrity metadata that must be checked before the module is used.
 */
export type IntegrityMetadata = string | readonly string[];

export interface ImportMapEntryObject {
  url: string;
  integrity?: IntegrityMetadata;
  /**
   * Object entries require integrity by default. Set this to false only when
   * the entry is deliberately allowed to load without integrity metadata.
   */
  requireIntegrity?: boolean;
  /**
   * Accepted for compatibility with loaders that used to retry an entry
   * without integrity. Such retries are deliberately not performed here.
   */
  fallback?: string;
}

export type ImportMapEntry =
  | string
  | ImportMapEntryObject
  | null
  | undefined;

export type SpecifierMap = Record<string, ImportMapEntry>;

export interface ImportMapScope {
  imports?: SpecifierMap;
  integrity?: Record<string, IntegrityMetadata | undefined>;
}

export type ScopeValue = SpecifierMap | ImportMapScope;

export interface ImportMap {
  imports?: SpecifierMap;
  scopes?: Record<string, ScopeValue | undefined>;
  /**
   * Integrity metadata keyed by specifier, resolved URL, or scope-relative
   * URL. Both the flat import-map form and the scoped form are supported.
   */
  integrity?: Record<string, IntegrityMetadata | undefined>;
}

export interface ResolveOptions {
  /** Base URL used to resolve relative mappings and scope keys. */
  baseUrl?: string | URL;
  /** Alias for baseUrl. */
  base?: string | URL;
  /** URL of the module doing the importing. */
  moduleUrl?: string | URL;
  /** Aliases commonly used by module loaders. */
  referrer?: string | URL;
  parentUrl?: string | URL;
  importingUrl?: string | URL;
  importer?: string | URL;
  importerUrl?: string | URL;
}

export type ResolveOptionsInput = ResolveOptions | string | URL | undefined;

export interface ResolvedImport {
  specifier: string;
  /** The resolved URL, or null when the mapping is explicitly blocked. */
  url: string | null;
  integrity: IntegrityMetadata | undefined;
  integrityRequired: boolean;
  blocked: boolean;
  /** The scope prefix that won, or null when the top-level map was used. */
  scope: string | null;
  /** The import-map key that matched. */
  mapKey: string | null;
  matchType:
    | "scope-exact"
    | "scope-prefix"
    | "imports-exact"
    | "imports-prefix"
    | "url";
}

export interface ImportMapErrorOptions {
  code?: string;
  specifier?: string;
  cause?: unknown;
}

export class ImportMapError extends Error {
  code: string;
  specifier: string | undefined;

  constructor(message: string, options: ImportMapErrorOptions = {}) {
    super(message, options.cause === undefined ? undefined : { cause: options.cause });
    this.name = "ImportMapError";
    this.code = options.code ?? "IMPORT_MAP_ERROR";
    this.specifier = options.specifier;
  }
}

export type IntegrityFailureCode =
  | "INTEGRITY_MISSING"
  | "INTEGRITY_ALGORITHM_UNKNOWN"
  | "INTEGRITY_MALFORMED"
  | "INTEGRITY_MISMATCH";

export interface IntegrityErrorOptions {
  code: IntegrityFailureCode;
  metadata?: string;
  algorithm?: string;
  specifier?: string;
}

export class IntegrityError extends ImportMapError {
  code: IntegrityFailureCode;
  metadata: string | undefined;
  algorithm: string | undefined;

  constructor(message: string, options: IntegrityErrorOptions) {
    super(message, { code: options.code, specifier: options.specifier });
    this.name = "IntegrityError";
    this.code = options.code;
    this.metadata = options.metadata;
    this.algorithm = options.algorithm;
  }
}

export type ModuleContent = string | ArrayBuffer | ArrayBufferView;

export interface FetchResponse {
  ok?: boolean;
  status?: number;
  statusText?: string;
  arrayBuffer?: () => Promise<ArrayBuffer>;
  text?: () => Promise<string>;
}

export type FetchLike = (
  url: string,
  init?: { integrity?: string },
) =>
  | Promise<FetchResponse | ModuleContent>
  | FetchResponse
  | ModuleContent;

export interface LoadOptions extends ResolveOptions {
  fetch?: FetchLike;
  fetcher?: FetchLike;
  /**
   * Require integrity metadata for the resolved entry. This propagates to
   * otherwise plain string entries.
   */
  requireIntegrity?: boolean;
  /** Explicit metadata, mainly useful when a loader owns the fetch step. */
  integrity?: IntegrityMetadata;
  /** Forward the SRI string to fetch as its integrity option. */
  passIntegrityToFetch?: boolean;
}

export interface LoadedModule {
  specifier: string;
  url: string;
  content: Uint8Array;
  /** Alias for content. */
  bytes: Uint8Array;
  /** UTF-8 text form of content, convenient for source modules. */
  text: string;
  /** Alias for text, matching fetch terminology. */
  body: string;
  /** Alias for text, matching common module-loader terminology. */
  source: string;
  integrity: IntegrityMetadata;
  resolution: ResolvedImport;
}

type NormalizedEntry = {
  target: string | null;
  integrity: IntegrityMetadata | undefined;
  integrityRequired: boolean;
};

type MappingMatch = {
  key: string;
  value: ImportMapEntry;
  prefix: boolean;
};

type ScopeMatch = {
  key: string;
  value: ScopeValue;
  imports: SpecifierMap;
  integrity: Record<string, IntegrityMetadata | undefined>;
};

type IntegrityLookup = {
  found: boolean;
  metadata: IntegrityMetadata | undefined;
};

const SUPPORTED_ALGORITHMS = new Set(["sha256", "sha384", "sha512"]);

const DIGEST_LENGTHS: Record<string, number> = {
  sha256: 32,
  sha384: 48,
  sha512: 64,
};

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOwn(object: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(object, key);
}

function isUrl(value: string): boolean {
  try {
    // A bare specifier such as "pkg" is not accepted by the URL constructor
    // without a base. URL-like specifiers are left alone by import maps.
    new URL(value);
    return true;
  } catch {
    return false;
  }
}

function isRelativeUrl(value: string): boolean {
  return (
    value.startsWith("./") ||
    value.startsWith("../") ||
    value.startsWith("/") ||
    value.startsWith("?") ||
    value.startsWith("#")
  );
}

function asUrl(value: string | URL | undefined): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  return value instanceof URL ? value.href : value;
}

function normalizeBase(options: ResolveOptionsInput): string | undefined {
  if (options === undefined) {
    return undefined;
  }
  if (typeof options === "string" || options instanceof URL) {
    return asUrl(options);
  }
  return asUrl(options.baseUrl ?? options.base);
}

function referrerFromOptions(options: ResolveOptionsInput): string | undefined {
  if (options === undefined || typeof options === "string" || options instanceof URL) {
    return normalizeBase(options);
  }
  return asUrl(
    options.moduleUrl ??
      options.referrer ??
      options.parentUrl ??
      options.importingUrl ??
      options.importer ??
      options.importerUrl ??
      options.baseUrl ??
      options.base,
  );
}

function withModuleUrl(
  options: ResolveOptionsInput,
  moduleUrl: string | URL | undefined,
): ResolveOptionsInput {
  if (moduleUrl === undefined) {
    return options;
  }
  if (typeof options === "string" || options instanceof URL) {
    return { baseUrl: asUrl(options), moduleUrl };
  }
  if (isObject(options)) {
    return { ...(options as ResolveOptions), moduleUrl };
  }
  return { baseUrl: normalizeBase(options), moduleUrl };
}

/**
 * Resolve a possibly-relative URL against a base. Relative bases are handled
 * without throwing so the library remains useful in small in-process tests.
 */
export function resolveUrl(value: string, base?: string): string {
  if (isUrl(value)) {
    return new URL(value).href;
  }

  if (base !== undefined) {
    try {
      return new URL(value, base).href;
    } catch {
      // Fall through to the small relative-join fallback below.
    }
  }

  if (!isRelativeUrl(value)) {
    return value;
  }

  if (base === undefined) {
    return value;
  }

  if (value.startsWith("#") || value.startsWith("?")) {
    return value;
  }

  const baseWithoutQuery = base.split(/[?#]/, 1)[0] ?? base;
  const directory = baseWithoutQuery.endsWith("/")
    ? baseWithoutQuery
    : baseWithoutQuery.slice(0, baseWithoutQuery.lastIndexOf("/") + 1);
  const parts = `${directory}${value}`.split("/");
  const output: string[] = [];

  for (let index = 0; index < parts.length; index += 1) {
    const part = parts[index];
    if (part === "" || part === ".") {
      if (index === 0) {
        output.push("");
      }
      continue;
    }
    if (part === "..") {
      if (output.length > 1) {
        output.pop();
      }
      continue;
    }
    output.push(part);
  }

  const joined = output.join("/");
  return joined.startsWith("/") ? joined : `/${joined}`;
}

function normalizeEntry(entry: ImportMapEntry): NormalizedEntry {
  if (entry === null || entry === undefined) {
    return { target: null, integrity: undefined, integrityRequired: false };
  }
  if (typeof entry === "string") {
    return { target: entry, integrity: undefined, integrityRequired: false };
  }
  if (!isObject(entry) || typeof entry.url !== "string") {
    throw new ImportMapError("Invalid import-map entry: expected a URL or mapping object", {
      code: "INVALID_IMPORT_MAP_ENTRY",
    });
  }

  const integrity = entry.integrity as IntegrityMetadata | undefined;

  return {
    target: entry.url,
    integrity,
    // Object entries are integrity-bearing records. Unless explicitly opted
    // out, an absent or empty integrity value is a hard failure at load time.
    integrityRequired: entry.requireIntegrity !== false,
  };
}

function normalizeStore(
  store: Record<string, ImportMapEntry> | undefined,
): Record<string, ImportMapEntry> {
  return isObject(store) ? (store as Record<string, ImportMapEntry>) : {};
}

function normalizeIntegrityStore(
  store: Record<string, IntegrityMetadata | undefined> | undefined,
): Record<string, IntegrityMetadata | undefined> {
  return isObject(store)
    ? (store as Record<string, IntegrityMetadata | undefined>)
    : {};
}

function scopeDetails(value: ScopeValue | undefined): {
  imports: SpecifierMap;
  integrity: Record<string, IntegrityMetadata | undefined>;
} {
  if (!isObject(value)) {
    return { imports: {}, integrity: {} };
  }

  if (isObject(value.imports) || isObject(value.integrity)) {
    return {
      imports: normalizeStore(value.imports as SpecifierMap | undefined),
      integrity: normalizeIntegrityStore(
        value.integrity as Record<string, IntegrityMetadata | undefined> | undefined,
      ),
    };
  }

  return {
    imports: normalizeStore(value as SpecifierMap),
    integrity: {},
  };
}

function selectScope(
  scopes: Record<string, ScopeValue | undefined> | undefined,
  referrer: string | undefined,
  base: string | undefined,
): ScopeMatch | null {
  if (!isObject(scopes) || referrer === undefined) {
    return null;
  }

  let winner: ScopeMatch | null = null;
  let winnerLength = -1;

  for (const [scopeKey, scopeValue] of Object.entries(scopes)) {
    if (scopeValue === undefined) {
      continue;
    }

    const prefix = resolveUrl(scopeKey, base);
    if (!referrer.startsWith(prefix)) {
      continue;
    }

    // Comparing lengths, rather than trusting object insertion order, is what
    // prevents a short scope from hiding a more-specific long scope.
    if (prefix.length > winnerLength) {
      const details = scopeDetails(scopeValue);
      winner = {
        key: prefix,
        value: scopeValue,
        imports: details.imports,
        integrity: details.integrity,
      };
      winnerLength = prefix.length;
    }
  }

  return winner;
}

function findMapping(
  store: Record<string, ImportMapEntry>,
  specifier: string,
): MappingMatch | null {
  if (hasOwn(store, specifier)) {
    return { key: specifier, value: store[specifier], prefix: false };
  }

  let winnerKey: string | null = null;
  for (const key of Object.keys(store)) {
    if (!key.endsWith("/") || !specifier.startsWith(key)) {
      continue;
    }
    if (winnerKey === null || key.length > winnerKey.length) {
      winnerKey = key;
    }
  }

  if (winnerKey === null) {
    return null;
  }

  return { key: winnerKey, value: store[winnerKey], prefix: true };
}

function lookupIntegrityByKeys(
  store: Record<string, IntegrityMetadata | undefined> | undefined,
  keys: string[],
  base: string | undefined,
): IntegrityLookup {
  if (!store) {
    return { found: false, metadata: undefined };
  }

  for (const key of keys) {
    if (key !== "" && hasOwn(store, key)) {
      return { found: true, metadata: store[key] };
    }
  }

  for (const [rawKey, metadata] of Object.entries(store)) {
    const resolvedKey = resolveUrl(rawKey, base);
    if (keys.includes(resolvedKey) || keys.includes(rawKey)) {
      return { found: true, metadata };
    }
  }

  return { found: false, metadata: undefined };
}

function mappingTarget(
  match: MappingMatch,
  specifier: string,
  base: string | undefined,
): string {
  const entry = normalizeEntry(match.value);
  if (entry.target === null) {
    throw new ImportMapError(`Import map blocks specifier "${specifier}"`, {
      code: "IMPORT_MAP_BLOCKED",
      specifier,
    });
  }
  const target = match.prefix
    ? `${entry.target}${specifier.slice(match.key.length)}`
    : entry.target;
  return resolveUrl(target, base);
}

/**
 * Resolve an import-map specifier and retain the details needed by a loader.
 *
 * Scope selection is based on the longest matching scope prefix. Within the
 * selected scope an exact specifier key wins, followed by the longest key
 * ending in "/". If the scope has no matching key, the top-level imports map
 * is consulted.
 */
export function resolveImportMapDetailed(
  importMap: ImportMap,
  specifier: string,
  options?: ResolveOptionsInput,
  moduleUrl?: string | URL,
): ResolvedImport | null;
export function resolveImportMapDetailed(
  specifier: string,
  importMap: ImportMap,
  options?: ResolveOptionsInput,
  moduleUrl?: string | URL,
): ResolvedImport | null;
export function resolveImportMapDetailed(
  importMapOrSpecifier: ImportMap | string,
  specifierOrImportMap: string | ImportMap,
  options?: ResolveOptionsInput,
  moduleUrl?: string | URL,
): ResolvedImport | null {
  let importMap: ImportMap;
  let specifier: string;

  if (
    typeof importMapOrSpecifier === "string" &&
    isObject(specifierOrImportMap)
  ) {
    specifier = importMapOrSpecifier;
    importMap = specifierOrImportMap as ImportMap;
  } else {
    importMap = importMapOrSpecifier as ImportMap;
    specifier = specifierOrImportMap as string;
  }

  if (typeof specifier !== "string" || specifier.length === 0) {
    throw new ImportMapError("Specifier must be a non-empty string", {
      code: "INVALID_SPECIFIER",
    });
  }

  const map: ImportMap = isObject(importMap) ? importMap : {};
  const effectiveOptions = withModuleUrl(options, moduleUrl);
  const base = normalizeBase(effectiveOptions);
  const referrerValue = referrerFromOptions(effectiveOptions);
  const referrer =
    referrerValue === undefined ? undefined : resolveUrl(referrerValue, base);
  const scope = selectScope(map.scopes, referrer, base);

  let match: MappingMatch | null = null;
  let matchType: ResolvedImport["matchType"] = "imports-exact";

  if (scope) {
    const scopedMatch = findMapping(scope.imports, specifier);
    if (scopedMatch) {
      match = scopedMatch;
      matchType = scopedMatch.prefix ? "scope-prefix" : "scope-exact";
    }
  }

  if (match === null) {
    const topLevelMatch = findMapping(normalizeStore(map.imports), specifier);
    if (topLevelMatch) {
      match = topLevelMatch;
      matchType = topLevelMatch.prefix ? "imports-prefix" : "imports-exact";
    }
  }

  if (match === null) {
    if (isUrl(specifier) || isRelativeUrl(specifier)) {
      return {
        specifier,
        url: resolveUrl(specifier, base),
        integrity: undefined,
        integrityRequired: false,
        blocked: false,
        scope: scope?.key ?? null,
        mapKey: null,
        matchType: "url",
      };
    }
    return null;
  }

  const entry = normalizeEntry(match.value);
  if (entry.target === null) {
    return {
      specifier,
      url: null,
      integrity: entry.integrity,
      integrityRequired: entry.integrityRequired,
      blocked: true,
      scope: scope?.key ?? null,
      mapKey: match.key,
      matchType,
    };
  }

  const target = mappingTarget(match, specifier, base);
  const topLevelIntegrity = normalizeIntegrityStore(map.integrity);
  const scopeIntegrity = scope?.integrity ?? {};
  const keys = [specifier, match.key, entry.target, target];
  let scopedLookup = lookupIntegrityByKeys(scopeIntegrity, keys, base);
  if (!scopedLookup.found && scope) {
    scopedLookup = lookupIntegrityByKeys(scopeIntegrity, keys, scope.key);
  }
  const topLevelLookup = lookupIntegrityByKeys(topLevelIntegrity, keys, base);
  const fromMap = scopedLookup.metadata ?? topLevelLookup.metadata;

  return {
    specifier,
    url: target,
    integrity: entry.integrity ?? fromMap,
    integrityRequired:
      entry.integrityRequired || scopedLookup.found || topLevelLookup.found,
    blocked: false,
    scope: scope?.key ?? null,
    mapKey: match.key,
    matchType,
  };
}

/**
 * Resolve a specifier to its URL, or null when the specifier is not mapped or
 * is explicitly blocked.
 */
export function resolveImportMap(
  importMap: ImportMap,
  specifier: string,
  options?: ResolveOptionsInput,
  moduleUrl?: string | URL,
): string | null;
export function resolveImportMap(
  specifier: string,
  importMap: ImportMap,
  options?: ResolveOptionsInput,
  moduleUrl?: string | URL,
): string | null;
export function resolveImportMap(
  importMapOrSpecifier: ImportMap | string,
  specifierOrImportMap: string | ImportMap,
  options?: ResolveOptionsInput,
  moduleUrl?: string | URL,
): string | null {
  return (
    resolveImportMapDetailed(
      importMapOrSpecifier as ImportMap,
      specifierOrImportMap as string,
      options,
      moduleUrl,
    )?.url ?? null
  );
}

export const resolveImportMapSpecifier = resolveImportMap;
export const resolveImportMapUrl = resolveImportMap;
export const resolveImportMapEntry = resolveImportMapDetailed;
export const resolveDetailed = resolveImportMapDetailed;
export const resolveSpecifier = resolveImportMap;
export const resolveImport = resolveImportMap;
export const resolve = resolveImportMap;

function integrityTokens(metadata: IntegrityMetadata | undefined): string[] {
  if (metadata === undefined || metadata === null) {
    return [];
  }
  if (typeof metadata === "string") {
    return metadata.split(/\s+/).filter((token) => token.length > 0);
  }
  if (Array.isArray(metadata)) {
    const tokens: string[] = [];
    for (const item of metadata) {
      tokens.push(...integrityTokens(item as IntegrityMetadata));
    }
    return tokens;
  }
  throw new IntegrityError("Integrity metadata must be a string or string array", {
    code: "INTEGRITY_MALFORMED",
    metadata: String(metadata),
  });
}

function toBytes(content: ModuleContent): Uint8Array {
  if (typeof content === "string") {
    return new TextEncoder().encode(content);
  }
  if (content instanceof ArrayBuffer) {
    return new Uint8Array(content);
  }
  if (ArrayBuffer.isView(content)) {
    return new Uint8Array(content.buffer, content.byteOffset, content.byteLength);
  }
  throw new TypeError("Module content must be a string, ArrayBuffer, or ArrayBufferView");
}

function parseIntegrityToken(
  token: string,
  specifier?: string,
): { algorithm: string; digest: Buffer } {
  const withoutOptions = token.split("?", 1)[0] ?? token;
  const separator = withoutOptions.indexOf("-");

  if (separator <= 0 || separator === withoutOptions.length - 1) {
    throw new IntegrityError(`Malformed integrity token "${token}"`, {
      code: "INTEGRITY_MALFORMED",
      metadata: token,
      specifier,
    });
  }

  const algorithm = withoutOptions.slice(0, separator).toLowerCase();
  const encodedDigest = withoutOptions.slice(separator + 1);

  if (!SUPPORTED_ALGORITHMS.has(algorithm)) {
    throw new IntegrityError(
      `Unsupported integrity algorithm "${algorithm}" in "${token}"`,
      {
        code: "INTEGRITY_ALGORITHM_UNKNOWN",
        metadata: token,
        algorithm,
        specifier,
      },
    );
  }

  if (!/^[A-Za-z0-9+/_-]+={0,2}$/.test(encodedDigest)) {
    throw new IntegrityError(`Malformed integrity digest "${token}"`, {
      code: "INTEGRITY_MALFORMED",
      metadata: token,
      algorithm,
      specifier,
    });
  }

  const digest = Buffer.from(encodedDigest.replace(/-/g, "+").replace(/_/g, "/"), "base64");
  const expectedLength = DIGEST_LENGTHS[algorithm];
  if (digest.byteLength !== expectedLength) {
    throw new IntegrityError(
      `Integrity digest for "${algorithm}" has the wrong length`,
      {
        code: "INTEGRITY_MALFORMED",
        metadata: token,
        algorithm,
        specifier,
      },
    );
  }

  return { algorithm, digest };
}

function integrityString(metadata: IntegrityMetadata): string {
  return typeof metadata === "string" ? metadata : metadata.join(" ");
}

/**
 * Verify SRI metadata against module content.
 *
 * Missing metadata, unsupported algorithms, malformed values, and digest
 * mismatches all reject. Verification never returns a URL that can be fetched
 * again without integrity.
 */
export async function verifyIntegrity(
  content: ModuleContent,
  metadata: IntegrityMetadata | undefined,
  options: { specifier?: string } = {},
): Promise<true> {
  return verifyIntegritySync(content, metadata, options);
}

export function verifyIntegritySync(
  content: ModuleContent,
  metadata: IntegrityMetadata | undefined,
  options: { specifier?: string } = {},
): true {
  const tokens = integrityTokens(metadata);

  if (tokens.length === 0) {
    throw new IntegrityError("Integrity metadata is missing", {
      code: "INTEGRITY_MISSING",
      specifier: options.specifier,
    });
  }

  const bytes = toBytes(content);
  const parsed = tokens.map((token) => parseIntegrityToken(token, options.specifier));

  for (const { algorithm, digest } of parsed) {
    const actual = createHash(algorithm).update(bytes).digest();
    if (actual.byteLength === digest.byteLength && timingSafeEqual(actual, digest)) {
      return true;
    }
  }

  throw new IntegrityError("Integrity digest does not match module content", {
    code: "INTEGRITY_MISMATCH",
    metadata: integrityString(metadata as IntegrityMetadata),
    specifier: options.specifier,
  });
}

export const verifySri = verifyIntegrity;

/**
 * Boolean form of integrity verification. Use verifyIntegrity when the
 * failure reason is needed.
 */
export function checkIntegrity(
  content: ModuleContent,
  metadata: IntegrityMetadata | undefined,
  options: { specifier?: string } = {},
): boolean {
  try {
    verifyIntegritySync(content, metadata, options);
    return true;
  } catch (error) {
    if (error instanceof IntegrityError) {
      return false;
    }
    throw error;
  }
}

function responseLike(value: unknown): value is FetchResponse {
  return (
    isObject(value) &&
    (typeof (value as FetchResponse).arrayBuffer === "function" ||
      typeof (value as FetchResponse).text === "function")
  );
}

function wrappedContent(value: unknown): ModuleContent | undefined {
  if (!isObject(value)) {
    return undefined;
  }
  const candidate = value.content ?? value.body ?? value.source ?? value.text;
  if (
    typeof candidate === "string" ||
    candidate instanceof ArrayBuffer ||
    ArrayBuffer.isView(candidate)
  ) {
    return candidate as ModuleContent;
  }
  return undefined;
}

async function readResponse(response: FetchResponse, url: string): Promise<Uint8Array> {
  if (response.ok === false) {
    throw new ImportMapError(
      `Failed to load "${url}": ${response.status ?? "HTTP error"} ${
        response.statusText ?? ""
      }`.trim(),
      { code: "FETCH_FAILED" },
    );
  }
  if (typeof response.arrayBuffer === "function") {
    return new Uint8Array(await response.arrayBuffer());
  }
  if (typeof response.text === "function") {
    return new TextEncoder().encode(await response.text());
  }
  throw new TypeError(`Fetch response for "${url}" has no readable body`);
}

function getFetch(options: LoadOptions): FetchLike {
  const fetcher = options.fetch ?? options.fetcher ?? (globalThis.fetch as FetchLike | undefined);
  if (typeof fetcher !== "function") {
    throw new ImportMapError("No fetch implementation was provided", {
      code: "FETCH_UNAVAILABLE",
    });
  }
  return fetcher;
}

/**
 * Resolve, fetch, and verify one module. Exactly one fetch is issued for the
 * resolved URL; an integrity failure is terminal and never retried against a
 * URL without integrity.
 */
export async function loadModule(
  importMap: ImportMap,
  specifier: string,
  options: LoadOptions = {},
): Promise<LoadedModule> {
  const resolution = resolveImportMapDetailed(importMap, specifier, options);

  if (!resolution || resolution.blocked || resolution.url === null) {
    throw new ImportMapError(
      resolution?.blocked
        ? `Import map blocks specifier "${specifier}"`
        : `Specifier "${specifier}" is not present in the import map`,
      {
        code: resolution?.blocked ? "IMPORT_MAP_BLOCKED" : "SPECIFIER_NOT_MAPPED",
        specifier,
      },
    );
  }

  const integrity = options.integrity ?? resolution.integrity;
  const required = options.requireIntegrity ?? resolution.integrityRequired;

  if (required && integrityTokens(integrity).length === 0) {
    throw new IntegrityError(
      `Integrity metadata is missing for specifier "${specifier}" (${resolution.url})`,
      { code: "INTEGRITY_MISSING", specifier },
    );
  }

  const fetcher = getFetch(options);
  const init =
    options.passIntegrityToFetch && integrity !== undefined
      ? { integrity: integrityString(integrity) }
      : undefined;
  let response: FetchResponse | ModuleContent;

  try {
    response = await fetcher(resolution.url, init);
  } catch (error) {
    if (error instanceof ImportMapError) {
      throw error;
    }
    throw new ImportMapError(`Failed to load "${resolution.url}"`, {
      code: "FETCH_FAILED",
      specifier,
      cause: error,
    });
  }

  const content = responseLike(response)
    ? await readResponse(response, resolution.url)
    : toBytes(wrappedContent(response) ?? (response as ModuleContent));

  if (integrity !== undefined) {
    await verifyIntegrity(content, integrity, { specifier });
  }

  const text = new TextDecoder().decode(content);

  return {
    specifier,
    url: resolution.url,
    content,
    bytes: content,
    text,
    body: text,
    source: text,
    integrity: integrity as IntegrityMetadata,
    resolution,
  };
}

export const load = loadModule;

export async function loadModuleText(
  importMap: ImportMap,
  specifier: string,
  options: LoadOptions = {},
): Promise<string> {
  return (await loadModule(importMap, specifier, options)).text;
}

export async function loadModuleBytes(
  importMap: ImportMap,
  specifier: string,
  options: LoadOptions = {},
): Promise<Uint8Array> {
  return (await loadModule(importMap, specifier, options)).content;
}

export const loadText = loadModuleText;
export const loadBytes = loadModuleBytes;

export interface ModuleLoader {
  /** Callable shorthand for resolve(specifier, options). */
  (specifier: string, options?: ResolveOptionsInput): string | null;
  importMap: ImportMap;
  resolve: (specifier: string, options?: ResolveOptionsInput) => string | null;
  resolveDetailed: (
    specifier: string,
    options?: ResolveOptionsInput,
  ) => ResolvedImport | null;
  load: (specifier: string, options?: LoadOptions) => Promise<LoadedModule>;
  loadText: (specifier: string, options?: LoadOptions) => Promise<string>;
  loadBytes: (specifier: string, options?: LoadOptions) => Promise<Uint8Array>;
}

/**
 * Create a reusable loader bound to one import map and optional defaults.
 */
export function createModuleLoader(
  importMap: ImportMap,
  defaults: LoadOptions = {},
): ModuleLoader {
  const loader = ((specifier: string, options?: ResolveOptionsInput) =>
    resolveImportMap(importMap, specifier, options ?? defaults)) as ModuleLoader;

  loader.importMap = importMap;
  loader.resolve = (specifier, options) =>
    resolveImportMap(importMap, specifier, options ?? defaults);
  loader.resolveDetailed = (specifier, options) =>
    resolveImportMapDetailed(importMap, specifier, options ?? defaults);
  loader.load = (specifier, options) =>
    loadModule(importMap, specifier, { ...defaults, ...options });
  loader.loadText = (specifier, options) =>
    loadModuleText(importMap, specifier, { ...defaults, ...options });
  loader.loadBytes = (specifier, options) =>
    loadModuleBytes(importMap, specifier, { ...defaults, ...options });

  return loader;
}

export const createImportMapResolver = createModuleLoader;
export const createResolver = createModuleLoader;

export default {
  resolveImportMap,
  resolveImportMapDetailed,
  resolveImportMapSpecifier,
  resolveImportMapUrl,
  resolveImportMapEntry,
  resolveDetailed,
  resolveSpecifier,
  resolveImport,
  resolve,
  verifyIntegrity,
  verifyIntegritySync,
  checkIntegrity,
  verifySri,
  loadModule,
  load,
  loadModuleText,
  loadModuleBytes,
  loadText,
  loadBytes,
  createModuleLoader,
  createImportMapResolver,
  createResolver,
  ImportMapError,
  IntegrityError,
};
