import type {
  ByteRange,
  ParsedArrayNode,
  ParsedJson,
  ParsedNode,
  ParsedObjectEntry,
  ParsedObjectNode,
} from "./parser.ts";
import {
  appendArrayPath,
  appendJsonPath,
  quoteJsonString,
} from "./parser.ts";
import { exactNumberToSafeValue } from "./number.ts";

export type DuplicatePolicy = "first" | "last" | "reject";
export type NumberValueMode = "safe" | "exact" | "number" | "bigint";

export type JsonValue =
  | null
  | boolean
  | string
  | number
  | bigint
  | JsonValue[]
  | { [key: string]: JsonValue };

export interface CanonicalObjectEntry {
  /** Canonical key spelling. Protected keys are case-normalized. */
  key: string;
  /** The decoded key spelling exactly as it appeared in the request. */
  sourceKey: string;
  /** Source key token, including its quotes and escapes. */
  rawKey: string;
  keyRange: ByteRange;
  value: CanonicalNode;
  /** Position among all entries in the original object, including duplicates. */
  originalIndex: number;
}

export interface CanonicalObjectNode extends ByteRange {
  kind: "object";
  path: string;
  /** Alias for start/end, convenient for consumers that pass ranges around. */
  range: ByteRange;
  entries: CanonicalObjectEntry[];
}

export interface CanonicalArrayNode extends ByteRange {
  kind: "array";
  path: string;
  range: ByteRange;
  items: CanonicalNode[];
}

export interface CanonicalStringNode extends ByteRange {
  kind: "string";
  path: string;
  range: ByteRange;
  value: string;
  raw: string;
}

export interface CanonicalNumberNode extends ByteRange {
  kind: "number";
  path: string;
  range: ByteRange;
  /** Exact source token. */
  raw: string;
  /** Deterministic precision-preserving numeric form. */
  canonical: string;
}

export interface CanonicalBooleanNode extends ByteRange {
  kind: "boolean";
  path: string;
  range: ByteRange;
  value: boolean;
}

export interface CanonicalNullNode extends ByteRange {
  kind: "null";
  path: string;
  range: ByteRange;
  value: null;
}

export type CanonicalNode =
  | CanonicalObjectNode
  | CanonicalArrayNode
  | CanonicalStringNode
  | CanonicalNumberNode
  | CanonicalBooleanNode
  | CanonicalNullNode;

export interface DuplicateOccurrence {
  key: string;
  rawKey: string;
  keyRange: ByteRange;
  valueRange: ByteRange;
  index: number;
  selected: boolean;
}

export interface DuplicateKeyRecord {
  /** JSONPath-like path for the key, for example `$.profile.role`. */
  path: string;
  /** Canonical display key (case-normalized for protected keys). */
  key: string;
  /** All distinct decoded spellings observed for this key. */
  keys: string[];
  protected: boolean;
  /** True when the duplicate is only visible after auth-key case folding. */
  caseVariants: boolean;
  policy: DuplicatePolicy;
  resolution: DuplicatePolicy;
  occurrences: DuplicateOccurrence[];
}

export interface CanonicalizeOptions {
  duplicatePolicy?: DuplicatePolicy;
  protectedKeys?: readonly string[];
  caseInsensitiveProtectedKeys?: boolean;
}

export interface CanonicalJson {
  root: CanonicalNode;
  duplicates: DuplicateKeyRecord[];
}

export const DEFAULT_PROTECTED_KEYS = Object.freeze([
  "role",
  "roles",
  "tenantId",
  "tenant_id",
  "scope",
  "scopes",
  "permissions",
  "permission",
  "userId",
  "user_id",
  "sub",
  "iss",
  "aud",
  "groups",
  "claims",
  "authorization",
] as const);

export function asciiLowerCase(value: string): string {
  let output = "";
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);
    output +=
      codeUnit >= 0x41 && codeUnit <= 0x5a
        ? String.fromCharCode(codeUnit + 0x20)
        : value[index];
  }
  return output;
}

export function canonicalizeParsedJson(
  parsed: ParsedJson,
  options: CanonicalizeOptions = {},
): CanonicalJson {
  const duplicatePolicy = options.duplicatePolicy ?? "last";
  if (
    duplicatePolicy !== "first" &&
    duplicatePolicy !== "last" &&
    duplicatePolicy !== "reject"
  ) {
    throw new TypeError(
      "duplicatePolicy must be 'first', 'last', or 'reject'",
    );
  }

  const caseInsensitiveProtectedKeys =
    options.caseInsensitiveProtectedKeys ?? true;
  const protectedNames = new Map<string, string>();
  for (const key of options.protectedKeys ?? DEFAULT_PROTECTED_KEYS) {
    const normalized = caseInsensitiveProtectedKeys
      ? asciiLowerCase(key)
      : key;
    if (!protectedNames.has(normalized)) {
      protectedNames.set(normalized, key);
    }
  }

  const duplicates: DuplicateKeyRecord[] = [];

  const canonicalizeNode = (node: ParsedNode): CanonicalNode => {
    if (node.kind === "object") {
      return canonicalizeObject(node);
    }
    if (node.kind === "array") {
      return canonicalizeArray(node);
    }
    if (node.kind === "string") {
      return {
        kind: "string",
        start: node.start,
        end: node.end,
        range: { start: node.start, end: node.end },
        path: pathOf(node),
        value: node.value,
        raw: node.raw,
      };
    }
    if (node.kind === "number") {
      return {
        kind: "number",
        start: node.start,
        end: node.end,
        range: { start: node.start, end: node.end },
        path: pathOf(node),
        raw: node.raw,
        canonical: node.canonical,
      };
    }
    if (node.kind === "boolean") {
      return {
        kind: "boolean",
        start: node.start,
        end: node.end,
        range: { start: node.start, end: node.end },
        path: pathOf(node),
        value: node.value,
      };
    }
    return {
      kind: "null",
      start: node.start,
      end: node.end,
      range: { start: node.start, end: node.end },
      path: pathOf(node),
      value: null,
    };
  };

  const pathOf = (node: ParsedNode): string => {
    return primitivePaths.get(node) ?? "$";
  };

  const primitivePaths = new Map<ParsedNode, string>();

  const canonicalizeArray = (node: ParsedArrayNode): CanonicalArrayNode => {
    const arrayPath = pathOf(node);
    return {
      kind: "array",
      start: node.start,
      end: node.end,
      range: { start: node.start, end: node.end },
      path: arrayPath,
      items: node.items.map((item, index) => {
        enablePath(item, appendArrayPath(arrayPath, index));
        return canonicalizeNode(item);
      }),
    };
  };

  const canonicalizeObject = (node: ParsedObjectNode): CanonicalObjectNode => {
    const objectPath = pathOf(node);
    type Group = {
      entries: ParsedObjectEntry[];
      protected: boolean;
      canonicalKey: string;
    };
    const groups = new Map<string, Group>();

    for (const entry of node.entries) {
      const normalized = caseInsensitiveProtectedKeys
        ? asciiLowerCase(entry.key)
        : entry.key;
      const canonicalProtectedKey = protectedNames.get(normalized);
      const protectedKey = canonicalProtectedKey !== undefined;
      const groupKey = protectedKey
        ? `protected:${normalized}`
        : `key:${entry.key}`;
      let group = groups.get(groupKey);
      if (group === undefined) {
        group = {
          entries: [],
          protected: protectedKey,
          canonicalKey: canonicalProtectedKey ?? entry.key,
        };
        groups.set(groupKey, group);
      }
      group.entries.push(entry);
    }

    const selectedEntries: ParsedObjectEntry[] = [];
    for (const group of groups.values()) {
      const entries = group.entries;
      let selectedEntry = entries[0];
      if (duplicatePolicy !== "first") {
        selectedEntry = entries[entries.length - 1];
      }

      if (entries.length > 1) {
        const keys = Array.from(new Set(entries.map((entry) => entry.key)));
        const occurrences = entries.map((entry) => ({
          key: entry.key,
          rawKey: entry.rawKey,
          keyRange: entry.keyRange,
          valueRange: {
            start: entry.value.start,
            end: entry.value.end,
          },
          index: entry.index,
          selected: entry === selectedEntry,
        }));
        const resolution: DuplicatePolicy = group.protected
          ? "reject"
          : duplicatePolicy;
        duplicates.push({
          path: appendJsonPath(objectPath, group.canonicalKey),
          key: group.canonicalKey,
          keys,
          protected: group.protected,
          caseVariants: keys.length > 1,
          policy: duplicatePolicy,
          resolution,
          occurrences,
        });
      }

      selectedEntries.push(selectedEntry);
    }

    const canonicalValues = new Map<ParsedObjectEntry, CanonicalNode>();
    for (const entry of node.entries) {
      const normalized = caseInsensitiveProtectedKeys
        ? asciiLowerCase(entry.key)
        : entry.key;
      const canonicalKey =
        protectedNames.get(normalized) ?? entry.key;
      enablePath(entry.value, appendJsonPath(objectPath, canonicalKey));
      canonicalValues.set(entry, canonicalizeNode(entry.value));
    }

    return {
      kind: "object",
      start: node.start,
      end: node.end,
      range: { start: node.start, end: node.end },
      path: objectPath,
      entries: selectedEntries.map((entry) => {
        const normalized = caseInsensitiveProtectedKeys
          ? asciiLowerCase(entry.key)
          : entry.key;
        const canonicalKey =
          protectedNames.get(normalized) ?? entry.key;
        const value = canonicalValues.get(entry);
        if (value === undefined) {
          throw new Error("Internal canonicalization error");
        }
        return {
          key: canonicalKey,
          sourceKey: entry.key,
          rawKey: entry.rawKey,
          keyRange: entry.keyRange,
          value,
          originalIndex: entry.index,
        };
      }),
    };
  };

  const enablePath = (node: ParsedNode, path: string): void => {
    primitivePaths.set(node, path);
  };

  primitivePaths.set(parsed.root, "$");
  const root = canonicalizeNode(parsed.root);
  return { root, duplicates };
}

export function toPlainValue(
  node: CanonicalNode,
  numberMode: NumberValueMode = "safe",
): JsonValue {
  if (node.kind === "null") {
    return null;
  }
  if (node.kind === "boolean" || node.kind === "string") {
    return node.value;
  }
  if (node.kind === "number") {
    if (numberMode === "exact") {
      return node.raw;
    }
    if (numberMode === "number") {
      return Number(node.raw);
    }
    if (numberMode === "bigint") {
      if (/^-?\d+$/.test(node.raw)) {
        return BigInt(node.raw);
      }
      return node.raw;
    }
    return exactNumberToSafeValue(node.raw, node.canonical);
  }
  if (node.kind === "array") {
    return node.items.map((item) => toPlainValue(item, numberMode));
  }

  const object: { [key: string]: JsonValue } = {};
  for (const entry of node.entries) {
    Object.defineProperty(object, entry.key, {
      value: toPlainValue(entry.value, numberMode),
      enumerable: true,
      configurable: true,
      writable: true,
    });
  }
  return object;
}

const utf8Encoder = new TextEncoder();

function compareUtf8(left: string, right: string): number {
  const leftBytes = utf8Encoder.encode(left);
  const rightBytes = utf8Encoder.encode(right);
  const length = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < length; index += 1) {
    if (leftBytes[index] !== rightBytes[index]) {
      return leftBytes[index] - rightBytes[index];
    }
  }
  return leftBytes.length - rightBytes.length;
}

export function serializeCanonical(node: CanonicalNode): string {
  if (node.kind === "null") {
    return "null";
  }
  if (node.kind === "boolean") {
    return node.value ? "true" : "false";
  }
  if (node.kind === "string") {
    return quoteJsonString(node.value);
  }
  if (node.kind === "number") {
    return node.canonical;
  }
  if (node.kind === "array") {
    return `[${node.items.map((item) => serializeCanonical(item)).join(",")}]`;
  }

  const entries = [...node.entries].sort((left, right) =>
    compareUtf8(left.key, right.key),
  );
  return `{${entries
    .map(
      (entry) =>
        `${quoteJsonString(entry.key)}:${serializeCanonical(entry.value)}`,
    )
    .join(",")}}`;
}

export function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== "object") {
    return value;
  }
  if (Object.isFrozen(value)) {
    return value;
  }
  Object.freeze(value);
  for (const key of Reflect.ownKeys(value)) {
    const child = (value as Record<PropertyKey, unknown>)[key];
    if (child !== null && typeof child === "object") {
      deepFreeze(child);
    }
  }
  return value;
}
