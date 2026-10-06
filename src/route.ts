import {
  compilePattern,
  joinPatterns,
  validateSegments,
  RouteError,
  type Segment,
} from "./pattern.ts";

export class MissingParamError extends RouteError {
  constructor(paramName: string, pattern: string) {
    super(`missing required parameter ":${paramName}" for route "${pattern}"`);
    this.name = "MissingParamError";
  }
}

export class EmptyParamError extends RouteError {
  constructor(paramName: string, pattern: string) {
    super(
      `parameter ":${paramName}" for route "${pattern}" must not be an empty ` +
        `string; an empty segment would make the URL unparseable`,
    );
    this.name = "EmptyParamError";
  }
}

export class UnknownParamError extends RouteError {
  constructor(paramName: string, pattern: string) {
    super(`unknown parameter ":${paramName}" for route "${pattern}"`);
    this.name = "UnknownParamError";
  }
}

type RequiredParamsOf<P extends string> =
  P extends `${string}:${infer Rest}`
    ? Rest extends `${infer Name}/${infer Tail}`
      ? (Name extends `${string}?` ? never : Name) | RequiredParamsOf<`/${Tail}`>
      : Rest extends `${string}?`
        ? never
        : Rest
    : never;

type OptionalParamsOf<P extends string> =
  P extends `${string}:${infer Rest}`
    ? Rest extends `${infer Name}/${infer Tail}`
      ? (Name extends `${infer N}?` ? N : never) | OptionalParamsOf<`/${Tail}`>
      : Rest extends `${infer N}?`
        ? N
        : never
    : never;

export type ParamsOf<P extends string> = {
  [K in RequiredParamsOf<P>]: string;
} & {
  [K in OptionalParamsOf<P>]?: string;
};

export type JoinPatterns<A extends string, B extends string> = A extends "/"
  ? B
  : `${A}${B}`;
export class Route<P extends string = string> {
  readonly pattern: P;
  readonly segments: readonly Segment[];

  constructor(pattern: P) {
    this.segments = compilePattern(pattern);
    this.pattern = pattern;
  }

  static fromSegments<P extends string>(
    pattern: P,
    segments: readonly Segment[],
  ): Route<P> {
    const route = Object.create(Route.prototype) as Route<P>;
    Object.defineProperty(route, "pattern", { value: pattern });
    Object.defineProperty(route, "segments", { value: segments });
    return route;
  }

  get paramNames(): string[] {
    return this.segments.flatMap((segment) =>
      segment.kind === "param" ? [segment.name] : [],
    );
  }

  get requiredParams(): string[] {
    return this.segments.flatMap((segment) =>
      segment.kind === "param" && !segment.optional ? [segment.name] : [],
    );
  }

  get optionalParams(): string[] {
    return this.segments.flatMap((segment) =>
      segment.kind === "param" && segment.optional ? [segment.name] : [],
    );
  }

  child<C extends string>(childPattern: C): Route<JoinPatterns<P, C>> {
    const childSegments = compilePattern(childPattern);
    const combined = [...this.segments, ...childSegments];
    const joined = joinPatterns(this.pattern, childPattern);
    validateSegments(combined, joined);
    return Route.fromSegments(joined as JoinPatterns<P, C>, combined);
  }

  build(params: ParamsOf<P> = {} as ParamsOf<P>): string {
    const record = params as Record<string, string | undefined>;
    const known = new Set(this.paramNames);
    for (const key of Object.keys(record)) {
      if (!known.has(key)) {
        throw new UnknownParamError(key, this.pattern);
      }
    }
    const parts: string[] = [];
    for (const segment of this.segments) {
      if (segment.kind === "static") {
        parts.push(encodeURIComponent(segment.value));
        continue;
      }
      const value = record[segment.name];
      if (value === undefined) {
        if (segment.optional) continue;
        throw new MissingParamError(segment.name, this.pattern);
      }
      if (typeof value !== "string" || value === "") {
        throw new EmptyParamError(segment.name, this.pattern);
      }
      parts.push(encodeURIComponent(value));
    }
    return "/" + parts.join("/");
  }

  match(path: string): Record<string, string> | null {
    const parts = splitPath(path);
    if (parts === null) return null;
    const out: Record<string, string> = {};
    return matchFrom(this.segments, 0, parts, 0, out) ? out : null;
  }

  toString(): string {
    return this.pattern;
  }
}

export function splitPath(path: string): string[] | null {
  if (typeof path !== "string" || !path.startsWith("/")) return null;
  if (path.includes("?") || path.includes("#")) return null;
  let normalized = path;
  if (normalized.length > 1 && normalized.endsWith("/")) {
    normalized = normalized.slice(0, -1);
  }
  if (normalized === "/") return [];
  const raw = normalized.slice(1).split("/");
  const decoded: string[] = [];
  for (const part of raw) {
    if (part === "") return null;
    try {
      decoded.push(decodeURIComponent(part));
    } catch {
      return null;
    }
  }
  return decoded;
}

function matchFrom(
  segments: readonly Segment[],
  segmentIndex: number,
  parts: readonly string[],
  partIndex: number,
  out: Record<string, string>,
): boolean {
  if (segmentIndex === segments.length) {
    return partIndex === parts.length;
  }
  const segment = segments[segmentIndex]!;
  if (segment.kind === "static") {
    return (
      partIndex < parts.length &&
      parts[partIndex] === segment.value &&
      matchFrom(segments, segmentIndex + 1, parts, partIndex + 1, out)
    );
  }
  if (!segment.optional) {
    if (partIndex >= parts.length) return false;
    out[segment.name] = parts[partIndex]!;
    return matchFrom(segments, segmentIndex + 1, parts, partIndex + 1, out);
  }
  if (partIndex < parts.length) {
    out[segment.name] = parts[partIndex]!;
    if (matchFrom(segments, segmentIndex + 1, parts, partIndex + 1, out)) {
      return true;
    }
    delete out[segment.name];
  }
  return matchFrom(segments, segmentIndex + 1, parts, partIndex, out);
}
