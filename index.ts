export type ExportsTarget = string | null | ExportsConditions;

export interface ExportsConditions {
  readonly [condition: string]: ExportsTarget;
}

export type ExportsMap =
  | ExportsTarget
  | { readonly [subpath: string]: ExportsTarget };

export interface ResolveExportsOptions {
  /**
   * Conditions considered active while walking a conditional target.
   * Keys are still matched in the declaration order of the object.
   * `default` is always active.
   */
  conditions?: readonly string[];
}

export type ExportsResolutionErrorCode =
  | "ERR_PACKAGE_PATH_NOT_EXPORTED"
  | "ERR_INVALID_PACKAGE_CONFIG"
  | "ERR_INVALID_PACKAGE_TARGET"
  | "ERR_INVALID_PACKAGE_SUBPATH";

export class ExportsResolutionError extends Error {
  readonly code: ExportsResolutionErrorCode;
  readonly packageName: string;
  readonly subpath: string;

  constructor(
    code: ExportsResolutionErrorCode,
    message: string,
    packageName: string,
    subpath: string,
  ) {
    super(message);
    this.name = "ExportsResolutionError";
    this.code = code;
    this.packageName = packageName;
    this.subpath = subpath;
  }
}

export interface ResolvedExport {
  packageName: string;
  subpath: string;
  target: string;
  specifier: string;
}

const DEFAULT_CONDITIONS = ["import", "node", "default"] as const;
const NO_MATCH = Symbol("NO_MATCH");
const BLOCKED = Symbol("BLOCKED");

type SearchResult =
  | { value: ExportsTarget; replacement?: string }
  | typeof BLOCKED;

interface PatternMatch {
  key: string;
  prefixLength: number;
  replacement: string;
  value: ExportsTarget;
}

/**
 * Resolves a package subpath through an `exports` map.
 *
 * The returned target is normalized, relative to the package root, and always
 * starts with `./`. This function never probes the file system: an unmapped
 * subpath is rejected rather than treated as a direct file path.
 */
export function resolveExports(
  packageName: string,
  exportsMap: ExportsMap,
  subpath: string,
  options: ResolveExportsOptions = {},
): string {
  assertValidPackageName(packageName, subpath);
  const request = normalizeSubpath(packageName, subpath);
  const activeConditions = new Set(options.conditions ?? DEFAULT_CONDITIONS);
  activeConditions.add("default");

  const result = findExport(exportsMap, request, packageName, subpath);
  if (result === BLOCKED) {
    throw pathNotExported(packageName, subpath);
  }

  const target = selectTarget(result.value, activeConditions, packageName, subpath);
  if (target === NO_MATCH || target === BLOCKED) {
    throw pathNotExported(packageName, subpath);
  }

  let replacement: string | undefined;
  if (result.replacement !== undefined) {
    replacement = validateWildcardReplacement(
      result.replacement,
      packageName,
      subpath,
    );
  }

  return validateTarget(target, replacement, packageName, subpath);
}

/**
 * Same resolution as {@link resolveExports}, but returns `null` for every
 * deliberate rejection. Unexpected programmer errors are re-thrown.
 */
export function tryResolveExports(
  packageName: string,
  exportsMap: ExportsMap,
  subpath: string,
  options: ResolveExportsOptions = {},
): string | null {
  try {
    return resolveExports(packageName, exportsMap, subpath, options);
  } catch (error) {
    if (error instanceof ExportsResolutionError) {
      return null;
    }
    throw error;
  }
}

/**
 * Converts the safe package-relative target into a canonical package
 * specifier, for example `pkg` plus `./dist/a.js` becomes `pkg/dist/a.js`.
 */
export function resolveModuleSpecifier(
  packageName: string,
  exportsMap: ExportsMap,
  subpath: string,
  options: ResolveExportsOptions = {},
): string {
  const target = resolveExports(packageName, exportsMap, subpath, options);
  return target === "./"
    ? packageName
    : `${packageName}/${target.slice(2)}`;
}

export function resolveExportsResult(
  packageName: string,
  exportsMap: ExportsMap,
  subpath: string,
  options: ResolveExportsOptions = {},
): ResolvedExport {
  const target = resolveExports(packageName, exportsMap, subpath, options);
  const normalizedSubpath = normalizeSubpath(packageName, subpath, false);

  return {
    packageName,
    subpath: normalizedSubpath,
    target,
    specifier:
      target === "./"
        ? packageName
        : `${packageName}/${target.slice(2)}`,
  };
}

/**
 * Alias retained for callers that use the more explicit package-oriented name.
 */
export const resolvePackageExports = resolveExports;
export const resolveExport = resolveExports;
export const resolvePackageSubpath = resolveExports;
export const resolveExportTarget = resolveExports;
export const resolveExportsTarget = resolveExports;

export default resolveExports;

function findExport(
  exportsMap: ExportsMap,
  request: string,
  packageName: string,
  subpath: string,
): SearchResult {
  if (exportsMap === null) {
    return BLOCKED;
  }

  if (typeof exportsMap === "string") {
    return request === "."
      ? { value: exportsMap }
      : BLOCKED;
  }

  if (!isPlainObject(exportsMap)) {
    throw invalidConfig(
      packageName,
      subpath,
      "exports must be a string, null, a condition object, or a subpath map",
    );
  }

  const keys = Object.keys(exportsMap);
  const hasSubpathKeys = keys.some((key) => key.startsWith("."));

  if (!hasSubpathKeys) {
    if (request !== ".") {
      return BLOCKED;
    }
    return { value: exportsMap as ExportsConditions };
  }

  if (keys.some((key) => !key.startsWith("."))) {
    throw invalidConfig(
      packageName,
      subpath,
      "exports cannot mix subpath keys with condition keys",
    );
  }

  const subpathMap = exportsMap as {
    readonly [subpath: string]: ExportsTarget;
  };

  if (Object.prototype.hasOwnProperty.call(subpathMap, request)) {
    const value = subpathMap[request];
    if (value === null) {
      return BLOCKED;
    }
    return { value };
  }

  let bestMatch: PatternMatch | undefined;

  for (const key of keys) {
    const starIndex = key.indexOf("*");
    if (starIndex === -1) {
      continue;
    }
    if (key.indexOf("*", starIndex + 1) !== -1) {
      throw invalidConfig(
        packageName,
        subpath,
        `subpath pattern ${JSON.stringify(key)} must contain exactly one *`,
      );
    }

    const prefix = key.slice(0, starIndex);
    const suffix = key.slice(starIndex + 1);
    if (!request.startsWith(prefix) || !request.endsWith(suffix)) {
      continue;
    }
    if (request.length < prefix.length + suffix.length) {
      continue;
    }

    const replacement = request.slice(
      prefix.length,
      request.length - suffix.length,
    );
    const candidate: PatternMatch = {
      key,
      prefixLength: prefix.length,
      replacement,
      value: subpathMap[key],
    };

    if (
      bestMatch === undefined ||
      candidate.prefixLength > bestMatch.prefixLength ||
      (candidate.prefixLength === bestMatch.prefixLength &&
        candidate.key.length > bestMatch.key.length)
    ) {
      bestMatch = candidate;
    }
  }

  if (bestMatch === undefined) {
    return BLOCKED;
  }
  if (bestMatch.value === null) {
    return BLOCKED;
  }
  return {
    value: bestMatch.value,
    replacement: bestMatch.replacement,
  };
}

function selectTarget(
  value: ExportsTarget,
  activeConditions: ReadonlySet<string>,
  packageName: string,
  subpath: string,
): string | typeof NO_MATCH | typeof BLOCKED {
  if (value === null) {
    return BLOCKED;
  }
  if (typeof value === "string") {
    return value;
  }
  if (!isPlainObject(value)) {
    throw invalidConfig(
      packageName,
      subpath,
      "export target must be a string, null, or condition object",
    );
  }

  for (const condition of Object.keys(value)) {
    if (!activeConditions.has(condition)) {
      continue;
    }

    const selected = selectTarget(
      (value as ExportsConditions)[condition],
      activeConditions,
      packageName,
      subpath,
    );
    if (selected === NO_MATCH) {
      continue;
    }
    return selected;
  }

  return NO_MATCH;
}

function validateWildcardReplacement(
  replacement: string,
  packageName: string,
  subpath: string,
): string {
  if (replacement.length === 0) {
    throw invalidSubpath(
      packageName,
      subpath,
      "wildcard replacement must not be empty",
    );
  }

  const decoded = decodePath(replacement, packageName, subpath);
  assertSafeSegments(decoded, packageName, subpath, "wildcard replacement");

  if (decoded.includes("*")) {
    throw invalidSubpath(
      packageName,
      subpath,
      "wildcard replacement must not contain another *",
    );
  }

  return decoded;
}

function validateTarget(
  rawTarget: string,
  replacement: string | undefined,
  packageName: string,
  subpath: string,
): string {
  if (!rawTarget.startsWith("./")) {
    throw invalidTarget(
      packageName,
      subpath,
      `export target ${JSON.stringify(rawTarget)} must start with ./`,
    );
  }

  let expandedTarget = rawTarget;
  if (replacement !== undefined && rawTarget.includes("*")) {
    expandedTarget = rawTarget.split("*").join(replacement);
  }

  const decoded = decodePath(expandedTarget, packageName, subpath);
  if (!decoded.startsWith("./")) {
    throw invalidTarget(
      packageName,
      subpath,
      "decoded export target must start with ./",
    );
  }

  const segments = decoded.slice(2).split("/");
  const normalized: string[] = [];

  for (const segment of segments) {
    if (segment.length === 0) {
      continue;
    }
    if (segment === "." || segment === "..") {
      throw invalidTarget(
        packageName,
        subpath,
        "export target must not contain . or .. path segments",
      );
    }
    if (segment.toLowerCase() === "node_modules") {
      throw invalidTarget(
        packageName,
        subpath,
        "export target must not contain a node_modules segment",
      );
    }
    if (segment.includes("\0")) {
      throw invalidTarget(
        packageName,
        subpath,
        "export target must not contain NUL bytes",
      );
    }
    normalized.push(segment);
  }

  return `./${normalized.join("/")}`;
}

function normalizeSubpath(
  packageName: string,
  subpath: string,
  validate = true,
): string {
  if (typeof subpath !== "string" || subpath.length === 0) {
    throw invalidSubpath(
      packageName,
      String(subpath),
      "requested subpath must be a non-empty string",
    );
  }
  if (
    subpath.startsWith("/") ||
    subpath.startsWith("\\") ||
    subpath.startsWith("../") ||
    subpath === ".."
  ) {
    throw invalidSubpath(
      packageName,
      subpath,
      "requested subpath must stay inside the package",
    );
  }

  if (subpath === "." || subpath === "./") {
    return ".";
  }

  const request = subpath.startsWith("./") ? subpath : `./${subpath}`;
  if (validate) {
    assertSafeSegments(
      decodePath(request, packageName, subpath),
      packageName,
      subpath,
      "requested subpath",
    );
  }
  return request;
}

function decodePath(
  value: string,
  packageName: string,
  subpath: string,
): string {
  let decoded = value;

  for (let depth = 0; depth < 8; depth += 1) {
    if (/[\\\0]/u.test(decoded)) {
      throw invalidSubpath(
        packageName,
        subpath,
        "path values must not contain backslashes or NUL bytes",
      );
    }
    if (/%2f|%5c/iu.test(decoded)) {
      throw invalidSubpath(
        packageName,
        subpath,
        "path values must not contain percent-encoded / or \\ separators",
      );
    }
    if (!/%[0-9a-f]{2}/iu.test(decoded)) {
      if (decoded.includes("%")) {
        throw invalidSubpath(
          packageName,
          subpath,
          "path values must not contain malformed percent encoding",
        );
      }
      return decoded;
    }

    let next: string;
    try {
      next = decodeURIComponent(decoded);
    } catch {
      throw invalidSubpath(
        packageName,
        subpath,
        "path values must not contain malformed percent encoding",
      );
    }
    if (next === decoded) {
      return decoded;
    }
    decoded = next;
  }

  throw invalidSubpath(
    packageName,
    subpath,
    "path values must not use deeply nested percent encoding",
  );
}

function assertSafeSegments(
  decoded: string,
  packageName: string,
  subpath: string,
  what: string,
): void {
  const segments = decoded.split("/");
  if (segments[0] === ".") {
    segments.shift();
  }

  for (const segment of segments) {
    if (segment === "." || segment === ".." || segment.length === 0) {
      throw invalidSubpath(
        packageName,
        subpath,
        `${what} must not contain empty, . or .. segments`,
      );
    }
    if (segment.toLowerCase() === "node_modules") {
      throw invalidSubpath(
        packageName,
        subpath,
        `${what} must not contain a node_modules segment`,
      );
    }
  }
}

function assertValidPackageName(packageName: string, subpath: string): void {
  if (typeof packageName !== "string" || packageName.length === 0) {
    throw invalidConfig(packageName, subpath, "package name must be non-empty");
  }
  if (
    packageName.startsWith("/") ||
    packageName.startsWith("\\") ||
    packageName.includes("\\") ||
    packageName.includes("\0")
  ) {
    throw invalidConfig(
      packageName,
      subpath,
      "package name must not contain absolute path or backslash syntax",
    );
  }

  const decoded = decodePath(packageName, packageName, subpath);
  for (const segment of decoded.split("/")) {
    if (segment === "." || segment === ".." || segment.length === 0) {
      throw invalidConfig(
        packageName,
        subpath,
        "package name must not contain empty, . or .. segments",
      );
    }
    if (segment.toLowerCase() === "node_modules") {
      throw invalidConfig(
        packageName,
        subpath,
        "package name must not contain a node_modules segment",
      );
    }
  }
}

function isPlainObject(
  value: unknown,
): value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function pathNotExported(
  packageName: string,
  subpath: string,
): ExportsResolutionError {
  return new ExportsResolutionError(
    "ERR_PACKAGE_PATH_NOT_EXPORTED",
    `Package subpath ${JSON.stringify(subpath)} is not exported by ${JSON.stringify(packageName)}`,
    packageName,
    subpath,
  );
}

function invalidConfig(
  packageName: string,
  subpath: string,
  detail: string,
): ExportsResolutionError {
  return new ExportsResolutionError(
    "ERR_INVALID_PACKAGE_CONFIG",
    `Invalid exports configuration: ${detail}`,
    packageName,
    subpath,
  );
}

function invalidTarget(
  packageName: string,
  subpath: string,
  detail: string,
): ExportsResolutionError {
  return new ExportsResolutionError(
    "ERR_INVALID_PACKAGE_TARGET",
    `Invalid export target: ${detail}`,
    packageName,
    subpath,
  );
}

function invalidSubpath(
  packageName: string,
  subpath: string,
  detail: string,
): ExportsResolutionError {
  return new ExportsResolutionError(
    "ERR_INVALID_PACKAGE_SUBPATH",
    `Invalid package subpath: ${detail}`,
    packageName,
    subpath,
  );
}
