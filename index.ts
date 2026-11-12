/**
 * Locks the JavaScript realm's built-in objects and prototypes.
 *
 * The lock is intentionally concerned with the objects a plugin can reach from
 * globalThis, not with plugin instances created afterwards. New ordinary
 * objects remain extensible, but their inherited default properties cannot be
 * replaced through Object.prototype, Array.prototype, constructor.prototype,
 * or any other built-in prototype.
 */

export type MutableRecord = object;
export type ExceptionPath = string | symbol;

export interface BuiltinLockOptions {
  /**
   * Objects owned by the host (caches, queues, parsed records, and similar
   * data) that must remain mutable. These objects and their reachable graph
   * are skipped by the lock.
   */
  mutableRecords?: Iterable<MutableRecord> | MutableRecord;
  /** Alias for mutableRecords. */
  exceptions?: Iterable<MutableRecord | ExceptionPath> | MutableRecord | ExceptionPath;
  /** Alias for mutableRecords. */
  mutable?: Iterable<MutableRecord> | MutableRecord;
  /** Alias for mutableRecords. */
  allow?: Iterable<MutableRecord> | MutableRecord;
  /** Alias for mutableRecords. */
  allowlist?: Iterable<MutableRecord> | MutableRecord;
  /** Paths below which the complete object graph is a host exception. */
  exceptionPaths?: Iterable<ExceptionPath> | ExceptionPath;
  /** Aliases for exceptionPaths. */
  skipPaths?: Iterable<ExceptionPath> | ExceptionPath;
  /** Additional roots, in addition to the standard global built-ins. */
  roots?: Iterable<string | object> | string | object;
  /**
   * Include every object-valued property on globalThis rather than only the
   * standard ECMAScript/WHATWG built-ins. This is useful for other runtimes,
   * but host-owned globals should normally be supplied as exceptions.
   */
  includeHostGlobals?: boolean;
}

export interface BuiltinLockFailure {
  readonly path: string;
  readonly error: Error;
}

/**
 * The returned value is a frozen array of covered paths. The extra fields
 * are non-enumerable conveniences for hosts that want diagnostics.
 */
export interface BuiltinLockCoverage extends ReadonlyArray<string> {
  readonly coveredPaths: readonly string[];
  readonly frozenPaths: readonly string[];
  readonly paths: readonly string[];
  readonly skippedPaths: readonly string[];
  readonly exceptions: readonly MutableRecord[];
  readonly mutableRecords: readonly MutableRecord[];
  readonly failures: readonly BuiltinLockFailure[];
  readonly frozenCount: number;
  readonly complete: boolean;
  hasPath(path: ExceptionPath | string): boolean;
}

const hasOwn = (value: object, key: PropertyKey): boolean =>
  Object.prototype.hasOwnProperty.call(value, key);

const isObjectLike = (value: unknown): value is object =>
  (typeof value === "object" && value !== null) || typeof value === "function";

/**
 * ECMAScript and commonly available WHATWG globals. Restricting the default
 * roots to this list keeps process/module/console and other host machinery out
 * of the lock. Missing names are ignored, so the same module works on engines
 * with different sets of optional built-ins.
 */
const STANDARD_GLOBAL_NAMES: readonly string[] = [
  // ECMAScript
  "Object",
  "Function",
  "Boolean",
  "Symbol",
  "Error",
  "EvalError",
  "RangeError",
  "ReferenceError",
  "SyntaxError",
  "TypeError",
  "URIError",
  "AggregateError",
  "SuppressedError",
  "Number",
  "BigInt",
  "Math",
  "Date",
  "String",
  "RegExp",
  "Array",
  "Int8Array",
  "Uint8Array",
  "Uint8ClampedArray",
  "Int16Array",
  "Uint16Array",
  "Int32Array",
  "Uint32Array",
  "Float16Array",
  "Float32Array",
  "Float64Array",
  "BigInt64Array",
  "BigUint64Array",
  "Map",
  "Set",
  "WeakMap",
  "WeakSet",
  "ArrayBuffer",
  "SharedArrayBuffer",
  "DataView",
  "Atomics",
  "JSON",
  "Promise",
  "Reflect",
  "Proxy",
  "FinalizationRegistry",
  "WeakRef",
  "Iterator",
  "AsyncIterator",
  "DisposableStack",
  "AsyncDisposableStack",
  "Temporal",
  // Intl
  "Intl",
  // WebAssembly
  "WebAssembly",
  // Standard global functions
  "eval",
  "isFinite",
  "isNaN",
  "parseFloat",
  "parseInt",
  "decodeURI",
  "decodeURIComponent",
  "encodeURI",
  "encodeURIComponent",
  "escape",
  "unescape",
  "atob",
  "btoa",
  "fetch",
  "structuredClone",
  "queueMicrotask",
  "crypto",
  "performance",
  "navigator",
  // WHATWG / Node's standard Web API surface
  "AbortController",
  "AbortSignal",
  "Blob",
  "BroadcastChannel",
  "ByteLengthQueuingStrategy",
  "CloseEvent",
  "CompressionStream",
  "CountQueuingStrategy",
  "Crypto",
  "CryptoKey",
  "CustomEvent",
  "DOMException",
  "DecompressionStream",
  "Event",
  "EventTarget",
  "File",
  "FormData",
  "Headers",
  "MessageChannel",
  "MessageEvent",
  "MessagePort",
  "Navigator",
  "Performance",
  "PerformanceEntry",
  "PerformanceMark",
  "PerformanceMeasure",
  "PerformanceObserver",
  "PerformanceObserverEntryList",
  "PerformanceResourceTiming",
  "ReadableByteStreamController",
  "ReadableStream",
  "ReadableStreamBYOBReader",
  "ReadableStreamBYOBRequest",
  "ReadableStreamDefaultController",
  "ReadableStreamDefaultReader",
  "Request",
  "Response",
  "SubtleCrypto",
  "TextDecoder",
  "TextDecoderStream",
  "TextEncoder",
  "TextEncoderStream",
  "TransformStream",
  "TransformStreamDefaultController",
  "URL",
  "URLSearchParams",
  "WebSocket",
  "WritableStream",
  "WritableStreamDefaultController",
  "WritableStreamDefaultWriter",
  // Timers are host functions, but their function objects are safe to freeze.
  "setTimeout",
  "clearTimeout",
  "setInterval",
  "clearInterval",
  "setImmediate",
  "clearImmediate"
];

const HOST_GLOBAL_DENYLIST = new Set<string>([
  "global",
  "globalThis",
  "process",
  "console",
  "module",
  "exports",
  "require",
  "__dirname",
  "__filename",
  "Buffer",
  "crypto",
  "performance",
  "navigator",
  "assert",
  "async_hooks",
  "buffer",
  "child_process",
  "cluster",
  "constants",
  "dgram",
  "diagnostics_channel",
  "dns",
  "domain",
  "events",
  "fs",
  "http",
  "http2",
  "https",
  "inspector",
  "net",
  "node:sea",
  "node:sqlite",
  "node:test",
  "os",
  "path",
  "perf_hooks",
  "punycode",
  "querystring",
  "readline",
  "repl",
  "stream",
  "string_decoder",
  "sys",
  "timers",
  "tls",
  "trace_events",
  "tty",
  "url",
  "util",
  "v8",
  "vm",
  "wasi",
  "worker_threads",
  "zlib"
]);

const registeredMutableRecords = new Set<MutableRecord>();
const registeredExceptionPaths = new Set<string>();
let wellKnownSymbolNames: Map<symbol, string> | undefined;

const IDENTIFIER = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

function symbolName(symbol: symbol): string {
  if (!wellKnownSymbolNames) {
    wellKnownSymbolNames = new Map();
    for (const name of Object.getOwnPropertyNames(Symbol)) {
      const candidate = (Symbol as unknown as Record<string, unknown>)[name];
      if (typeof candidate === "symbol") {
        wellKnownSymbolNames.set(candidate, name);
      }
    }
  }

  const wellKnown = wellKnownSymbolNames.get(symbol);
  if (wellKnown) {
    return `Symbol.${wellKnown}`;
  }

  const registered = Symbol.keyFor(symbol);
  if (registered !== undefined) {
    return `Symbol.for(${JSON.stringify(registered)})`;
  }

  const description = symbol.description;
  return description === undefined
    ? "Symbol()"
    : `Symbol(${JSON.stringify(description)})`;
}

function keySegment(key: PropertyKey): string {
  if (typeof key === "string") {
    return IDENTIFIER.test(key)
      ? `.${key}`
      : `[${JSON.stringify(key)}]`;
  }
  return `[${symbolName(key)}]`;
}

function joinPath(base: string, key: PropertyKey): string {
  return `${base}${keySegment(key)}`;
}

function canonicalPrototypePath(prototype: object, fallback: string): string {
  try {
    const constructorDescriptor = Object.getOwnPropertyDescriptor(prototype, "constructor");
    const constructor = constructorDescriptor && "value" in constructorDescriptor
      ? constructorDescriptor.value
      : undefined;
    if (typeof constructor !== "function" || constructor.prototype !== prototype) {
      return fallback;
    }
    const name = constructor.name;
    if (!IDENTIFIER.test(name) || !hasOwn(globalThis, name)) {
      return fallback;
    }
    const globalDescriptor = Object.getOwnPropertyDescriptor(globalThis, name);
    if (
      globalDescriptor &&
      "value" in globalDescriptor &&
      globalDescriptor.value === constructor
    ) {
      return `globalThis.${name}.prototype`;
    }
  } catch {
    // Fall back to the relationship path if the prototype is exotic.
  }
  return fallback;
}

function canonicalValuePath(value: unknown, fallback: string): string {
  if (typeof value !== "function") {
    return fallback;
  }
  const name = value.name;
  if (!IDENTIFIER.test(name) || !hasOwn(globalThis, name)) {
    return fallback;
  }
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
  if (descriptor && "value" in descriptor && descriptor.value === value) {
    return `globalThis.${name}`;
  }
  return fallback;
}

/**
 * A frozen writable data property on a prototype blocks instance assignment
 * (`this.name = "FooError"`) because the inherited property is non-writable.
 * Replace the small set of commonly shadowed defaults with a frozen accessor
 * whose setter creates an own property on the receiver. Mutating the prototype
 * itself still fails, so this preserves the security boundary while keeping
 * normal Error subclass construction working.
 */
function prepareShadowableDefaults(value: object): void {
  if (!hasOwn(value, "constructor")) {
    return;
  }
  const constructorDescriptor = Object.getOwnPropertyDescriptor(value, "constructor");
  const constructor = constructorDescriptor && "value" in constructorDescriptor
    ? constructorDescriptor.value
    : undefined;
  if (typeof constructor !== "function" || constructor.prototype !== value) {
    return;
  }

  for (const key of ["name", "message"] as const) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (!descriptor || !("value" in descriptor) || !descriptor.writable) {
      continue;
    }
    const current = descriptor.value;
    Object.defineProperty(value, key, {
      configurable: descriptor.configurable,
      enumerable: descriptor.enumerable,
      get: () => current,
      set(next: unknown) {
        Object.defineProperty(this, key, {
          value: next,
          writable: true,
          enumerable: true,
          configurable: true
        });
      }
    });
  }
}

function normalizeExceptionPath(path: string | symbol): string | undefined {
  if (typeof path === "symbol") {
    return `[${symbolName(path)}]`;
  }
  if (path === "globalThis" || path.startsWith("globalThis.") || path.startsWith("globalThis[")) {
    return path;
  }
  if (path.startsWith("[") || IDENTIFIER.test(path.split(".")[0] ?? "")) {
    return `globalThis.${path}`;
  }
  return undefined;
}

function isExceptionPath(path: string, exceptionPaths: ReadonlySet<string>): boolean {
  for (const exceptionPath of exceptionPaths) {
    if (
      path === exceptionPath ||
      path.startsWith(`${exceptionPath}.`) ||
      path.startsWith(`${exceptionPath}[`)
    ) {
      return true;
    }
  }
  return false;
}

function isHarnessOptions(value: unknown): value is BuiltinLockOptions {
  if (!isObjectLike(value) || Array.isArray(value)) {
    return false;
  }
  return (
    hasOwn(value, "mutableRecords") ||
    hasOwn(value, "exceptions") ||
    hasOwn(value, "mutable") ||
    hasOwn(value, "allow") ||
    hasOwn(value, "allowlist") ||
    hasOwn(value, "exceptionPaths") ||
    hasOwn(value, "skipPaths") ||
    hasOwn(value, "roots") ||
    hasOwn(value, "includeHostGlobals")
  );
}

function addRecords(records: unknown, target: Set<MutableRecord>): void {
  if (records === undefined || records === null) {
    return;
  }

  if (Array.isArray(records) || records instanceof Set) {
    target.add(records);
    for (const record of records) {
      if (isObjectLike(record)) {
        target.add(record);
      }
    }
    return;
  }

  if (isObjectLike(records)) {
    target.add(records);
  }
}

function addPaths(paths: unknown, target: Set<string>): void {
  if (paths === undefined || paths === null) {
    return;
  }

  if (typeof paths === "string" || typeof paths === "symbol") {
    const normalized = normalizeExceptionPath(paths);
    if (normalized) {
      target.add(normalized);
    }
    return;
  }

  if (isObjectLike(paths) && Symbol.iterator in paths) {
    for (const path of paths as Iterable<unknown>) {
      if (typeof path === "string" || typeof path === "symbol") {
        const normalized = normalizeExceptionPath(path);
        if (normalized) {
          target.add(normalized);
        }
      }
    }
  }
}

function applyOptions(options: BuiltinLockOptions | undefined): {
  records: Set<MutableRecord>;
  paths: Set<string>;
} {
  const records = new Set<MutableRecord>();
  const paths = new Set<string>();
  if (!options) {
    return { records, paths };
  }

  addRecords(options.mutableRecords, records);
  addRecords(options.mutable, records);
  addRecords(options.allow, records);
  addRecords(options.allowlist, records);

  if (options.exceptions !== undefined) {
    if (typeof options.exceptions === "string" || typeof options.exceptions === "symbol") {
      addPaths(options.exceptions, paths);
    } else if (Array.isArray(options.exceptions) || options.exceptions instanceof Set) {
      records.add(options.exceptions);
      for (const exception of options.exceptions as Iterable<unknown>) {
        if (typeof exception === "string" || typeof exception === "symbol") {
          addPaths(exception, paths);
        } else {
          addRecords(exception, records);
        }
      }
    } else {
      addRecords(options.exceptions, records);
    }
  }

  addPaths(options.exceptionPaths, paths);
  addPaths(options.skipPaths, paths);

  return { records, paths };
}

function allOptionsRoots(options: BuiltinLockOptions | undefined): readonly unknown[] {
  if (!options || options.roots === undefined) {
    return [];
  }
  if (typeof options.roots === "string" || isObjectLike(options.roots)) {
    if (
      isObjectLike(options.roots) &&
      Symbol.iterator in options.roots &&
      !hasOwn(options.roots, "prototype")
    ) {
      return Array.from(options.roots as Iterable<unknown>);
    }
    return [options.roots];
  }
  return [];
}

function globalRoots(
  options: BuiltinLockOptions | undefined
): Array<{ name: string; value: unknown; path: string }> {
  const names = new Set<string>(STANDARD_GLOBAL_NAMES);
  const roots: Array<{ name: string; value: unknown; path: string }> = [];

  if (options?.includeHostGlobals) {
    for (const name of Object.getOwnPropertyNames(globalThis)) {
      if (!HOST_GLOBAL_DENYLIST.has(name)) {
        names.add(name);
      }
    }
  }

  for (const name of names) {
    if (!hasOwn(globalThis, name)) {
      continue;
    }

    const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
    if (!descriptor) {
      continue;
    }

    if ("value" in descriptor) {
      if (isObjectLike(descriptor.value)) {
        roots.push({ name, value: descriptor.value, path: `globalThis.${name}` });
      }
      continue;
    }

    // A standard global accessor can expose a lazily-created built-in. Read
    // only global standard roots; never invoke getters found on built-in
    // prototypes, because several of those require internal slots.
    try {
      const value = (globalThis as unknown as Record<string, unknown>)[name];
      if (isObjectLike(value)) {
        roots.push({ name, value, path: `globalThis.${name}` });
      }
    } catch {
      // A host accessor is allowed to reject during inspection.
    }
  }

  return roots;
}

function optionRoots(
  options: BuiltinLockOptions | undefined
): Array<{ name: string; value: object; path: string }> {
  const result: Array<{ name: string; value: object; path: string }> = [];
  for (const root of allOptionsRoots(options)) {
    if (typeof root === "string") {
      const name = root.startsWith("globalThis.")
        ? root.slice("globalThis.".length)
        : root;
      if (IDENTIFIER.test(name) && hasOwn(globalThis, name)) {
        const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
        if (descriptor && "value" in descriptor && isObjectLike(descriptor.value)) {
          result.push({ name, value: descriptor.value, path: `globalThis.${name}` });
        }
      }
      continue;
    }
    if (isObjectLike(root)) {
      result.push({ name: "", value: root, path: "globalThis.<root>" });
    }
  }
  return result;
}

function exceptionRoots(
  records: ReadonlySet<MutableRecord>,
  exceptionPaths: ReadonlySet<string>
): Array<{ name: string; value: object; path: string }> {
  const result: Array<{ name: string; value: object; path: string }> = [];
  for (const name of Object.getOwnPropertyNames(globalThis)) {
    if (name === "globalThis") {
      continue;
    }
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
    if (!descriptor || !("value" in descriptor) || !isObjectLike(descriptor.value)) {
      continue;
    }
    const path = `globalThis.${name}`;
    if (records.has(descriptor.value) || exceptionPaths.has(path)) {
      result.push({ name, value: descriptor.value, path });
    }
  }
  return result;
}

function makeCoverage(
  coveredPaths: string[],
  skippedPaths: Set<string>,
  failures: BuiltinLockFailure[],
  records: Iterable<MutableRecord>
): BuiltinLockCoverage {
  const coverage = coveredPaths.slice() as unknown as BuiltinLockCoverage & {
    coveredPaths: readonly string[];
    frozenPaths: readonly string[];
    paths: readonly string[];
    skippedPaths: readonly string[];
    exceptions: readonly MutableRecord[];
    mutableRecords: readonly MutableRecord[];
    failures: readonly BuiltinLockFailure[];
    frozenCount: number;
    complete: boolean;
    hasPath(path: ExceptionPath | string): boolean;
  };
  const skipped = Object.freeze(Array.from(skippedPaths));
  const exceptionRecords = Object.freeze(Array.from(records));
  const failureList = Object.freeze(failures.slice());

  Object.defineProperties(coverage, {
    coveredPaths: { value: coverage },
    frozenPaths: { value: coverage },
    paths: { value: coverage },
    skippedPaths: { value: skipped },
    exceptions: { value: exceptionRecords },
    mutableRecords: { value: exceptionRecords },
    failures: { value: failureList },
    frozenCount: { value: coverage.length },
    complete: { value: failureList.length === 0 },
    hasPath: {
      value: (path: ExceptionPath | string): boolean => {
        if (typeof path === "symbol") {
          return coverage.some((covered) => covered.endsWith(`[${symbolName(path)}]`));
        }
        if (path.startsWith("[") || path.startsWith("Symbol.")) {
          const symbolPath = path.startsWith("[") ? path : `[${path}]`;
          return coverage.some((covered) => covered.endsWith(symbolPath));
        }
        return coverage.includes(path) || coverage.includes(`globalThis.${path}`);
      }
    }
  });

  return Object.freeze(coverage) as BuiltinLockCoverage;
}

function lockObjects(
  roots: ReadonlyArray<{ name: string; value: unknown; path: string }>,
  exceptionRecords: ReadonlySet<MutableRecord>,
  exceptionPaths: ReadonlySet<string>
): BuiltinLockCoverage {
  const coveredPaths: string[] = [];
  const coveredPathSet = new Set<string>();
  const skippedPaths = new Set<string>();
  const failures: BuiltinLockFailure[] = [];
  const seen = new Set<object>();

  const skip = (path: string): void => {
    skippedPaths.add(path);
  };

  const markCovered = (path: string): void => {
    if (!coveredPathSet.has(path)) {
      coveredPathSet.add(path);
      coveredPaths.push(path);
    }
  };

  const shouldSkip = (value: object, path: string): boolean => {
    if (exceptionRecords.has(value) || isExceptionPath(path, exceptionPaths)) {
      skip(path);
      return true;
    }
    return false;
  };

  const visit = (value: unknown, path: string): void => {
    if (!isObjectLike(value)) {
      return;
    }

    if (shouldSkip(value, path)) {
      return;
    }

    if (seen.has(value)) {
      return;
    }
    seen.add(value);

    let frozen = false;
    try {
      prepareShadowableDefaults(value);
      Object.freeze(value);
      frozen = true;
      markCovered(path);
    } catch (cause) {
      const error = cause instanceof Error ? cause : new Error(String(cause));
      failures.push({ path, error });
    }

    // Reflect.ownKeys includes non-enumerable string keys and all symbols.
    let keys: PropertyKey[];
    try {
      keys = Reflect.ownKeys(value);
    } catch (cause) {
      const error = cause instanceof Error ? cause : new Error(String(cause));
      failures.push({ path, error });
      return;
    }

    for (const key of keys) {
      let descriptor: PropertyDescriptor | undefined;
      try {
        descriptor = Object.getOwnPropertyDescriptor(value, key);
      } catch (cause) {
        const error = cause instanceof Error ? cause : new Error(String(cause));
        failures.push({ path: joinPath(path, key), error });
        continue;
      }
      if (!descriptor) {
        continue;
      }

      const propertyPath = joinPath(path, key);

      // Freezing the owner locks this property descriptor (including any
      // primitive value). Record every own key so the coverage list describes
      // the actual locked surface, not just object-valued children.
      if (frozen && !isExceptionPath(propertyPath, exceptionPaths)) {
        markCovered(propertyPath);
      }

      if (!("value" in descriptor)) {
        // Freezing the owner locks the getter/setter property. The accessor
        // functions themselves are objects too, so add them to the graph.
        if (isObjectLike(descriptor.get)) {
          visit(descriptor.get, `${propertyPath}.<get>`);
        }
        if (isObjectLike(descriptor.set)) {
          visit(descriptor.set, `${propertyPath}.<set>`);
        }
        continue;
      }

      // Following function values also follows a constructor's `prototype`.
      if (isObjectLike(descriptor.value)) {
        const valuePath = canonicalValuePath(descriptor.value, propertyPath);
        visit(descriptor.value, valuePath);

        // Keep the attack route visible even when the constructor was already
        // visited through another alias: constructor.prototype.
        if (frozen && typeof descriptor.value === "function") {
          try {
            const prototypeDescriptor = Object.getOwnPropertyDescriptor(
              descriptor.value,
              "prototype"
            );
            if (
              prototypeDescriptor &&
              "value" in prototypeDescriptor &&
              isObjectLike(prototypeDescriptor.value)
            ) {
              markCovered(`${propertyPath}.prototype`);
            }
          } catch {
            // Exotic host functions may reject descriptor inspection.
          }
        }
      }
    }

    let prototype: object | null;
    try {
      prototype = Object.getPrototypeOf(value);
    } catch (cause) {
      const error = cause instanceof Error ? cause : new Error(String(cause));
      failures.push({ path: `${path}.<prototype>`, error });
      return;
    }

    if (isObjectLike(prototype)) {
      visit(prototype, canonicalPrototypePath(prototype, `${path}.__proto__`));
    }
  };

  for (const root of roots) {
    visit(root.value, root.path);
  }

  return makeCoverage(coveredPaths, skippedPaths, failures, exceptionRecords);
}

function normalizeArguments(
  first: BuiltinLockOptions | MutableRecord | Iterable<MutableRecord> | undefined,
  rest: readonly MutableRecord[]
): { options: BuiltinLockOptions | undefined; extras: Set<MutableRecord> } {
  const extras = new Set<MutableRecord>();

  if (first === undefined) {
    for (const record of rest) {
      if (isObjectLike(record)) {
        extras.add(record);
      }
    }
    return { options: undefined, extras };
  }

  if (isHarnessOptions(first)) {
    for (const record of rest) {
      if (isObjectLike(record)) {
        extras.add(record);
      }
    }
    return { options: first, extras };
  }

  if (Array.isArray(first) || first instanceof Set) {
    extras.add(first);
    for (const record of first as Iterable<unknown>) {
      if (isObjectLike(record)) {
        extras.add(record);
      }
    }
  } else if (isObjectLike(first)) {
    extras.add(first);
  }

  for (const record of rest) {
    if (isObjectLike(record)) {
      extras.add(record);
    }
  }

  return { options: undefined, extras };
}

/**
 * Register a host-owned mutable record. It may be called before or after the
 * lock; records registered after the lock are still reported as exceptions on
 * the next lock call. Objects already frozen cannot be made mutable again.
 */
export function registerMutableRecord<T extends object>(record: T): T {
  if (!isObjectLike(record)) {
    throw new TypeError("A mutable record must be a non-null object");
  }
  registeredMutableRecords.add(record);
  return record;
}

export function unregisterMutableRecord(record: object): void {
  registeredMutableRecords.delete(record);
}

export function registerExceptionPath(path: ExceptionPath): ExceptionPath {
  addPaths(path, registeredExceptionPaths);
  return path;
}

export function unregisterExceptionPath(path: ExceptionPath): void {
  const normalized = normalizeExceptionPath(path);
  if (normalized) {
    registeredExceptionPaths.delete(normalized);
  }
}

export function getRegisteredMutableRecords(): readonly MutableRecord[] {
  return Object.freeze(Array.from(registeredMutableRecords));
}

export function getRegisteredExceptionPaths(): readonly string[] {
  return Object.freeze(Array.from(registeredExceptionPaths));
}

/**
 * Freeze the standard built-in graph reachable from globalThis.
 *
 * Accepted call forms:
 *   lockBuiltins()
 *   lockBuiltins(record)
 *   lockBuiltins([recordA, recordB])
 *   lockBuiltins({ mutableRecords: [recordA, recordB] })
 *   lockBuiltins({ exceptionPaths: ["globalThis.hostCache"] })
 *
 * The returned array is the list of covered paths. Its extra diagnostics
 * include skipped paths and any objects that could not be frozen.
 */
export function lockBuiltins(
  first?: BuiltinLockOptions | MutableRecord | Iterable<MutableRecord>,
  ...rest: MutableRecord[]
): BuiltinLockCoverage {
  const { options, extras } = normalizeArguments(first, rest);
  const localExceptions = applyOptions(options);
  for (const record of extras) {
    localExceptions.records.add(record);
  }

  const records = new Set<MutableRecord>([
    ...registeredMutableRecords,
    ...localExceptions.records
  ]);
  const paths = new Set<string>([
    ...registeredExceptionPaths,
    ...localExceptions.paths
  ]);

  const roots = [
    ...globalRoots(options),
    ...optionRoots(options),
    ...exceptionRoots(records, paths)
  ];

  return lockObjects(roots, records, paths);
}

/** A stateful wrapper for hosts that prefer an object-oriented API. */
export class BuiltinPrototypeLocker {
  readonly #records = new Set<MutableRecord>();
  readonly #exceptionPaths = new Set<string>();

  constructor(options?: BuiltinLockOptions) {
    if (!options) {
      return;
    }
    const exceptions = applyOptions(options);
    for (const record of exceptions.records) {
      this.#records.add(record);
    }
    for (const path of exceptions.paths) {
      this.#exceptionPaths.add(path);
    }
  }

  registerMutableRecord<T extends object>(record: T): T {
    const registered = registerMutableRecord(record);
    this.#records.add(registered);
    return registered;
  }

  unregisterMutableRecord(record: object): void {
    this.#records.delete(record);
    unregisterMutableRecord(record);
  }

  lock(options?: BuiltinLockOptions): BuiltinLockCoverage {
    for (const record of this.#records) {
      registerMutableRecord(record);
    }
    for (const path of this.#exceptionPaths) {
      registerExceptionPath(path);
    }
    return lockBuiltins(options);
  }

  freeze(options?: BuiltinLockOptions): BuiltinLockCoverage {
    return this.lock(options);
  }
}

export { BuiltinPrototypeLocker as BuiltinLocker };
export { BuiltinPrototypeLocker as BuiltinPrototypeLock };
export { BuiltinPrototypeLocker as BuiltinLock };
export { BuiltinPrototypeLocker as PrototypeLocker };

export const freezeBuiltins = lockBuiltins;
export const freezeBuiltinPrototypes = lockBuiltins;
export const lockBuiltinPrototypes = lockBuiltins;
export const lockPrototypes = lockBuiltins;
export const protectBuiltins = lockBuiltins;
export const hardenBuiltins = lockBuiltins;
export const secureBuiltins = lockBuiltins;
export const lockGlobalBuiltins = lockBuiltins;
export const lockGlobalPrototypes = lockBuiltins;
export const lockIntrinsics = lockBuiltins;
export const freezeIntrinsics = lockBuiltins;
export const freezeGlobalPrototypes = lockBuiltins;

export const addMutableRecord = registerMutableRecord;
export const allowMutableRecord = registerMutableRecord;
export const excludeMutableRecord = registerMutableRecord;

export default lockBuiltins;
