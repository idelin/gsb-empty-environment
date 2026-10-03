import { Bitmap } from "./bitmap.ts";
import { StringColumn } from "./string-column.ts";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/**
 * Vectorized StartsWith: compares the prefix bytes directly against the
 * column's compact byte buffer, row by row, without decoding any string.
 * Emits a compressed bitmap (1 bit per row).
 */
export function startsWith(column: StringColumn, prefix: string | Uint8Array): Bitmap {
  const needle = typeof prefix === "string" ? encoder.encode(prefix) : prefix;
  const needleLen = needle.length;
  const { bytes, offsets, length } = column;
  const out = new Bitmap(length);
  const words = out.words;

  for (let row = 0; row < length; row++) {
    const start = offsets[row];
    if (offsets[row + 1] - start < needleLen) continue;
    let matched = true;
    for (let k = 0; k < needleLen; k++) {
      if (bytes[start + k] !== needle[k]) {
        matched = false;
        break;
      }
    }
    if (matched) words[row >>> 5] |= 1 << (row & 31);
  }
  return out;
}

/** Vectorized equality on raw bytes (no string decode). */
export function equals(column: StringColumn, value: string): Bitmap {
  const needle = encoder.encode(value);
  const needleLen = needle.length;
  const { bytes, offsets, length } = column;
  const out = new Bitmap(length);
  const words = out.words;

  for (let row = 0; row < length; row++) {
    const start = offsets[row];
    if (offsets[row + 1] - start !== needleLen) continue;
    let matched = true;
    for (let k = 0; k < needleLen; k++) {
      if (bytes[start + k] !== needle[k]) {
        matched = false;
        break;
      }
    }
    if (matched) words[row >>> 5] |= 1 << (row & 31);
  }
  return out;
}

type LikePlan =
  | { kind: "prefix"; literal: string }
  | { kind: "exact"; literal: string }
  | { kind: "regex"; regex: RegExp };

/**
 * Compile a SQL LIKE pattern (`%` = any run, `_` = any single char) into an
 * execution plan. Pure-prefix patterns (e.g. 'https://api.%') and wildcard-free
 * patterns take the vectorized byte-compare fast path; anything else falls
 * back to a lazily-decoded regex scan.
 */
function compileLike(pattern: string): LikePlan {
  const wildcard = /[%_]/;
  if (pattern.endsWith("%")) {
    const literal = pattern.slice(0, -1);
    if (!wildcard.test(literal)) return { kind: "prefix", literal };
  }
  if (!wildcard.test(pattern)) return { kind: "exact", literal: pattern };

  let source = "^";
  for (const ch of pattern) {
    if (ch === "%") source += ".*";
    else if (ch === "_") source += ".";
    else source += ch.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }
  source += "$";
  return { kind: "regex", regex: new RegExp(source, "su") };
}

/** Batch SQL LIKE filter over the whole column; returns a compressed bitmap. */
export function like(column: StringColumn, pattern: string): Bitmap {
  const plan = compileLike(pattern);
  switch (plan.kind) {
    case "prefix":
      return startsWith(column, plan.literal);
    case "exact":
      return equals(column, plan.literal);
    case "regex":
      return matchRegex(column, plan.regex);
  }
}

/**
 * General regex filter. Rows are decoded lazily, one at a time, straight
 * from the compact buffer; results still land in a compressed bitmap.
 */
export function matchRegex(column: StringColumn, regex: RegExp): Bitmap {
  const { bytes, offsets, length } = column;
  const out = new Bitmap(length);
  const words = out.words;
  const re = regex.global
    ? new RegExp(regex.source, regex.flags.replace("g", ""))
    : regex;

  for (let row = 0; row < length; row++) {
    const value = decoder.decode(bytes.subarray(offsets[row], offsets[row + 1]));
    if (re.test(value)) words[row >>> 5] |= 1 << (row & 31);
  }
  return out;
}
