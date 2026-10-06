export type Segment =
  | { readonly kind: "static"; readonly value: string }
  | { readonly kind: "param"; readonly name: string; readonly optional: boolean };

export class RouteError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RouteError";
  }
}

export class DuplicateParamError extends RouteError {
  constructor(paramName: string, source: string) {
    super(
      `duplicate parameter ":${paramName}" on the same effective path (${source}); ` +
        `each parameter name must be unique within a composed route`,
    );
    this.name = "DuplicateParamError";
  }
}

export class AmbiguousPatternError extends RouteError {
  constructor(source: string) {
    super(
      `ambiguous pattern (${source}): two optional segments without a static ` +
        `segment between them cannot be parsed back unambiguously`,
    );
    this.name = "AmbiguousPatternError";
  }
}

const PARAM_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function compilePattern(pattern: string): Segment[] {
  if (typeof pattern !== "string" || pattern.length === 0) {
    throw new RouteError("pattern must be a non-empty string");
  }
  if (!pattern.startsWith("/")) {
    throw new RouteError(`pattern must start with "/": "${pattern}"`);
  }
  if (pattern.length > 1 && pattern.endsWith("/")) {
    throw new RouteError(
      `pattern must not end with "/": "${pattern}" (use "/" for the root route)`,
    );
  }
  const parts = pattern === "/" ? [] : pattern.slice(1).split("/");
  const segments: Segment[] = parts.map((part) => {
    if (part === "") {
      throw new RouteError(`empty segment in pattern "${pattern}"`);
    }
    if (part.startsWith(":")) {
      const optional = part.endsWith("?");
      const name = optional ? part.slice(1, -1) : part.slice(1);
      if (!PARAM_NAME.test(name)) {
        throw new RouteError(
          `invalid parameter name ":${name}" in pattern "${pattern}"`,
        );
      }
      return { kind: "param", name, optional };
    }
    return { kind: "static", value: part };
  });
  validateSegments(segments, pattern);
  return segments;
}

export function validateSegments(
  segments: readonly Segment[],
  source: string,
): void {
  const seen = new Set<string>();
  let lastOptionalIndex = -1;
  for (let index = 0; index < segments.length; index += 1) {
    const segment = segments[index]!;
    if (segment.kind !== "param") continue;
    if (seen.has(segment.name)) {
      throw new DuplicateParamError(segment.name, source);
    }
    seen.add(segment.name);
    if (segment.optional) {
      if (lastOptionalIndex >= 0) {
        const anchored = segments
          .slice(lastOptionalIndex + 1, index)
          .some((between) => between.kind === "static");
        if (!anchored) {
          throw new AmbiguousPatternError(source);
        }
      }
      lastOptionalIndex = index;
    }
  }
}

export function joinPatterns(parent: string, child: string): string {
  if (parent === "/") return child;
  return parent + child;
}
