import type { ParsedFilter, ParsedScimPath } from "./types.ts";

const COMPARE_OPS = new Set([
  "eq",
  "ne",
  "co",
  "sw",
  "ew",
  "gt",
  "lt",
  "ge",
  "le",
]);

const ATTRIBUTE_SEGMENT = /^[A-Za-z0-9_$][A-Za-z0-9_$-]*$/;

export class ScimPathError extends Error {
  readonly code: "INVALID_PATH" | "INVALID_FILTER";
  readonly path: string;

  constructor(
    message: string,
    path: string,
    code: "INVALID_PATH" | "INVALID_FILTER" = "INVALID_PATH",
  ) {
    super(message);
    this.name = "ScimPathError";
    this.code = code;
    this.path = path;
  }
}

function splitSegments(
  raw: string,
  path: string,
  code: "INVALID_PATH" | "INVALID_FILTER" = "INVALID_PATH",
): string[] {
  const segments = raw.split(".");
  if (segments.length === 0) {
    throw new ScimPathError("Attribute path is empty", path, code);
  }

  for (const segment of segments) {
    if (!segment || !ATTRIBUTE_SEGMENT.test(segment)) {
      throw new ScimPathError(
        `Invalid attribute name ${JSON.stringify(segment)}`,
        path,
        code,
      );
    }
  }

  return segments.map((segment) => segment.toLowerCase());
}

function findFilterBounds(path: string): { open: number; close: number } | null {
  let quote: '"' | "'" | null = null;
  let escaped = false;
  let open = -1;

  for (let index = 0; index < path.length; index += 1) {
    const character = path[index];

    if (quote !== null) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === quote) {
        quote = null;
      }
      continue;
    }

    if (character === '"' || character === "'") {
      quote = character;
      continue;
    }

    if (character === "[") {
      if (open !== -1) {
        throw new ScimPathError(
          "Nested value filters are not supported by RFC 7644",
          path,
        );
      }
      open = index;
      continue;
    }

    if (character === "]") {
      if (open === -1) {
        throw new ScimPathError("Unexpected closing bracket in path", path);
      }
      return { open, close: index };
    }
  }

  if (quote !== null) {
    throw new ScimPathError("Unterminated quoted string in path", path);
  }
  if (open !== -1) {
    throw new ScimPathError("Unterminated value filter in path", path);
  }

  return null;
}

function parseCompareValue(raw: string, path: string): unknown {
  if (raw.startsWith('"')) {
    try {
      const value = JSON.parse(raw) as unknown;
      if (typeof value !== "string") {
        throw new Error("not a string");
      }
      return value;
    } catch {
      throw new ScimPathError(
        "Filter string value must be a valid JSON string",
        path,
        "INVALID_FILTER",
      );
    }
  }

  if (raw.startsWith("'")) {
    if (raw.length < 2 || !raw.endsWith("'")) {
      throw new ScimPathError(
        "Unterminated filter string value",
        path,
        "INVALID_FILTER",
      );
    }
    return raw
      .slice(1, -1)
      .replace(/\\'/g, "'")
      .replace(/\\\\/g, "\\");
  }

  const lowered = raw.toLowerCase();
  if (lowered === "true") {
    return true;
  }
  if (lowered === "false") {
    return false;
  }
  if (lowered === "null") {
    return null;
  }

  if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(raw)) {
    return Number(raw);
  }

  if (/\s/.test(raw) || raw.length === 0) {
    throw new ScimPathError(
      "Unquoted filter values must be a single token",
      path,
      "INVALID_FILTER",
    );
  }

  return raw;
}

export function parseScimFilter(raw: string, path = raw): ParsedFilter {
  const trimmed = raw.trim();
  if (!trimmed) {
    throw new ScimPathError("Value filter is empty", path, "INVALID_FILTER");
  }

  const match =
    /^(.+?)\s+(eq|ne|co|sw|ew|gt|lt|ge|le)\s+(.+)$/i.exec(trimmed);
  if (!match) {
    throw new ScimPathError(
      "Value filter must use the form: attrPath compareOp compValue",
      path,
      "INVALID_FILTER",
    );
  }

  const attributeRaw = match[1].trim();
  const operation = match[2].toLowerCase();
  const valueRaw = match[3].trim();

  if (!COMPARE_OPS.has(operation)) {
    throw new ScimPathError(
      `Unsupported filter operator ${JSON.stringify(operation)}`,
      path,
      "INVALID_FILTER",
    );
  }
  if (!attributeRaw || /\s|[[\]]/.test(attributeRaw)) {
    throw new ScimPathError(
      "Filter attribute path is invalid",
      path,
      "INVALID_FILTER",
    );
  }

  const attributeSegments = splitSegments(
    attributeRaw,
    path,
    "INVALID_FILTER",
  );

  return {
    raw: trimmed,
    attribute: attributeRaw.toLowerCase(),
    attributeSegments,
    op: operation,
    value: parseCompareValue(valueRaw, path),
    valueRaw,
  };
}

function parseSchemaAndAttribute(
  raw: string,
  path: string,
  knownSchemaUrns: readonly string[],
): { schemaUrn?: string; attributeRaw: string } {
  const lowered = raw.toLowerCase();
  const candidates = [...knownSchemaUrns]
    .map((urn) => ({ urn, lowered: urn.toLowerCase() }))
    .sort((left, right) => right.lowered.length - left.lowered.length);

  for (const candidate of candidates) {
    const prefix = `${candidate.lowered}:`;
    if (lowered.startsWith(prefix)) {
      const attributeRaw = raw.slice(prefix.length);
      if (!attributeRaw) {
        throw new ScimPathError(
          "Schema URN path must include an attribute name",
          path,
        );
      }
      return { schemaUrn: candidate.urn, attributeRaw };
    }
  }

  if (lowered.startsWith("urn:")) {
    // RFC 7644 paths use "schemaUrn:attribute". Schema URNs may contain dots
    // (for example, "2.0"), so the final colon is the safest delimiter when
    // no caller-supplied schema list is available.
    const delimiter = raw.lastIndexOf(":");
    if (delimiter <= "urn:".length) {
      throw new ScimPathError("Invalid schema URN attribute path", path);
    }
    const schemaUrn = raw.slice(0, delimiter);
    const attributeRaw = raw.slice(delimiter + 1);
    if (!attributeRaw) {
      throw new ScimPathError(
        "Schema URN path must include an attribute name",
        path,
      );
    }
    return { schemaUrn, attributeRaw };
  }

  return { attributeRaw: raw };
}

export function parseScimPath(
  rawPath: string,
  knownSchemaUrns: readonly string[] = [],
): ParsedScimPath {
  if (typeof rawPath !== "string") {
    throw new ScimPathError("PATCH path must be a string", String(rawPath));
  }

  const raw = rawPath.trim();
  if (!raw) {
    throw new ScimPathError("PATCH path must not be empty", rawPath);
  }

  const bounds = findFilterBounds(raw);
  const baseRaw = bounds ? raw.slice(0, bounds.open).trim() : raw;
  if (!baseRaw) {
    throw new ScimPathError("PATCH path has no attribute before filter", raw);
  }

  const { schemaUrn, attributeRaw } = parseSchemaAndAttribute(
    baseRaw,
    raw,
    knownSchemaUrns,
  );
  const attributeSegments = splitSegments(attributeRaw, raw);

  let filter: ParsedFilter | undefined;
  let subAttributeSegments: string[] = [];

  if (bounds) {
    const filterRaw = raw.slice(bounds.open + 1, bounds.close);
    filter = parseScimFilter(filterRaw, raw);

    const suffixRaw = raw.slice(bounds.close + 1).trim();
    if (suffixRaw) {
      if (!suffixRaw.startsWith(".")) {
        throw new ScimPathError(
          "Only a sub-attribute may follow a value filter",
          raw,
        );
      }
      subAttributeSegments = splitSegments(suffixRaw.slice(1), raw);
    }
  }

  const targetSegments = [
    ...attributeSegments,
    ...subAttributeSegments,
  ];

  return {
    raw,
    schemaUrn,
    attributeSegments,
    subAttributeSegments,
    targetSegments,
    filter,
  };
}

export function canonicalSegments(path: ParsedScimPath): string {
  return path.targetSegments.join(".");
}
