const FORBIDDEN_KEYS = new Set([
  "__proto__",
  "constructor",
  "prototype",
  "__defineGetter__",
  "__defineSetter__",
  "__lookupGetter__",
  "__lookupSetter__",
]);

const SAFE_KEY_PATTERN = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/;
const MAX_KEY_LENGTH = 64;
const UTF8_ENCODER = new TextEncoder();

export type ConfigPrimitive = string | number | boolean | null;

export type ConfigValue =
  | ConfigPrimitive
  | readonly ConfigValue[]
  | ConfigObject;

export interface ConfigObject {
  readonly [key: string]: ConfigValue;
}

export interface MergeLimits {
  readonly maxDepth: number;
  readonly maxNodes: number;
  readonly maxBytes: number;
}

export type KeyAllowlist =
  | ReadonlySet<string>
  | readonly string[]
  | ((key: string, path: readonly string[]) => boolean);

export interface MergeOptions {
  readonly limits?: Partial<MergeLimits>;
  readonly maxDepth?: number;
  readonly maxNodes?: number;
  readonly maxBytes?: number;
  readonly allowedKeys?: KeyAllowlist;
  readonly keyAllowlist?: KeyAllowlist;
}

export type ConfigErrorCode =
  | "INVALID_INPUT"
  | "INVALID_VALUE"
  | "INVALID_KEY"
  | "FORBIDDEN_KEY"
  | "UNKNOWN_KEY"
  | "INVALID_LIMIT"
  | "DEPTH_LIMIT"
  | "NODE_LIMIT"
  | "SIZE_LIMIT"
  | "CYCLE";

export class ConfigMergeError extends Error {
  readonly code: ConfigErrorCode;
  readonly path: string;

  constructor(code: ConfigErrorCode, path: string, message: string) {
    super(`${message} (at ${path})`);
    this.name = "ConfigMergeError";
    this.code = code;
    this.path = path;
  }
}

export const DEFAULT_MERGE_LIMITS: Readonly<MergeLimits> = Object.freeze({
  maxDepth: 8,
  maxNodes: 2_048,
  maxBytes: 32 * 1_024,
});

interface NormalizedOptions {
  readonly limits: MergeLimits;
  readonly allowlist?: CompiledAllowlist;
}

interface CompiledAllowlist {
  readonly allows: (key: string, path: readonly string[]) => boolean;
}

interface TraversalContext {
  readonly limits: MergeLimits;
  readonly stats: {
    nodes: number;
    bytes: number;
  };
  readonly ancestors: Set<object>;
}

interface PathPolicy {
  allows(key: string, path: readonly string[]): boolean;
}

/**
 * Merge a tenant patch into a default configuration snapshot.
 *
 * The returned value is a recursively frozen, null-prototype object. Neither
 * input is modified and arrays in a patch atomically replace arrays in the
 * defaults.
 */
export function mergeTenantConfig(
  defaults: unknown,
  patch: unknown,
  options: MergeOptions = {},
): ConfigObject {
  const normalized = normalizeOptions(options);
  const safeDefaults = cloneConfigObject(defaults, "$defaults", normalized);
  const policy = normalized.allowlist ?? createSchemaAllowlist(safeDefaults);
  const safePatch = cloneConfigObject(patch, "$patch", normalized, policy);
  const merged = mergeObjects(safeDefaults, safePatch, "$", 0, normalized.limits);

  assertFinalSize(merged, normalized.limits);
  return deepFreeze(merged);
}

export const mergeConfig = mergeTenantConfig;

/**
 * Validate and freeze one configuration without merging a tenant patch.
 */
export function createConfigSnapshot(
  config: unknown,
  options: MergeOptions = {},
): ConfigObject {
  const normalized = normalizeOptions(options);
  const value = cloneConfigObject(config, "$", normalized);
  assertFinalSize(value, normalized.limits);
  return deepFreeze(value);
}

export function assertSafeKey(key: string, path: readonly string[] = []): void {
  const renderedPath = renderPath(path, key);

  if (FORBIDDEN_KEYS.has(key)) {
    throw new ConfigMergeError(
      "FORBIDDEN_KEY",
      renderedPath,
      `Forbidden configuration key "${key}"`,
    );
  }

  if (key.includes(".")) {
    throw new ConfigMergeError(
      "FORBIDDEN_KEY",
      renderedPath,
      "Dot-separated configuration paths are not allowed",
    );
  }

  if (key.length > MAX_KEY_LENGTH || !SAFE_KEY_PATTERN.test(key)) {
    throw new ConfigMergeError(
      "INVALID_KEY",
      renderedPath,
      `Configuration key "${key}" is outside the key allowlist`,
    );
  }
}

function normalizeOptions(options: MergeOptions): NormalizedOptions {
  const configuredLimits = options.limits ?? {};
  const limits: MergeLimits = {
    maxDepth: readLimit(
      options.maxDepth,
      configuredLimits.maxDepth,
      DEFAULT_MERGE_LIMITS.maxDepth,
      "maxDepth",
    ),
    maxNodes: readLimit(
      options.maxNodes,
      configuredLimits.maxNodes,
      DEFAULT_MERGE_LIMITS.maxNodes,
      "maxNodes",
    ),
    maxBytes: readLimit(
      options.maxBytes,
      configuredLimits.maxBytes,
      DEFAULT_MERGE_LIMITS.maxBytes,
      "maxBytes",
    ),
  };

  const allowlist = options.allowedKeys ?? options.keyAllowlist;
  return {
    limits,
    allowlist: allowlist === undefined ? undefined : compileAllowlist(allowlist),
  };
}

function readLimit(
  topLevel: number | undefined,
  nested: number | undefined,
  fallback: number,
  name: keyof MergeLimits,
): number {
  const value = topLevel ?? nested ?? fallback;
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new ConfigMergeError(
      "INVALID_LIMIT",
      "$",
      `${name} must be a positive safe integer`,
    );
  }
  return value;
}

function compileAllowlist(allowlist: KeyAllowlist): CompiledAllowlist {
  if (typeof allowlist === "function") {
    return {
      allows: (key, path) => allowlist(key, path),
    };
  }

  if (
    typeof allowlist === "string" ||
    !allowlist ||
    typeof (allowlist as Iterable<string>)[Symbol.iterator] !== "function"
  ) {
    throw new ConfigMergeError(
      "INVALID_INPUT",
      "$",
      "allowedKeys must be an array, a Set, or a predicate",
    );
  }

  const globalKeys = new Set<string>();
  const paths = new Set<string>();

  for (const entry of allowlist) {
    if (typeof entry !== "string" || entry.length === 0) {
      throw new ConfigMergeError(
        "INVALID_INPUT",
        "$",
        "Every allowlist entry must be a non-empty string",
      );
    }

    const segments = entry.split(".");
    if (segments.some((segment) => segment.length === 0)) {
      throw new ConfigMergeError(
        "INVALID_INPUT",
        "$",
        `Invalid allowlist path "${entry}"`,
      );
    }

    if (segments.length === 1) {
      globalKeys.add(entry);
      continue;
    }

    for (let index = 1; index <= segments.length; index += 1) {
      paths.add(segments.slice(0, index).join("."));
    }
  }

  return {
    allows: (key, path) =>
      globalKeys.has(key) || paths.has([...path, key].join(".")),
  };
}

function createSchemaAllowlist(defaults: ConfigObject): PathPolicy {
  const paths = new Set<string>();
  const visited = new Set<object>();

  const walk = (value: ConfigValue, path: readonly string[]): void => {
    if (Array.isArray(value)) {
      return;
    }
    if (!isConfigObject(value) || visited.has(value)) {
      return;
    }

    visited.add(value);
    for (const key of Object.keys(value)) {
      const childPath = [...path, key];
      paths.add(childPath.join("."));
      walk(value[key], childPath);
    }
  };

  walk(defaults, []);

  return {
    allows: (key, path) => paths.has([...path, key].join(".")),
  };
}

function cloneConfigObject(
  value: unknown,
  path: string,
  options: NormalizedOptions,
  policy?: PathPolicy,
): ConfigObject {
  if (!isRecordLike(value)) {
    throw new ConfigMergeError(
      "INVALID_INPUT",
      path,
      "Configuration root must be an object",
    );
  }

  const context: TraversalContext = {
    limits: options.limits,
    stats: { nodes: 0, bytes: 0 },
    ancestors: new Set(),
  };
  const cloned = cloneValue(value, [], 0, context, policy);
  assertFinalSize(cloned, options.limits);
  return cloned as ConfigObject;
}

function cloneValue(
  value: unknown,
  path: readonly string[],
  depth: number,
  context: TraversalContext,
  policy?: PathPolicy,
): ConfigValue {
  assertDepth(depth, path, context.limits);

  if (
    value === null ||
    typeof value === "string" ||
    typeof value === "boolean"
  ) {
    if (
      typeof value === "string" &&
      value.length > context.limits.maxBytes
    ) {
      throw new ConfigMergeError(
        "SIZE_LIMIT",
        renderPath(path),
        `Configuration exceeds the ${context.limits.maxBytes}-byte size limit`,
      );
    }
    consumePrimitive(context, value, path);
    return value;
  }

  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new ConfigMergeError(
        "INVALID_VALUE",
        renderPath(path),
        "Configuration numbers must be finite",
      );
    }
    consumePrimitive(context, value, path);
    return value;
  }

  if (typeof value !== "object") {
    throw new ConfigMergeError(
      "INVALID_VALUE",
      renderPath(path),
      "Configuration values must be JSON-compatible",
    );
  }

  if (context.ancestors.has(value)) {
    throw new ConfigMergeError(
      "CYCLE",
      renderPath(path),
      "Configuration must not contain circular references",
    );
  }

  context.ancestors.add(value);
  try {
    if (Array.isArray(value)) {
      return cloneArray(value, path, depth, context, policy);
    }

    return cloneObject(value as ConfigObject, path, depth, context, policy);
  } finally {
    context.ancestors.delete(value);
  }
}

function cloneObject(
  value: ConfigObject,
  path: readonly string[],
  depth: number,
  context: TraversalContext,
  policy?: PathPolicy,
): ConfigObject {
  consumeNode(context, path);

  if (Object.getOwnPropertySymbols(value).length > 0) {
    throw new ConfigMergeError(
      "INVALID_KEY",
      renderPath(path),
      "Symbol keys are not allowed in configuration",
    );
  }

  if (Reflect.ownKeys(value).length > context.limits.maxNodes) {
    throw new ConfigMergeError(
      "NODE_LIMIT",
      renderPath(path),
      `Configuration exceeds the ${context.limits.maxNodes}-node limit`,
    );
  }

  const descriptors = Object.getOwnPropertyDescriptors(value);
  const clone: Record<string, ConfigValue> = Object.create(null);

  for (const key of Object.keys(descriptors)) {
    const descriptor = descriptors[key];
    const childPath = [...path, key];

    assertSafeKey(key, path);
    if (policy && !policy.allows(key, path)) {
      throw new ConfigMergeError(
        "UNKNOWN_KEY",
        renderPath(childPath),
        `Configuration key "${key}" is not in the allowlist`,
      );
    }
    if (!("value" in descriptor) || descriptor.get || descriptor.set) {
      throw new ConfigMergeError(
        "INVALID_VALUE",
        renderPath(childPath),
        "Accessor properties are not allowed in configuration",
      );
    }

    consumeKey(context, key);
    clone[key] = cloneValue(
      descriptor.value,
      childPath,
      depth + 1,
      context,
      policy,
    );
  }

  return clone;
}

function cloneArray(
  value: readonly unknown[],
  path: readonly string[],
  depth: number,
  context: TraversalContext,
  policy?: PathPolicy,
): readonly ConfigValue[] {
  consumeNode(context, path);

  if (Object.getOwnPropertySymbols(value).length > 0) {
    throw new ConfigMergeError(
      "INVALID_KEY",
      renderPath(path),
      "Symbol keys are not allowed in configuration arrays",
    );
  }

  if (value.length > context.limits.maxNodes) {
    throw new ConfigMergeError(
      "NODE_LIMIT",
      renderPath(path),
      `Configuration exceeds the ${context.limits.maxNodes}-node limit`,
    );
  }

  const descriptors = Object.getOwnPropertyDescriptors(value);
  const allowedIndexes = new Set<string>();
  for (let index = 0; index < value.length; index += 1) {
    allowedIndexes.add(String(index));
  }

  for (const key of Object.keys(descriptors)) {
    if (key === "length") {
      continue;
    }
    if (!allowedIndexes.has(key)) {
      throw new ConfigMergeError(
        "INVALID_KEY",
        renderPath([...path, key]),
        "Arrays may only contain indexed values",
      );
    }
  }

  const clone: ConfigValue[] = [];
  for (let index = 0; index < value.length; index += 1) {
    const descriptor = descriptors[String(index)];
    if (!descriptor) {
      throw new ConfigMergeError(
        "INVALID_VALUE",
        renderPath([...path, String(index)]),
        "Sparse arrays are not allowed",
      );
    }
    if (!("value" in descriptor) || descriptor.get || descriptor.set) {
      throw new ConfigMergeError(
        "INVALID_VALUE",
        renderPath([...path, String(index)]),
        "Accessor properties are not allowed in configuration",
      );
    }

    consumeKey(context, String(index));
    // Arrays are atomic replacements, so their element shapes are not merged
    // against the corresponding default array element. They are still cloned
    // through the universal key-name checks.
    clone.push(
      cloneValue(
        descriptor.value,
        [...path, String(index)],
        depth + 1,
        context,
      ),
    );
  }

  return clone;
}

function mergeObjects(
  defaults: ConfigObject,
  patch: ConfigObject,
  path: readonly string[],
  depth: number,
  limits: MergeLimits,
): ConfigObject {
  assertDepth(depth, path, limits);
  const result: Record<string, ConfigValue> = Object.create(null);

  for (const key of Object.keys(defaults)) {
    result[key] = defaults[key];
  }

  for (const key of Object.keys(patch)) {
    const defaultValue = defaults[key];
    const patchValue = patch[key];
    const childPath = [...path, key];

    if (isConfigObject(defaultValue) && isConfigObject(patchValue)) {
      result[key] = mergeObjects(
        defaultValue,
        patchValue,
        childPath,
        depth + 1,
        limits,
      );
    } else {
      result[key] = patchValue;
    }
  }

  return result;
}

function assertFinalSize(value: ConfigValue, limits: MergeLimits): void {
  let serialized: string;
  try {
    serialized = JSON.stringify(value);
  } catch {
    throw new ConfigMergeError(
      "INVALID_VALUE",
      "$",
      "Configuration could not be serialized",
    );
  }

  const bytes = UTF8_ENCODER.encode(serialized).byteLength;
  if (bytes > limits.maxBytes) {
    throw new ConfigMergeError(
      "SIZE_LIMIT",
      "$",
      `Configuration exceeds the ${limits.maxBytes}-byte size limit`,
    );
  }
}

function consumePrimitive(
  context: TraversalContext,
  value: ConfigPrimitive,
  path: readonly string[],
): void {
  consumeNode(context, path);
  const serialized = JSON.stringify(value);
  context.stats.bytes += UTF8_ENCODER.encode(serialized).byteLength;
  assertTraversalLimits(context, path);
}

function consumeNode(context: TraversalContext, path: readonly string[]): void {
  context.stats.nodes += 1;
  assertTraversalLimits(context, path);
}

function consumeKey(context: TraversalContext, key: string): void {
  context.stats.bytes += UTF8_ENCODER.encode(key).byteLength + 3;
  assertTraversalLimits(context, []);
}

function assertTraversalLimits(
  context: TraversalContext,
  path: readonly string[],
): void {
  if (context.stats.nodes > context.limits.maxNodes) {
    throw new ConfigMergeError(
      "NODE_LIMIT",
      renderPath(path),
      `Configuration exceeds the ${context.limits.maxNodes}-node limit`,
    );
  }
  if (context.stats.bytes > context.limits.maxBytes) {
    throw new ConfigMergeError(
      "SIZE_LIMIT",
      renderPath(path),
      `Configuration exceeds the ${context.limits.maxBytes}-byte size limit`,
    );
  }
}

function assertDepth(
  depth: number,
  path: readonly string[],
  limits: MergeLimits,
): void {
  if (depth > limits.maxDepth) {
    throw new ConfigMergeError(
      "DEPTH_LIMIT",
      renderPath(path),
      `Configuration exceeds the maximum depth of ${limits.maxDepth}`,
    );
  }
}

function isConfigObject(value: unknown): value is ConfigObject {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }

  try {
    const prototype = Object.getPrototypeOf(value);
    return prototype === Object.prototype || prototype === null;
  } catch {
    return false;
  }
}

function isRecordLike(value: unknown): value is object {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function deepFreeze<T extends ConfigValue>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const key of Object.keys(value)) {
      deepFreeze(value[key]);
    }
    Object.freeze(value);
  }
  return value;
}

function renderPath(path: readonly string[], key?: string): string {
  let rendered = "$";
  for (const segment of path) {
    rendered += /^\d+$/.test(segment) ? `[${segment}]` : `.${segment}`;
  }
  if (key !== undefined) {
    rendered += /^\d+$/.test(key) ? `[${key}]` : `.${key}`;
  }
  return rendered;
}
