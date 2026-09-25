export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type JsonObject = { [key: string]: JsonValue };

export type PathSegment = string | number;

export interface OriginRef {
  source: string;
  version: string;
  path: string;
}

export interface NodeRef {
  version: string;
  path: string;
}

export interface LineageNode {
  version: string;
  path: string;
  operation: string;
  parents: NodeRef[];
  origins: OriginRef[];
}

export interface Relation<T extends JsonValue[] = JsonValue[]> {
  rows: T;
  version: string;
  lineage: Lineage;
}

export interface SourceVersionInfo {
  source: string;
  version: string;
  rowsPath: string;
}

export interface ErrorLocation {
  source?: string | undefined;
  version: string;
  path: string;
}

interface StoredNode extends LineageNode {}

export class LineageError extends Error {
  readonly code: string;
  readonly version?: string;
  readonly path?: string;
  readonly locations: ErrorLocation[];

  constructor(
    message: string,
    options: {
      code: string;
      version?: string;
      path?: string;
      locations?: ErrorLocation[];
      cause?: unknown;
    },
  ) {
    super(message, { cause: options.cause });
    this.name = "LineageError";
    this.code = options.code;
    this.version = options.version;
    this.path = options.path;
    this.locations = options.locations ?? [];
  }
}

export class Lineage {
  private readonly versions: string[] = [];
  private readonly nodes = new Map<string, Map<string, StoredNode>>();
  private readonly sourceInfo = new Map<string, SourceVersionInfo>();

  versionNames(): string[] {
    return [...this.versions];
  }

  hasVersion(version: string): boolean {
    return this.nodes.has(version);
  }

  addSourceVersion(info: SourceVersionInfo, rows: JsonValue[]): void {
    this.assertNewVersion(info.version);
    const byPath = new Map<string, OriginRef[]>();
    const add = (path: string, originPath: string) => {
      byPath.set(path, [{ source: info.source, version: info.version, path: originPath }]);
    };

    add("/", info.rowsPath);
    walkJson(rows, "/", (path) => {
      if (path !== "/") add(path, combinePointers(info.rowsPath, path));
    });

    this.sourceInfo.set(info.version, info);
    this.putNodes(info.version, "source", byPath, rows);
  }

  addVersion(
    version: string,
    operation: string,
    rows: JsonValue[],
    mappings: ReadonlyMap<string, NodeRef[]>,
  ): void {
    this.assertNewVersion(version);
    const byPath = new Map<string, OriginRef[]>();

    for (const [path, parents] of mappings) {
      const seen = new Set<string>();
      const origins: OriginRef[] = [];
      const queue: NodeRef[] = dedupeRefs(parents);
      const visited = new Set<string>();

      while (queue.length > 0) {
        const ref = queue.shift()!;
        const refKey = `${ref.version}\u0000${ref.path}`;
        if (visited.has(refKey)) continue;
        visited.add(refKey);

        const node = this.getNode(ref.version, ref.path);
        for (const origin of node.origins) {
          const key = originKey(origin);
          if (!seen.has(key)) {
            seen.add(key);
            origins.push(origin);
          }
        }
        queue.push(...node.parents);
      }
      byPath.set(path, origins);
    }

    this.putNodes(version, operation, byPath, rows, mappings);
  }

  field(version: string, path: string): LineageNode {
    return this.getNode(version, normalizePointer(path));
  }

  origins(version: string, path: string): OriginRef[] {
    return this.field(version, path).origins.map((origin) => ({ ...origin }));
  }

  parents(version: string, path: string): NodeRef[] {
    return this.field(version, path).parents.map((ref) => ({ ...ref }));
  }

  explain(version: string, path: string): ExplainedNode {
    return this.explainNode(version, normalizePointer(path), []);
  }

  entries(version: string): LineageNode[] {
    const map = this.nodes.get(version);
    if (!map) throw missingVersion(version);
    return [...map.values()].map((node) => ({
      ...node,
      parents: node.parents.map((ref) => ({ ...ref })),
      origins: node.origins.map((origin) => ({ ...origin })),
    }));
  }

  source(version: string): SourceVersionInfo | undefined {
    const info = this.sourceInfo.get(version);
    return info ? { ...info } : undefined;
  }

  private explainNode(version: string, path: string, ancestorKeys: string[]): ExplainedNode {
    const node = this.getNode(version, path);
    const key = `${version}\u0000${path}`;
    if (ancestorKeys.includes(key)) {
      return { version, path, operation: node.operation, parents: [], origins: node.origins };
    }
    const nextAncestors = [...ancestorKeys, key];
    return {
      version,
      path,
      operation: node.operation,
      parents: node.parents.map((parent) =>
        this.explainNode(parent.version, parent.path, nextAncestors),
      ),
      origins: node.origins.map((origin) => ({ ...origin })),
    };
  }

  private assertNewVersion(version: string): void {
    if (typeof version !== "string" || version.length === 0) {
      throw new LineageError("Version must be a non-empty string.", {
        code: "INVALID_VERSION",
      });
    }
    if (this.nodes.has(version)) {
      throw new LineageError(`Version already exists: ${version}`, {
        code: "DUPLICATE_VERSION",
        version,
      });
    }
  }

  private getNode(version: string, path: string): StoredNode {
    const map = this.nodes.get(version);
    if (!map) throw missingVersion(version);
    const node = map.get(path);
    if (!node) {
      throw new LineageError(`Path is not recorded in version ${version}: ${path}`, {
        code: "UNKNOWN_PATH",
        version,
        path,
      });
    }
    return node;
  }

  private putNodes(
    version: string,
    operation: string,
    byPath: ReadonlyMap<string, OriginRef[]>,
    rows: JsonValue[],
    parents: ReadonlyMap<string, NodeRef[]> = new Map(),
  ): void {
    assertJsonCompatible(rows, { source: operation, version, path: "/" });
    const actual = new Set<string>();
    walkJson(rows, "/", (path) => actual.add(path));

    for (const path of actual) {
      if (!byPath.has(path)) {
        throw new LineageError(`Missing lineage mapping for output path ${path}.`, {
          code: "MISSING_MAPPING",
          version,
          path,
        });
      }
    }
    for (const path of byPath.keys()) {
      if (!actual.has(path)) {
        throw new LineageError(`Lineage mapping points to missing output path ${path}.`, {
          code: "STALE_MAPPING",
          version,
          path,
        });
      }
    }

    const map = new Map<string, StoredNode>();
    for (const [path, origins] of byPath) {
      map.set(path, {
        version,
        path,
        operation,
        parents: dedupeRefs(parents.get(path) ?? []),
        origins: dedupeOrigins(origins),
      });
    }
    this.nodes.set(version, map);
    this.versions.push(version);
  }
}

export interface ExplainedNode extends NodeRef {
  operation: string;
  parents: ExplainedNode[];
  origins: OriginRef[];
}

export function originKey(origin: OriginRef): string {
  return `${origin.source}\u0000${origin.version}\u0000${origin.path}`;
}

export function refKey(ref: NodeRef): string {
  return `${ref.version}\u0000${ref.path}`;
}

export function dedupeOrigins(origins: Iterable<OriginRef>): OriginRef[] {
  const seen = new Set<string>();
  const result: OriginRef[] = [];
  for (const origin of origins) {
    const key = originKey(origin);
    if (!seen.has(key)) {
      seen.add(key);
      result.push({ ...origin });
    }
  }
  return result;
}

export function dedupeRefs(refs: Iterable<NodeRef>): NodeRef[] {
  const seen = new Set<string>();
  const result: NodeRef[] = [];
  for (const ref of refs) {
    const key = refKey(ref);
    if (!seen.has(key)) {
      seen.add(key);
      result.push({ ...ref });
    }
  }
  return result;
}

export function walkJson(
  value: JsonValue,
  pointer: string,
  visit: (pointer: string, value: JsonValue) => void,
): void {
  visit(pointer, value);
  if (Array.isArray(value)) {
    value.forEach((item, index) => walkJson(item, joinPointer(pointer, index), visit));
  } else if (isJsonObject(value)) {
    for (const [key, child] of Object.entries(value)) {
      walkJson(child, joinPointer(pointer, key), visit);
    }
  }
}

export function isJsonObject(value: JsonValue | undefined): value is JsonObject {
  if (value === null || typeof value !== "object") return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

export function escapePointer(segment: PathSegment): string {
  return String(segment).replace(/~/g, "~0").replace(/\//g, "~1");
}

export function unescapePointer(segment: string): string {
  return segment.replace(/~1/g, "/").replace(/~0/g, "~");
}

export function normalizePointer(pointer: string): string {
  if (pointer === "") return "/";
  if (!pointer.startsWith("/")) {
    throw new LineageError(`JSON pointer must start with '/': ${pointer}`, {
      code: "INVALID_POINTER",
      path: pointer,
    });
  }
  return pointer;
}

export function joinPointer(base: string, segment: PathSegment): string {
  const normalized = normalizePointer(base);
  const tail = escapePointer(segment);
  return normalized === "/" ? `/${tail}` : `${normalized}/${tail}`;
}

export function appendPointer(base: string, relative: string): string {
  const normalizedBase = normalizePointer(base);
  const normalizedRelative = relative === "" ? "/" : normalizePointer(relative);
  if (normalizedRelative === "/") return normalizedBase;
  return normalizedBase === "/"
    ? normalizedRelative
    : `${normalizedBase}${normalizedRelative}`;
}

export function combinePointers(base: string, suffix: string): string {
  const normalizedBase = normalizePointer(base);
  const normalizedSuffix = normalizePointer(suffix);
  if (normalizedSuffix === "/") return normalizedBase;
  return normalizedBase === "/"
    ? normalizedSuffix
    : `${normalizedBase}${normalizedSuffix}`;
}

export function pointerSegments(pointer: string): string[] {
  const normalized = normalizePointer(pointer);
  if (normalized === "/") return [];
  return normalized.slice(1).split("/").map(unescapePointer);
}

export function resolvePointer<T extends JsonValue = JsonValue>(
  root: JsonValue,
  pointer: string,
): T {
  let current: JsonValue = root;
  const segments = pointerSegments(pointer);
  for (const segment of segments) {
    if (Array.isArray(current)) {
      const index = Number(segment);
      if (!/^(?:0|[1-9]\d*)$/.test(segment) || index >= current.length) {
        throw new LineageError(`Array index not found: ${segment}`, {
          code: "PATH_NOT_FOUND",
          path: pointer,
        });
      }
      current = current[index]!;
    } else if (isJsonObject(current)) {
      if (!Object.prototype.hasOwnProperty.call(current, segment)) {
        throw new LineageError(`Object key not found: ${segment}`, {
          code: "PATH_NOT_FOUND",
          path: pointer,
        });
      }
      current = current[segment]!;
    } else {
      throw new LineageError(`Cannot descend into ${typeof current} at ${pointer}`, {
        code: "PATH_NOT_FOUND",
        path: pointer,
      });
    }
  }
  return current as T;
}

export function hasPointer(root: JsonValue, pointer: string): boolean {
  try {
    resolvePointer(root, pointer);
    return true;
  } catch (error) {
    if (error instanceof LineageError && error.code === "PATH_NOT_FOUND") return false;
    throw error;
  }
}

export function assertJsonCompatible(
  value: unknown,
  location: { source?: string; version?: string; path?: string } = {},
): asserts value is JsonValue {
  const seen = new WeakSet<object>();
  const validate = (input: unknown, path: string): void => {
    if (input === null || typeof input === "string" || typeof input === "boolean") return;
    if (typeof input === "number") {
      if (!Number.isFinite(input)) {
        throw invalidJson("Non-finite number is not JSON compatible", path, location);
      }
      return;
    }
    if (typeof input === "undefined") {
      throw invalidJson("undefined is not JSON compatible", path, location);
    }
    if (typeof input === "bigint") {
      throw invalidJson("bigint is not JSON compatible", path, location);
    }
    if (typeof input === "function" || typeof input === "symbol") {
      throw invalidJson(`${typeof input} is not JSON compatible`, path, location);
    }
    if (typeof input !== "object") {
      throw invalidJson(`${typeof input} is not JSON compatible`, path, location);
    }
    if (seen.has(input)) {
      throw invalidJson("Circular reference is not JSON compatible", path, location);
    }
    seen.add(input);
    if (Array.isArray(input)) {
      input.forEach((item, index) => validate(item, joinPointer(path, index)));
    } else if (isPlainObjectLike(input)) {
      for (const [key, child] of Object.entries(input)) {
        validate(child, joinPointer(path, key));
      }
    } else {
      throw invalidJson("Object is not a plain JSON object", path, location);
    }
  };
  validate(value, location.path ?? "/");
}

function isPlainObjectLike(value: object): boolean {
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function invalidJson(
  message: string,
  path: string,
  location: { source?: string; version?: string },
): LineageError {
  return new LineageError(`${message} at ${path}`, {
    code: "INVALID_JSON",
    version: location.version,
    path,
    locations: location.version
      ? [{ source: location.source, version: location.version, path }]
      : [],
  });
}

function missingVersion(version: string): LineageError {
  return new LineageError(`Unknown lineage version: ${version}`, {
    code: "UNKNOWN_VERSION",
    version,
  });
}
