export interface SanitizeUrlOptions {
  /** Protocols (with or without a trailing colon) that absolute URLs may use. */
  allowedProtocols?: readonly string[];
  /** Relative URLs are accepted by default. */
  allowRelative?: boolean;
  /** Optional base used only to validate protocol-relative and root-relative URLs. */
  baseUrl?: string;
}

const DEFAULT_PROTOCOLS = ["http", "https"] as const;

const DANGEROUS_SCHEMES = new Set([
  "javascript",
  "data",
  "vbscript",
  "livescript",
  "mocha",
  "file",
  "blob",
  "filesystem",
  "about",
  "chrome",
  "chrome-extension",
  "moz-extension",
  "jar",
  "view-source",
  "resource",
]);

const NAMED_ENTITIES: Readonly<Record<string, string>> = {
  amp: "&",
  AMP: "&",
  lt: "<",
  LT: "<",
  gt: ">",
  GT: ">",
  quot: "\"",
  QUOT: "\"",
  apos: "'",
  colon: ":",
  semi: ";",
  Tab: "\t",
  NewLine: "\n",
  nbsp: "\u00a0",
  sol: "/",
  bsol: "\\",
  lpar: "(",
  rpar: ")",
  num: "#",
  dollar: "$",
  percnt: "%",
  ast: "*",
  midast: "*",
  plus: "+",
  comma: ",",
  period: ".",
  hyphen: "-",
  under: "_",
  equals: "=",
  quest: "?",
  commat: "@",
  excl: "!",
  grave: "`",
  lsqb: "[",
  rsqb: "]",
  lcub: "{",
  rcub: "}",
  vert: "|",
  bverbar: "|",
  copy: "\u00a9",
  reg: "\u00ae",
  trade: "\u2122",
  hellip: "\u2026",
  mdash: "\u2014",
  ndash: "\u2013",
  lsquo: "\u2018",
  rsquo: "\u2019",
  ldquo: "\u201c",
  rdquo: "\u201d",
  laquo: "\u00ab",
  raquo: "\u00bb",
  shy: "\u00ad",
  zwj: "\u200d",
  zwnj: "\u200c",
  ZeroWidthSpace: "\u200b",
  NoBreak: "\u2060",
  InvisibleComma: "\u2063",
  InvisibleTimes: "\u2062",
  lrm: "\u200e",
  rlm: "\u200f",
  times: "\u00d7",
  divide: "\u00f7",
  plusmn: "\u00b1",
  micro: "\u00b5",
  para: "\u00b6",
  sect: "\u00a7",
  deg: "\u00b0",
};

const C1_REPLACEMENTS: Readonly<Record<number, number>> = {
  0x80: 0x20ac,
  0x82: 0x201a,
  0x83: 0x0192,
  0x84: 0x201e,
  0x85: 0x2026,
  0x86: 0x2020,
  0x87: 0x2021,
  0x88: 0x02c6,
  0x89: 0x2030,
  0x8a: 0x0160,
  0x8b: 0x2039,
  0x8c: 0x0152,
  0x8e: 0x017d,
  0x91: 0x2018,
  0x92: 0x2019,
  0x93: 0x201c,
  0x94: 0x201d,
  0x95: 0x2022,
  0x96: 0x2013,
  0x97: 0x2014,
  0x98: 0x02dc,
  0x99: 0x2122,
  0x9a: 0x0161,
  0x9b: 0x203a,
  0x9c: 0x0153,
  0x9e: 0x017e,
  0x9f: 0x0178,
};

function codePointToString(value: number): string {
  if (
    value === 0 ||
    Number.isNaN(value) ||
    value > 0x10ffff ||
    (value >= 0xd800 && value <= 0xdfff)
  ) {
    return "\ufffd";
  }
  const mapped = C1_REPLACEMENTS[value] ?? value;
  return String.fromCodePoint(mapped);
}

/**
 * Decode one HTML character-reference pass. A trailing semicolon is accepted
 * as optional for numeric references and for known named references, which is
 * deliberately a little more permissive than an HTML parser: it lets callers
 * inspect the eventual URL rather than relying on browser error recovery.
 */
export function decodeHtmlEntitiesOnce(input: string): string {
  if (!input.includes("&")) return input;

  return input
    .replace(/&#(?:[xX]([0-9a-fA-F]+)|([0-9]+));?/g, (_match, hex, decimal) => {
      const value = hex !== undefined ? Number.parseInt(hex, 16) : Number.parseInt(decimal, 10);
      return codePointToString(value);
    })
    .replace(/&([A-Za-z][A-Za-z0-9]{1,31});?/g, (match, name) => {
      const decoded = NAMED_ENTITIES[name];
      return decoded === undefined ? match : decoded;
    });
}

/** Decode entities repeatedly up to a fixed bound to defeat nesting tricks. */
export function decodeHtmlEntitiesDeep(input: string, maxPasses = 8): string {
  let value = input;
  for (let pass = 0; pass < maxPasses; pass++) {
    const next = decodeHtmlEntitiesOnce(value);
    if (next === value) break;
    value = next;
  }
  return value;
}

function decodePercentOnce(input: string): string {
  return input.replace(/%([0-9a-fA-F]{2})/g, (_match, hex: string) =>
    String.fromCharCode(Number.parseInt(hex, 16)),
  );
}

function decodePercentDeep(input: string, maxPasses = 6): string {
  let value = input;
  for (let pass = 0; pass < maxPasses; pass++) {
    const next = decodePercentOnce(value);
    if (next === value) break;
    value = next;
  }
  return value;
}

/** WHATWG URL parsers remove tabs and line breaks from the input. */
function removeUrlTabsAndNewlines(input: string): string {
  return input.replace(/[\t\n\r]/g, "");
}

/** WHATWG URL parsers trim leading/trailing C0 controls and spaces. */
function trimUrlC0AndSpace(input: string): string {
  return input.replace(/^[\u0000-\u0020]+|[\u0000-\u0020]+$/g, "");
}

function hasForbiddenControlCharacter(input: string): boolean {
  return /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/u.test(input);
}

function isForbiddenCodePoint(value: number): boolean {
  return value === 0 || value <= 0x1f || (value >= 0x7f && value <= 0x9f);
}

function hasForbiddenNumericReference(input: string): boolean {
  const pattern = /&#(?:[xX]([0-9a-fA-F]+)|([0-9]+));?/g;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(input)) !== null) {
    const value =
      match[1] !== undefined
        ? Number.parseInt(match[1], 16)
        : Number.parseInt(match[2], 10);
    if (isForbiddenCodePoint(value)) return true;
  }
  return false;
}

/**
 * Decode URL character references while checking every intermediate layer.
 * This prevents nested references such as `&amp;#0;` or `&amp;#x6a;` from
 * hiding a control character or a script scheme from the final inspection.
 */
function decodeUrlEntitiesDeep(input: string): string | null {
  let value = input;
  for (let pass = 0; pass < 8; pass++) {
    if (hasForbiddenNumericReference(value)) return null;
    const next = decodeHtmlEntitiesOnce(value);
    if (next === value) return value;
    value = next;
  }
  return hasForbiddenNumericReference(value) ? null : value;
}

function explicitScheme(input: string): string | null {
  const match = /^([A-Za-z][A-Za-z0-9+.-]*):/.exec(input);
  return match ? match[1].toLowerCase() : null;
}

/**
 * Inspect a URL after repeatedly applying character-reference, percent, and
 * browser whitespace normalization. This catches `%6a...`, `&#x6a;...`,
 * `java&Tab;script:`, and similar variants.
 */
function inspectScheme(input: string): string | null {
  let value = input;
  for (let pass = 0; pass < 6; pass++) {
    const next = decodeHtmlEntitiesDeep(decodePercentDeep(value, 4), 4);
    if (next === value) break;
    value = next;
  }
  // For the dangerous-scheme probe only, remove whitespace and invisible
  // formatting characters anywhere in the string. Browsers generally treat
  // `java script:` as a relative path, but rejecting it as well is safer and
  // avoids relying on that subtle distinction.
  value = trimUrlC0AndSpace(
    value.replace(
      /[\s\u0000-\u001f\u007f-\u009f\u200b-\u200f\u202a-\u202e\u2060-\u206f\ufeff]+/g,
      "",
    ),
  );
  return explicitScheme(value);
}

function normalizeAllowedProtocols(options: SanitizeUrlOptions): Set<string> {
  const source = options.allowedProtocols ?? DEFAULT_PROTOCOLS;
  const protocols = new Set<string>();
  for (const protocol of source) {
    protocols.add(protocol.replace(/:$/, "").toLowerCase());
  }
  // Script-capable schemes are never accepted, even if a caller accidentally
  // adds them to the configured allow-list.
  for (const dangerous of DANGEROUS_SCHEMES) protocols.delete(dangerous);
  return protocols;
}

function normalizeRootRelative(input: string, baseUrl?: string): string | null {
  try {
    const base = baseUrl ?? "https://relative.invalid/";
    const parsed = new URL(input, base);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null;
    return parsed.pathname + parsed.search + parsed.hash;
  } catch {
    return null;
  }
}

function normalizeProtocolRelative(input: string, baseUrl?: string): string | null {
  try {
    const base = baseUrl ?? "https://relative.invalid/";
    const parsed = new URL(input, base);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null;
    return `//${parsed.host}${parsed.pathname}${parsed.search}${parsed.hash}`;
  } catch {
    return null;
  }
}

/**
 * Return a safe URL for an href/src attribute, or null when the value must be
 * dropped. Absolute URLs are limited to http(s); relative references are kept.
 * URL inspection follows browser normalization (entity decoding, tab/newline
 * removal, C0 trimming, WHATWG scheme parsing) before accepting a value.
 */
export function sanitizeUrl(
  input: unknown,
  options: SanitizeUrlOptions = {},
): string | null {
  if (typeof input !== "string") return null;

  const allowedProtocols = normalizeAllowedProtocols(options);
  const decoded = decodeUrlEntitiesDeep(input);
  if (decoded === null) return null;
  let url = decoded;
  url = trimUrlC0AndSpace(removeUrlTabsAndNewlines(url));
  if (hasForbiddenControlCharacter(url)) return null;

  const scheme = explicitScheme(url);
  if (scheme !== null) {
    if (!allowedProtocols.has(scheme)) return null;
    try {
      const parsed = new URL(url);
      const parsedScheme = parsed.protocol.replace(/:$/, "").toLowerCase();
      if (!allowedProtocols.has(parsedScheme)) return null;
      if (DANGEROUS_SCHEMES.has(parsedScheme)) return null;
      return parsed.href;
    } catch {
      return null;
    }
  }

  const inspected = inspectScheme(url);
  if (inspected !== null && DANGEROUS_SCHEMES.has(inspected)) return null;

  if (options.allowRelative === false) return null;
  if (url === "") return "";

  if (/^[\\/]{2}/.test(url)) {
    return normalizeProtocolRelative(url, options.baseUrl);
  }
  if (url.startsWith("/") || url.startsWith("\\")) {
    return normalizeRootRelative(url, options.baseUrl);
  }

  return url;
}

export function isSafeUrl(input: unknown, options: SanitizeUrlOptions = {}): boolean {
  return sanitizeUrl(input, options) !== null;
}
