import type {
  HeaderCondition,
  HeaderConditionInput,
  HeadersInput,
  PathCondition,
  PathInput,
  RouteRequest,
} from "./types.ts";

export function normalizePath(value: string): string {
  let path = value.trim();
  if (!path.startsWith("/")) {
    throw new Error(`route path must start with "/": ${value}`);
  }
  path = path.replace(/\/+$/, "");
  return path.length === 0 ? "/" : path;
}

export function parsePath(input: PathInput | undefined): PathCondition {
  if (input === undefined) {
    return { kind: "prefix", value: "/" };
  }

  if (typeof input === "string") {
    const trimmed = input.trim();
    if (trimmed.startsWith("=")) {
      return { kind: "exact", value: normalizePath(trimmed.slice(1)) };
    }
    if (trimmed.endsWith("*")) {
      return { kind: "prefix", value: normalizePath(trimmed.slice(0, -1)) };
    }
    return { kind: "exact", value: normalizePath(trimmed) };
  }

  if ("exact" in input && typeof input.exact === "string") {
    return { kind: "exact", value: normalizePath(input.exact) };
  }
  if ("prefix" in input && typeof input.prefix === "string") {
    return { kind: "prefix", value: normalizePath(input.prefix) };
  }
  if ("kind" in input && (input.kind === "exact" || input.kind === "prefix")) {
    return { kind: input.kind, value: normalizePath(input.value) };
  }
  if ("type" in input && (input.type === "exact" || input.type === "prefix")) {
    return { kind: input.type, value: normalizePath(input.value) };
  }
  if ("match" in input && (input.match === "exact" || input.match === "prefix")) {
    return { kind: input.match, value: normalizePath(input.path) };
  }

  throw new Error("invalid path matcher");
}

export function segmentPrefixCovers(outer: string, inner: string): boolean {
  if (outer === "/") {
    return true;
  }
  return inner === outer || inner.startsWith(`${outer}/`);
}

/**
 * True when every path accepted by `inner` is also accepted by `outer`.
 */
export function pathCovers(outer: PathCondition, inner: PathCondition): boolean {
  if (outer.kind === "exact") {
    return inner.kind === "exact" && inner.value === outer.value;
  }
  if (inner.kind === "exact") {
    return segmentPrefixCovers(outer.value, inner.value);
  }
  return segmentPrefixCovers(outer.value, inner.value);
}

export function intersectPaths(
  left: PathCondition,
  right: PathCondition,
): PathCondition | null {
  if (left.kind === "exact" && right.kind === "exact") {
    return left.value === right.value ? left : null;
  }

  if (left.kind === "exact") {
    return segmentPrefixCovers(right.value, left.value) ? left : null;
  }

  if (right.kind === "exact") {
    return segmentPrefixCovers(left.value, right.value) ? right : null;
  }

  if (segmentPrefixCovers(left.value, right.value)) {
    return right;
  }
  if (segmentPrefixCovers(right.value, left.value)) {
    return left;
  }
  return null;
}

export function pathMatches(condition: PathCondition, requestPath: string): boolean {
  const path = normalizePath(requestPath);
  return condition.kind === "exact"
    ? path === condition.value
    : segmentPrefixCovers(condition.value, path);
}

function normalizeHeaderName(name: string): string {
  const normalized = name.trim().toLowerCase();
  if (normalized.length === 0) {
    throw new Error("header name must not be empty");
  }
  return normalized;
}

function parseHeaderCondition(input: HeaderConditionInput): HeaderCondition {
  const nameText = input.name ?? input.header ?? input.key;
  if (typeof nameText !== "string") {
    throw new Error("header condition requires a name");
  }
  const name = normalizeHeaderName(nameText);

  if (input.match === "absent" || input.match === "missing" || input.type === "absent" ||
      input.type === "missing" || input.absent === true || input.missing === true ||
      input.present === false || input.value === null) {
    return { name, kind: "absent" };
  }

  if (input.match === "present" || input.type === "present" || input.present === true ||
      input.absent === false) {
    return { name, kind: "present" };
  }

  if (input.match === "exact" || input.type === "exact") {
    if (typeof input.exact === "string") {
      return { name, kind: "exact", value: input.exact };
    }
    if (typeof input.value === "string") {
      return { name, kind: "exact", value: input.value };
    }
    throw new Error(`header "${name}" needs an exact value`);
  }

  if (typeof input.exact === "string") {
    return { name, kind: "exact", value: input.exact };
  }
  if (typeof input.value === "string") {
    return { name, kind: "exact", value: input.value };
  }
  if (input.exact === true) {
    return { name, kind: "present" };
  }

  return { name, kind: "present" };
}

/**
 * Parses headers into a canonical conjunction. Conflicting literals are
 * rejected here; intersection callers receive null and prune the branch.
 */
export function parseHeaders(input: HeadersInput | undefined): HeaderCondition[] {
  if (input === undefined) {
    return [];
  }

  if (Array.isArray(input)) {
    return mergeHeaderSets([], input.map(parseHeaderCondition)) ?? failConflict();
  }

  const conditions: HeaderCondition[] = [];
  for (const [name, value] of Object.entries(input as Record<string, string | null | boolean>)) {
    if (typeof value === "string") {
      conditions.push({ name: normalizeHeaderName(name), kind: "exact", value });
    } else if (value === null || value === false) {
      conditions.push({ name: normalizeHeaderName(name), kind: "absent" });
    } else {
      conditions.push({ name: normalizeHeaderName(name), kind: "present" });
    }
  }
  return mergeHeaderSets([], conditions) ?? failConflict();
}

function failConflict(): never {
  throw new Error("contradictory header conditions in one rule");
}

/**
 * Intersects two header conjunctions. Returns null when no request can satisfy
 * both sides. Exact and present conditions are merged without inventing values.
 */
export function mergeHeaderSets(
  left: readonly HeaderCondition[],
  right: readonly HeaderCondition[],
): HeaderCondition[] | null {
  const merged = new Map<string, HeaderCondition>();

  const add = (condition: HeaderCondition): boolean => {
    const name = normalizeHeaderName(condition.name);
    const existing = merged.get(name);
    if (existing === undefined) {
      merged.set(name, condition);
      return true;
    }

    if (condition.kind === "present") {
      if (existing.kind === "absent") {
        return false;
      }
      return true;
    }

    if (condition.kind === "exact") {
      if (existing.kind === "absent") {
        return false;
      }
      if (existing.kind === "exact" && existing.value !== condition.value) {
        return false;
      }
      merged.set(name, condition);
      return true;
    }

    if (existing.kind === "exact" || existing.kind === "present") {
      return false;
    }
    return true;
  };

  for (const condition of [...left, ...right]) {
    if (!add(condition)) {
      return null;
    }
  }

  return [...merged.values()].sort((a, b) => a.name.localeCompare(b.name));
}

function requestHeaderValues(
  headers: RouteRequest["headers"],
  name: string,
): readonly string[] | undefined {
  if (headers === undefined) {
    return undefined;
  }

  const key = Object.keys(headers).find((candidate) => candidate.toLowerCase() === name);
  if (key === undefined) {
    return undefined;
  }

  const value = headers[key];
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value === "string") {
    return [value];
  }
  return value;
}

export function headersMatch(
  conditions: readonly HeaderCondition[],
  request: RouteRequest,
): boolean {
  for (const condition of conditions) {
    const values = requestHeaderValues(request.headers, condition.name);
    const present = values !== undefined && values.length > 0;

    if (condition.kind === "absent") {
      if (present) {
        return false;
      }
    } else if (condition.kind === "present") {
      if (!present) {
        return false;
      }
    } else if (!present || !values?.includes(condition.value)) {
      return false;
    }
  }
  return true;
}

export function ruleMatchesPathAndHeaders(
  path: PathCondition,
  headers: readonly HeaderCondition[],
  request: RouteRequest,
): boolean {
  return pathMatches(path, request.path) && headersMatch(headers, request);
}
