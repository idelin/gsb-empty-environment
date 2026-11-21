import {
  decodeBase64,
  decodeForm,
  decodeHex,
  decodeHtmlEntities,
  decodeUnicodeEscapes,
  encodeHtmlEntities,
  encodeJsonEscapes,
  encodeUnicodeEscapes,
  safeDecodeURIComponent,
} from "./codec.ts";
import { DesensitizationLeakError } from "./errors.ts";
import type { DetectionOptions, Leak } from "./types.ts";

const MAX_VARIANT_LENGTH = 32_768;
const MAX_VARIANTS_PER_VALUE = 144;
const MAX_TOTAL_VARIANTS = 20_000;
const MAX_DECODE_CANDIDATES = 4_000;

export interface ResolvedDetection {
  percent: boolean;
  base64: boolean;
  hex: boolean;
  unicodeEscapes: boolean;
  htmlEntities: boolean;
  normalized: boolean;
  reversed: boolean;
  caseInsensitive: boolean;
  separated: boolean;
  splitAcrossFields: boolean;
  minLength: number;
  maxDepth: number;
}

export function resolveDetection(
  options: DetectionOptions | undefined,
): ResolvedDetection {
  return {
    percent: options?.percent ?? true,
    base64: options?.base64 ?? true,
    hex: options?.hex ?? true,
    unicodeEscapes: options?.unicodeEscapes ?? true,
    htmlEntities: options?.htmlEntities ?? true,
    normalized: options?.normalized ?? true,
    reversed: options?.reversed ?? false,
    caseInsensitive: options?.caseInsensitive ?? false,
    separated: options?.separated ?? true,
    splitAcrossFields: options?.splitAcrossFields ?? true,
    minLength: Math.max(1, options?.minLength ?? 4),
    maxDepth: Math.max(0, Math.min(4, options?.maxDepth ?? 2)),
  };
}

interface VariantEntry {
  text: string;
  value: string;
  encoding: string;
}

export interface SensitiveSpan {
  start: number;
  end: number;
  value: string;
  encoding: string;
}

export interface VariantIndex {
  values: string[];
  minLength: number;
  options: ResolvedDetection;
  entries: VariantEntry[];
  longValues: string[];
  matcher: AhoMatcher;
  compactMatcher: AhoMatcher;
  compactDiffersFromRaw: boolean;
}

function collectPrimitiveStrings(
  value: unknown,
  output: Set<string>,
  seen: Set<object>,
): void {
  if (value === null || value === undefined) {
    return;
  }
  const type = typeof value;
  if (type === "string") {
    if (value.length > 0) output.add(value as string);
    return;
  }
  if (type === "number" || type === "bigint" || type === "boolean") {
    output.add(String(value));
    return;
  }
  if (type !== "object") {
    return;
  }
  if (seen.has(value as object)) {
    return;
  }
  seen.add(value as object);
  if (value instanceof Date) {
    output.add(value.toISOString());
    seen.delete(value as object);
    return;
  }
  if (value instanceof Map) {
    for (const [mapKey, mapValue] of value.entries()) {
      collectPrimitiveStrings(mapKey, output, seen);
      collectPrimitiveStrings(mapValue, output, seen);
    }
    seen.delete(value as object);
    return;
  }
  if (value instanceof Set) {
    for (const item of value) {
      collectPrimitiveStrings(item, output, seen);
    }
    seen.delete(value as object);
    return;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      collectPrimitiveStrings(item, output, seen);
    }
  } else {
    for (const item of Object.values(value as Record<string, unknown>)) {
      collectPrimitiveStrings(item, output, seen);
    }
  }
  seen.delete(value as object);
}

export function normalizeSensitiveInputs(
  inputs: Iterable<unknown>,
): string[] {
  const output = new Set<string>();
  for (const input of inputs) {
    collectPrimitiveStrings(input, output, new Set<object>());
  }
  return [...output];
}

const SEPARATOR_PATTERN = /[\s\u00A0._:/|,+-]/;
const SEPARATOR_GLOBAL_PATTERN = /[\s\u00A0._:/|,+-]/g;

function stripSeparators(value: string): {
  compact: string;
  positions: number[];
} {
  let compact = "";
  const positions: number[] = [];
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (SEPARATOR_PATTERN.test(character)) continue;
    compact += character;
    positions.push(index);
  }
  return { compact, positions };
}

interface Encoder {
  encoding: string;
  apply: (value: string) => string;
}

const ENCODERS: Encoder[] = [
  {
    encoding: "url",
    apply: (value) => encodeURIComponent(value),
  },
  {
    encoding: "url",
    apply: (value) => encodeURI(value),
  },
  {
    encoding: "url",
    apply: (value) =>
      encodeURIComponent(value).replace(/%[0-9A-F]{2}/g, (part) =>
        part.toLowerCase(),
      ),
  },
  {
    encoding: "form",
    apply: (value) => encodeURIComponent(value).replace(/%20/g, "+"),
  },
  {
    encoding: "base64",
    apply: (value) => Buffer.from(value, "utf8").toString("base64"),
  },
  {
    encoding: "base64url",
    apply: (value) =>
      Buffer.from(value, "utf8").toString("base64url"),
  },
  {
    encoding: "base64",
    apply: (value) =>
      Buffer.from(value, "utf8").toString("base64").replace(/=+$/, ""),
  },
  {
    encoding: "hex",
    apply: (value) => Buffer.from(value, "utf8").toString("hex"),
  },
  {
    encoding: "hex",
    apply: (value) =>
      Buffer.from(value, "utf8").toString("hex").toUpperCase(),
  },
  {
    encoding: "unicode-escape",
    apply: (value) => encodeUnicodeEscapes(value, false),
  },
  {
    encoding: "unicode-escape",
    apply: (value) => encodeUnicodeEscapes(value, true),
  },
  {
    encoding: "html",
    apply: (value) => encodeHtmlEntities(value),
  },
  {
    encoding: "json-escape",
    apply: (value) => encodeJsonEscapes(value),
  },
];

function generateVariants(
  value: string,
  options: ResolvedDetection,
): Map<string, VariantEntry> {
  const variants = new Map<string, VariantEntry>();

  const add = (text: string, encoding: string): boolean => {
    if (text.length === 0 || text === value) {
      return false;
    }
    if (text.length > MAX_VARIANT_LENGTH) {
      return false;
    }
    const existing = variants.get(text);
    if (existing) {
      if (existing.value.length < value.length) {
        existing.value = value;
        existing.encoding = encoding;
      }
      return false;
    }
    if (variants.size >= MAX_VARIANTS_PER_VALUE) {
      return false;
    }
    variants.set(text, { text, value, encoding });
    return true;
  };

  const special: Array<[string, string]> = [];
  if (options.normalized) {
    const normalized = value.normalize("NFKC");
    if (normalized !== value) special.push([normalized, "normalized"]);
  }
  if (options.reversed) {
    special.push([Array.from(value).reverse().join(""), "reversed"]);
  }
  if (options.caseInsensitive) {
    special.push([value.toLowerCase(), "case"]);
    special.push([value.toUpperCase(), "case"]);
  }

  const frontier: Array<{ text: string; encoding: string; depth: number }> = [
    { text: value, encoding: "raw", depth: 0 },
  ];

  for (const [text, encoding] of special) {
    if (add(text, encoding)) {
      frontier.push({ text, encoding, depth: 0 });
    }
  }

  for (let depth = 1; depth <= options.maxDepth; depth += 1) {
    const next: Array<{ text: string; encoding: string; depth: number }> = [];
    for (const current of frontier) {
      if (current.depth >= depth) continue;
      for (const encoder of ENCODERS) {
        const text = encoder.apply(current.text);
        if (text === current.text) continue;
        const encoding =
          current.encoding === "raw"
            ? encoder.encoding
            : `${current.encoding}>${encoder.encoding}`;
        if (add(text, encoding)) {
          next.push({ text, encoding, depth });
        }
        if (variants.size >= MAX_VARIANTS_PER_VALUE) break;
      }
      if (variants.size >= MAX_VARIANTS_PER_VALUE) break;
    }
    if (next.length === 0 || variants.size >= MAX_VARIANTS_PER_VALUE) break;
    frontier.push(...next);
  }

  return variants;
}

class AhoMatcher {
  private readonly transitions: Array<Map<number, number>>;
  private readonly failures: number[];
  private readonly outputs: number[][];
  private readonly entries: VariantEntry[];

  constructor(entries: VariantEntry[]) {
    this.entries = entries;
    this.transitions = [new Map<number, number>()];
    this.failures = [0];
    this.outputs = [[]];

    for (let entryIndex = 0; entryIndex < entries.length; entryIndex += 1) {
      let node = 0;
      const text = entries[entryIndex].text;
      for (let index = 0; index < text.length; index += 1) {
        const character = text.charCodeAt(index);
        let next = this.transitions[node].get(character);
        if (next === undefined) {
          next = this.transitions.length;
          this.transitions[node].set(character, next);
          this.transitions.push(new Map<number, number>());
          this.failures.push(0);
          this.outputs.push([]);
        }
        node = next;
      }
      this.outputs[node].push(entryIndex);
    }

    const queue: number[] = [];
    for (const child of this.transitions[0].values()) {
      this.failures[child] = 0;
      queue.push(child);
    }

    for (let cursor = 0; cursor < queue.length; cursor += 1) {
      const node = queue[cursor];
      for (const [character, child] of this.transitions[node]) {
        queue.push(child);
        let fallback = this.failures[node];
        while (
          fallback !== 0 &&
          this.transitions[fallback].get(character) === undefined
        ) {
          fallback = this.failures[fallback];
        }
        const candidate = this.transitions[fallback].get(character);
        this.failures[child] = candidate === undefined ? 0 : candidate;
        this.outputs[child].push(...this.outputs[this.failures[child]]);
      }
    }
  }

  forEachMatch(
    text: string,
    callback: (start: number, end: number, entry: VariantEntry) => void,
  ): void {
    let node = 0;
    for (let index = 0; index < text.length; index += 1) {
      const character = text.charCodeAt(index);
      while (
        node !== 0 &&
        this.transitions[node].get(character) === undefined
      ) {
        node = this.failures[node];
      }
      node = this.transitions[node].get(character) ?? 0;
      for (const entryIndex of this.outputs[node]) {
        const entry = this.entries[entryIndex];
        const end = index + 1;
        const start = end - entry.text.length;
        if (start >= 0) callback(start, end, entry);
      }
    }
  }
}

export function buildVariantIndex(
  inputs: Iterable<unknown>,
  detection?: DetectionOptions,
): VariantIndex {
  const options = resolveDetection(detection);
  const values = normalizeSensitiveInputs(inputs);
  const entriesByText = new Map<string, VariantEntry>();
  const longValues: string[] = [];

  for (const value of values) {
    if (value.length > MAX_VARIANT_LENGTH) {
      longValues.push(value);
      continue;
    }
    if (!entriesByText.has(value)) {
      entriesByText.set(value, { text: value, value, encoding: "raw" });
    }
    if (entriesByText.size >= MAX_TOTAL_VARIANTS) {
      continue;
    }
    for (const entry of generateVariants(value, options).values()) {
      if (entriesByText.size >= MAX_TOTAL_VARIANTS) break;
      const existing = entriesByText.get(entry.text);
      if (!existing || existing.value.length < entry.value.length) {
        entriesByText.set(entry.text, entry);
      }
    }
  }

  const entries = [...entriesByText.values()].sort(
    (left, right) => right.text.length - left.text.length,
  );

  const compactEntries = new Map<string, VariantEntry>();
  let compactDiffersFromRaw = false;
  if (options.separated) {
    for (const value of values) {
      const compact = value.replace(SEPARATOR_GLOBAL_PATTERN, "");
      if (compact.length === 0) continue;
      if (compact !== value) compactDiffersFromRaw = true;
      const existing = compactEntries.get(compact);
      if (!existing || existing.value.length < value.length) {
        compactEntries.set(compact, {
          text: compact,
          value,
          encoding: "compact",
        });
      }
    }
  }

  return {
    values,
    minLength: options.minLength,
    options,
    entries,
    longValues,
    matcher: new AhoMatcher(entries),
    compactMatcher: new AhoMatcher([...compactEntries.values()]),
    compactDiffersFromRaw,
  };
}

function addSpan(spans: SensitiveSpan[], span: SensitiveSpan): void {
  if (span.start < 0 || span.end <= span.start) return;
  spans.push(span);
}

function containsRawValue(
  text: string,
  index: VariantIndex,
): string | undefined {
  for (const value of index.values) {
    if (value.length < index.minLength && text !== value) {
      continue;
    }
    if (text.includes(value)) return value;
  }
  return undefined;
}

function containsConfiguredTransform(
  text: string,
  index: VariantIndex,
): string | undefined {
  if (index.options.normalized) {
    const normalized = text.normalize("NFKC");
    if (normalized !== text) {
      const value = containsRawValue(normalized, index);
      if (value) return value;
    }
  }
  if (index.options.reversed) {
    const reversed = Array.from(text).reverse().join("");
    if (reversed !== text) {
      const value = containsRawValue(reversed, index);
      if (value) return value;
    }
  }
  if (index.options.caseInsensitive) {
    const lowered = text.toLowerCase();
    for (const value of index.values) {
      if (value.length < index.minLength) continue;
      if (lowered.includes(value.toLowerCase())) return value;
    }
  }
  return undefined;
}

function decodedLeak(
  text: string,
  index: VariantIndex,
  depth: number,
  seen: Set<string>,
): { value: string; encoding: string } | undefined {
  const direct = containsRawValue(text, index);
  if (direct) return { value: direct, encoding: "raw" };
  const transformed = containsConfiguredTransform(text, index);
  if (transformed) return { value: transformed, encoding: "transformed" };

  if (depth >= index.options.maxDepth || seen.has(text)) {
    return undefined;
  }
  seen.add(text);

  const decoders: Array<{
    encoding: string;
    decode: (value: string) => string | undefined;
    eligible: (value: string) => boolean;
  }> = [];

  if (index.options.percent) {
    decoders.push({
      encoding: "url",
      decode: safeDecodeURIComponent,
      eligible: (value) => value.includes("%"),
    });
    decoders.push({
      encoding: "form",
      decode: decodeForm,
      eligible: (value) => value.includes("%") || value.includes("+"),
    });
  }
  if (index.options.base64) {
    decoders.push({
      encoding: "base64",
      decode: decodeBase64,
      eligible: (value) =>
        value.length >= 8 && /^[A-Za-z0-9+/_-]+={0,2}$/.test(value),
    });
  }
  if (index.options.hex) {
    decoders.push({
      encoding: "hex",
      decode: decodeHex,
      eligible: (value) =>
        value.length >= 8 && /^(?:[0-9A-Fa-f]{2})+$/.test(value),
    });
  }
  if (index.options.unicodeEscapes) {
    decoders.push({
      encoding: "unicode-escape",
      decode: decodeUnicodeEscapes,
      eligible: (value) => value.includes("\\u") || value.includes("\\x"),
    });
  }
  if (index.options.htmlEntities) {
    decoders.push({
      encoding: "html",
      decode: decodeHtmlEntities,
      eligible: (value) => value.includes("&"),
    });
  }

  for (const decoder of decoders) {
    if (!decoder.eligible(text)) continue;
    const decoded = decoder.decode(text);
    if (!decoded || decoded === text) continue;
    const nested = decodedLeak(decoded, index, depth + 1, seen);
    if (nested) {
      return {
        value: nested.value,
        encoding:
          nested.encoding === "raw"
            ? decoder.encoding
            : `${decoder.encoding}>${nested.encoding}`,
      };
    }
  }

  seen.delete(text);
  return undefined;
}

function addDecodedRunSpans(
  text: string,
  index: VariantIndex,
  spans: SensitiveSpan[],
  wholeTextTransforms: boolean,
): void {
  const options = index.options;
  let candidates = 0;

  const testRun = (
    start: number,
    end: number,
    encoding: string,
    decode: (value: string) => string | undefined,
  ): void => {
    if (candidates >= MAX_DECODE_CANDIDATES) return;
    candidates += 1;
    const decoded = decode(text.slice(start, end));
    if (!decoded) return;
    const leak = decodedLeak(decoded, index, 1, new Set<string>());
    if (leak) {
      addSpan(spans, {
        start,
        end,
        value: leak.value,
        encoding:
          leak.encoding === "raw" ? encoding : `${encoding}>${leak.encoding}`,
      });
    }
  };

  if (options.percent) {
    const percentRuns = /(?:%[0-9A-Fa-f]{2})+/g;
    for (const match of text.matchAll(percentRuns)) {
      testRun(match.index!, match.index! + match[0].length, "url", (value) =>
        safeDecodeURIComponent(value),
      );
    }
    if (wholeTextTransforms && text.includes("%")) {
      testRun(0, text.length, "url", safeDecodeURIComponent);
      testRun(0, text.length, "form", decodeForm);
    }
  }

  if (options.base64) {
    const base64Runs = /[A-Za-z0-9+/_-]{8,}={0,2}/g;
    for (const match of text.matchAll(base64Runs)) {
      if (match[0].length < 8) continue;
      testRun(match.index!, match.index! + match[0].length, "base64", decodeBase64);
    }
    if (wholeTextTransforms) {
      const compact = text.replace(/\s+/g, "");
      if (compact !== text) {
        const compactRuns = /[A-Za-z0-9+/_-]{8,}={0,2}/g;
        for (const match of compact.matchAll(compactRuns)) {
          const decoded = decodeBase64(match[0]);
          if (!decoded) continue;
          const leak = decodedLeak(decoded, index, 1, new Set<string>());
          if (leak) {
            addSpan(spans, {
              start: 0,
              end: text.length,
              value: leak.value,
              encoding: "base64",
            });
            break;
          }
        }
      }
    }
  }

  if (options.hex) {
    const hexRuns = /(?:[0-9A-Fa-f]{2}){4,}/g;
    for (const match of text.matchAll(hexRuns)) {
      testRun(match.index!, match.index! + match[0].length, "hex", decodeHex);
    }
    if (wholeTextTransforms) {
      const compact = text.replace(/[\s:]+/g, "");
      if (compact !== text) {
        const compactRuns = /(?:[0-9A-Fa-f]{2}){4,}/g;
        for (const match of compact.matchAll(compactRuns)) {
          const decoded = decodeHex(match[0]);
          if (!decoded) continue;
          const leak = decodedLeak(decoded, index, 1, new Set<string>());
          if (leak) {
            addSpan(spans, {
              start: 0,
              end: text.length,
              value: leak.value,
              encoding: "hex",
            });
            break;
          }
        }
      }
    }
  }

  if (options.unicodeEscapes) {
    const unicodeRuns = /(?:\\u[0-9A-Fa-f]{4}|\\x[0-9A-Fa-f]{2})+/g;
    for (const match of text.matchAll(unicodeRuns)) {
      testRun(
        match.index!,
        match.index! + match[0].length,
        "unicode-escape",
        decodeUnicodeEscapes,
      );
    }
    if (
      wholeTextTransforms &&
      (text.includes("\\u") || text.includes("\\x"))
    ) {
      testRun(
        0,
        text.length,
        "unicode-escape",
        decodeUnicodeEscapes,
      );
    }
  }

  if (options.htmlEntities) {
    const htmlRuns = /(?:&#x[0-9A-Fa-f]+;|&#\d+;|&[A-Za-z]{2,8};)+/g;
    for (const match of text.matchAll(htmlRuns)) {
      testRun(
        match.index!,
        match.index! + match[0].length,
        "html",
        decodeHtmlEntities,
      );
    }
    if (wholeTextTransforms && text.includes("&")) {
      testRun(0, text.length, "html", decodeHtmlEntities);
    }
  }
}

function normalizeSpan(spans: SensitiveSpan[]): SensitiveSpan[] {
  if (spans.length <= 1) return spans;
  spans.sort((left, right) => {
    if (left.start !== right.start) return left.start - right.start;
    if (left.end !== right.end) return right.end - left.end;
    if (left.encoding === "raw" && right.encoding !== "raw") return -1;
    if (right.encoding === "raw" && left.encoding !== "raw") return 1;
    return 0;
  });

  const merged: SensitiveSpan[] = [];
  for (const span of spans) {
    const previous = merged[merged.length - 1];
    if (!previous || span.start >= previous.end) {
      merged.push({ ...span });
      continue;
    }
    if (span.end > previous.end) {
      previous.end = span.end;
    }
    if (span.value.length > previous.value.length) {
      previous.value = span.value;
      previous.encoding = span.encoding;
    } else if (
      span.value.length === previous.value.length &&
      previous.encoding !== "raw" &&
      span.encoding === "raw"
    ) {
      previous.encoding = "raw";
    }
  }
  return merged;
}

export function findSensitiveSpans(
  text: string,
  index: VariantIndex,
  options: { wholeTextTransforms?: boolean } = {},
): SensitiveSpan[] {
  if (text.length === 0 || index.values.length === 0) {
    return [];
  }

  const spans: SensitiveSpan[] = [];

  for (const value of index.longValues) {
    let cursor = text.indexOf(value);
    while (cursor !== -1) {
      addSpan(spans, {
        start: cursor,
        end: cursor + value.length,
        value,
        encoding: "raw",
      });
      cursor = text.indexOf(value, cursor + 1);
    }
  }

  index.matcher.forEachMatch(text, (start, end, entry) => {
    const coversWhole = start === 0 && end === text.length;
    if (entry.text.length < index.minLength && !coversWhole) {
      return;
    }
    addSpan(spans, {
      start,
      end,
      value: entry.value,
      encoding: entry.encoding,
    });
  });

  if (index.options.separated) {
    const stripped = stripSeparators(text);
    if (
      stripped.compact.length > 0 &&
      (stripped.compact !== text || index.compactDiffersFromRaw)
    ) {
      index.compactMatcher.forEachMatch(
        stripped.compact,
        (start, end, entry) => {
          const coversWhole =
            start === 0 && end === stripped.compact.length;
          if (entry.text.length < index.minLength && !coversWhole) {
            return;
          }
          const originalStart = stripped.positions[start];
          const originalEnd = stripped.positions[end - 1] + 1;
          addSpan(spans, {
            start: originalStart,
            end: originalEnd,
            value: entry.value,
            encoding: "separated",
          });
        },
      );
    }
  }

  const wholeTextTransforms = options.wholeTextTransforms ?? true;
  const addWholeTransform = (
    encoding: string,
    transformed: string,
    transformValue: (value: string) => string = (value) => value,
  ): void => {
    if (transformed === text) return;
    if (transformed.length === text.length) {
      for (const value of index.values) {
        const needle = transformValue(value);
        if (needle.length < index.minLength) continue;
        let cursor = transformed.indexOf(needle);
        while (cursor !== -1) {
          addSpan(spans, {
            start: cursor,
            end: cursor + needle.length,
            value,
            encoding,
          });
          cursor = transformed.indexOf(needle, cursor + 1);
        }
      }
      return;
    }
    for (const value of index.values) {
      const needle = transformValue(value);
      if (needle.length < index.minLength) continue;
      if (transformed.includes(needle)) {
        addSpan(spans, {
          start: 0,
          end: text.length,
          value,
          encoding,
        });
        break;
      }
    }
  };

  if (wholeTextTransforms && index.options.normalized) {
    addWholeTransform(
      "normalized",
      text.normalize("NFKC"),
      (value) => value.normalize("NFKC"),
    );
  }
  if (wholeTextTransforms && index.options.caseInsensitive) {
    addWholeTransform(
      "case",
      text.toLowerCase(),
      (value) => value.toLowerCase(),
    );
  }
  if (wholeTextTransforms && index.options.reversed) {
    const reversed = Array.from(text).reverse().join("");
    for (const value of index.values) {
      if (value.length < index.minLength) continue;
      const position = reversed.indexOf(value);
      if (position === -1) continue;
      const start = text.length - (position + value.length);
      addSpan(spans, {
        start,
        end: text.length - position,
        value,
        encoding: "reversed",
      });
    }
  }

  if (
    index.options.percent ||
    index.options.base64 ||
    index.options.hex ||
    index.options.unicodeEscapes ||
    index.options.htmlEntities
  ) {
    addDecodedRunSpans(text, index, spans, wholeTextTransforms);
  }

  return normalizeSpan(spans);
}

export function findLeaksInText(
  text: string,
  index: VariantIndex,
): Leak[] {
  return findSensitiveSpans(text, index)
    .slice(0, 100)
    .map((span) => ({
      value: span.value,
      encoding: span.encoding,
      index: span.start,
      location: "text" as const,
    }));
}

interface TextUnit {
  text: string;
  path: string;
}

function flattenUnits(
  value: unknown,
  path: string,
  units: TextUnit[],
): void {
  if (value === null || value === undefined) return;
  const type = typeof value;
  if (type === "string" || type === "number" || type === "bigint" || type === "boolean") {
    units.push({ text: String(value), path });
    return;
  }
  if (type !== "object") return;
  if (Array.isArray(value)) {
    value.forEach((item, index) =>
      flattenUnits(item, `${path}/${index}`, units),
    );
    return;
  }
  for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
    flattenUnits(item, `${path}/${key.replace(/~/g, "~0").replace(/\//g, "~1")}`, units);
  }
}

function detectSplitLeaks(
  value: unknown,
  index: VariantIndex,
): Leak[] {
  if (!index.options.splitAcrossFields) return [];
  const roots: Array<{ value: unknown; path: string }> = [];
  if (Array.isArray(value)) {
    roots.push({ value, path: "" });
    value.forEach((item, position) =>
      roots.push({ value: item, path: `/${position}` }),
    );
  } else {
    roots.push({ value, path: "" });
  }

  const leaks: Leak[] = [];
  for (const root of roots) {
    const units: TextUnit[] = [];
    flattenUnits(root.value, root.path, units);
    if (units.length < 2) continue;
    const combined = units.map((unit) => unit.text).join("");
    if (combined.length === 0) continue;

    const offsets: number[] = [];
    let offset = 0;
    for (const unit of units) {
      offsets.push(offset);
      offset += unit.text.length;
    }

    for (const span of findSensitiveSpans(combined, index, {
      wholeTextTransforms: false,
    })) {
      let first = -1;
      let last = -1;
      for (let unitIndex = 0; unitIndex < units.length; unitIndex += 1) {
        const start = offsets[unitIndex];
        const end = start + units[unitIndex].text.length;
        if (end <= span.start) continue;
        if (start >= span.end) break;
        if (first === -1) first = unitIndex;
        last = unitIndex;
      }
      if (first !== -1 && last > first) {
        leaks.push({
          value: span.value,
          encoding: `split>${span.encoding}`,
          index: span.start,
          location: "text",
          path: units[first].path,
        });
      }
    }
  }
  return leaks;
}

export function findLeaksInValue(
  value: unknown,
  index: VariantIndex,
  rootPath = "",
): Leak[] {
  const leaks: Leak[] = [];

  const walk = (current: unknown, path: string): void => {
    if (current === null || current === undefined) return;
    const type = typeof current;
    if (type === "string" || type === "number" || type === "bigint" || type === "boolean") {
      for (const leak of findLeaksInText(String(current), index)) {
        leaks.push({ ...leak, path });
      }
      return;
    }
    if (type !== "object") return;
    if (Array.isArray(current)) {
      current.forEach((item, position) =>
        walk(item, `${path}/${position}`),
      );
      return;
    }
    for (const [key, item] of Object.entries(current as Record<string, unknown>)) {
      for (const leak of findLeaksInText(key, index)) {
        leaks.push({ ...leak, location: "key", path });
      }
      walk(
        item,
        `${path}/${key.replace(/~/g, "~0").replace(/\//g, "~1")}`,
      );
    }
  };

  walk(value, rootPath);
  leaks.push(...detectSplitLeaks(value, index));
  return leaks;
}

function dedupeLeaks(leaks: Leak[]): Leak[] {
  const seen = new Set<string>();
  const output: Leak[] = [];
  for (const leak of leaks) {
    const key = `${leak.value}\u0000${leak.encoding}\u0000${leak.path ?? ""}\u0000${leak.index}`;
    if (seen.has(key)) continue;
    seen.add(key);
    output.push(leak);
  }
  return output;
}

export function findLeaksWithIndex(
  payload: string | unknown,
  index: VariantIndex,
): Leak[] {
  const leaks: Leak[] = [];
  const serialized = typeof payload === "string" ? payload : undefined;
  if (serialized !== undefined) {
    leaks.push(...findLeaksInText(serialized, index));
    try {
      const parsed = JSON.parse(serialized) as unknown;
      leaks.push(...findLeaksInValue(parsed, index));
    } catch {
      // Not JSON; the raw text scan above is the best available check.
    }
  } else {
    leaks.push(...findLeaksInValue(payload, index));
  }
  return dedupeLeaks(leaks);
}

export function findLeaks(
  payload: string | unknown,
  sensitiveValues: Iterable<unknown>,
  detection?: DetectionOptions,
): Leak[] {
  const index = buildVariantIndex(sensitiveValues, detection);
  return findLeaksWithIndex(payload, index);
}

export function containsLeak(
  payload: string | unknown,
  sensitiveValues: Iterable<unknown>,
  detection?: DetectionOptions,
): boolean {
  return findLeaks(payload, sensitiveValues, detection).length > 0;
}

export function assertNoLeak(
  payload: string | unknown,
  sensitiveValues: Iterable<unknown>,
  detection?: DetectionOptions,
): void {
  const leaks = findLeaks(payload, sensitiveValues, detection);
  if (leaks.length > 0) {
    throw new DesensitizationLeakError(leaks);
  }
}
