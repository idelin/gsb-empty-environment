/**
 * RFC 8941 Structured Field Values: Dictionary parser.
 *
 * Only what the Reporting-Endpoints header needs, but strict: any syntax
 * error fails the whole field value (callers then ignore the header).
 *
 * Duplicate keys: the first occurrence wins, later ones are ignored
 * (RFC 8941 section 3.2). Duplicate parameter names on a member: the
 * first occurrence wins as well (RFC 8941 section 3.1.2).
 */

export type BareItem =
  | { kind: "string"; value: string }
  | { kind: "token"; value: string }
  | { kind: "bytes"; value: Uint8Array }
  | { kind: "boolean"; value: boolean }
  | { kind: "number"; value: number };

export interface DictionaryMember {
  value: BareItem;
  params: Map<string, BareItem>;
}

export type Dictionary = Map<string, DictionaryMember>;

const TRUE: BareItem = { kind: "boolean", value: true };

function isDigit(ch: string | undefined): boolean {
  return ch !== undefined && ch >= "0" && ch <= "9";
}

function isAlpha(ch: string | undefined): boolean {
  return (
    ch !== undefined &&
    ((ch >= "a" && ch <= "z") || (ch >= "A" && ch <= "Z"))
  );
}

function isKeyChar(ch: string | undefined): boolean {
  return (
    ch !== undefined &&
    ((ch >= "a" && ch <= "z") ||
      isDigit(ch) ||
      ch === "_" ||
      ch === "-" ||
      ch === "." ||
      ch === "*")
  );
}

const TOKEN_CHARS = new Set([..."!#$%&'*+-.^_`|~:/"]);

function parseKey(input: string, pos: number): { key: string; next: number } | null {
  const first = input[pos];
  if (first === undefined) return null;
  if (!((first >= "a" && first <= "z") || first === "*")) return null;
  let i = pos + 1;
  while (i < input.length && isKeyChar(input[i])) i++;
  return { key: input.slice(pos, i), next: i };
}

function parseString(input: string, pos: number): { value: string; next: number } | null {
  // input[pos] === '"'
  let i = pos + 1;
  let out = "";
  while (i < input.length) {
    const ch = input[i];
    const code = ch.charCodeAt(0);
    if (ch === '"') return { value: out, next: i + 1 };
    if (ch === "\\") {
      const next = input[i + 1];
      if (next !== '"' && next !== "\\") return null;
      out += next;
      i += 2;
      continue;
    }
    if (code < 0x20 || code > 0x7e) return null;
    out += ch;
    i++;
  }
  return null;
}

function parseNumber(input: string, pos: number): { value: number; next: number } | null {
  let i = pos;
  let negative = false;
  if (input[i] === "-") {
    negative = true;
    i++;
  }
  let intPart = "";
  while (isDigit(input[i])) {
    intPart += input[i];
    i++;
  }
  if (intPart.length === 0) return null;
  if (input[i] === ".") {
    if (intPart.length > 12) return null;
    i++;
    let frac = "";
    while (isDigit(input[i])) {
      frac += input[i];
      i++;
    }
    if (frac.length < 1 || frac.length > 3) return null;
    const value = Number((negative ? "-" : "") + intPart + "." + frac);
    return { value, next: i };
  }
  if (intPart.length > 15) return null;
  const value = Number(intPart) * (negative ? -1 : 1);
  return { value, next: i };
}

function parseToken(input: string, pos: number): { value: string; next: number } | null {
  const first = input[pos];
  if (!(isAlpha(first) || first === "*")) return null;
  let i = pos + 1;
  while (i < input.length) {
    const ch = input[i];
    if (isAlpha(ch) || isDigit(ch) || TOKEN_CHARS.has(ch)) {
      i++;
    } else {
      break;
    }
  }
  return { value: input.slice(pos, i), next: i };
}

function parseBytes(input: string, pos: number): { value: Uint8Array; next: number } | null {
  // input[pos] === ':'
  const end = input.indexOf(":", pos + 1);
  if (end === -1) return null;
  const body = input.slice(pos + 1, end);
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(body)) return null;
  return { value: Uint8Array.from(atob(body), (c) => c.charCodeAt(0)), next: end + 1 };
}

function parseBareItem(input: string, pos: number): { item: BareItem; next: number } | null {
  const ch = input[pos];
  if (ch === undefined) return null;
  if (ch === '"') {
    const r = parseString(input, pos);
    return r === null ? null : { item: { kind: "string", value: r.value }, next: r.next };
  }
  if (ch === "-" || isDigit(ch)) {
    const r = parseNumber(input, pos);
    return r === null ? null : { item: { kind: "number", value: r.value }, next: r.next };
  }
  if (isAlpha(ch) || ch === "*") {
    const r = parseToken(input, pos);
    return r === null ? null : { item: { kind: "token", value: r.value }, next: r.next };
  }
  if (ch === ":") {
    const r = parseBytes(input, pos);
    return r === null ? null : { item: { kind: "bytes", value: r.value }, next: r.next };
  }
  if (ch === "?") {
    const b = input[pos + 1];
    if (b === "1") return { item: TRUE, next: pos + 2 };
    if (b === "0") return { item: { kind: "boolean", value: false }, next: pos + 2 };
    return null;
  }
  return null;
}

function parseParameters(
  input: string,
  pos: number,
): { params: Map<string, BareItem>; next: number } | null {
  const params = new Map<string, BareItem>();
  let i = pos;
  while (input[i] === ";") {
    i++;
    while (input[i] === " ") i++;
    const k = parseKey(input, i);
    if (k === null) return null;
    i = k.next;
    let value: BareItem = TRUE;
    if (input[i] === "=") {
      const bare = parseBareItem(input, i + 1);
      if (bare === null) return null;
      value = bare.item;
      i = bare.next;
    }
    // Duplicate parameter names: first occurrence wins.
    if (!params.has(k.key)) params.set(k.key, value);
  }
  return { params, next: i };
}

function parseItem(
  input: string,
  pos: number,
): { value: BareItem; params: Map<string, BareItem>; next: number } | null {
  const bare = parseBareItem(input, pos);
  if (bare === null) return null;
  const p = parseParameters(input, bare.next);
  if (p === null) return null;
  return { value: bare.item, params: p.params, next: p.next };
}

/**
 * Parse one or more already-combined field values as an RFC 8941
 * Dictionary. Returns null if the input is not a valid dictionary; the
 * caller must then ignore the entire header.
 */
export function parseDictionary(input: string): Dictionary | null {
  const dict: Dictionary = new Map();
  let i = 0;
  while (input[i] === " ") i++;
  while (i < input.length) {
    const k = parseKey(input, i);
    if (k === null) return null;
    i = k.next;
    let value: BareItem = TRUE;
    let params = new Map<string, BareItem>();
    if (input[i] === "=") {
      const item = parseItem(input, i + 1);
      if (item === null) return null;
      value = item.value;
      params = item.params;
      i = item.next;
    } else {
      const p = parseParameters(input, i);
      if (p === null) return null;
      params = p.params;
      i = p.next;
    }
    // Duplicate keys: first occurrence wins.
    if (!dict.has(k.key)) dict.set(k.key, { value, params });
    while (input[i] === " " || input[i] === "\t") i++;
    if (i >= input.length) break;
    if (input[i] !== ",") return null;
    i++;
    while (input[i] === " " || input[i] === "\t") i++;
    if (i >= input.length) return null; // trailing comma
  }
  return dict;
}

/**
 * Combine multiple Reporting-Endpoints (or any dictionary) header field
 * values into one, per RFC 9110 list-based field semantics, then parse.
 */
export function parseDictionaryHeaders(fieldValues: string[]): Dictionary | null {
  return parseDictionary(fieldValues.join(","));
}
