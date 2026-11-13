import { decodePunycode, encodePunycode } from "./punycode.ts";

export type IdnRejectionReason =
  | "invalid-hostname"
  | "invisible-character"
  | "leading-combining-mark"
  | "mixed-script"
  | "cyrillic-confusable";

export interface IdnDisplayAnalysis {
  readonly input: string;
  readonly ascii: string;
  readonly unicode: string;
  readonly display: string;
  readonly safeForUnicode: boolean;
  readonly rejected: boolean;
  readonly reasons: readonly IdnRejectionReason[];
  readonly primaryReason: IdnRejectionReason | null;
}

const CJK_SCRIPTS = new Set(["Han", "Hiragana", "Katakana", "Hangul", "Bopomofo"]);

const SCRIPT_PATTERNS: ReadonlyArray<readonly [string, RegExp]> = [
  ["Latin", /\p{Script=Latin}/u],
  ["Cyrillic", /\p{Script=Cyrillic}/u],
  ["Greek", /\p{Script=Greek}/u],
  ["Han", /\p{Script=Han}/u],
  ["Hiragana", /\p{Script=Hiragana}/u],
  ["Katakana", /\p{Script=Katakana}/u],
  ["Hangul", /\p{Script=Hangul}/u],
  ["Bopomofo", /\p{Script=Bopomofo}/u],
];

const LETTER_RE = /\p{Letter}/u;
const MARK_RE = /\p{M}/u;
const LEADING_MARK_RE = /^\p{M}/u;
const NEUTRAL_IN_DOMAIN_RE = /^[\p{Nd}-]$/u;

const INVISIBLE_RE =
  /[\p{Cf}\p{Cc}\u034F\u115F\u1160\u17B4\u17B5\u180B-\u180F\u200B-\u200F\u202A-\u202E\u2060-\u2064\u2066-\u206F\u3164\uFE00-\uFE0F\uFEFF\uFFA0\uFFF9-\uFFFB\u{E0000}-\u{E007F}\u{E0100}-\u{E01EF}]/u;

const FORBIDDEN_HOSTNAME_CHARACTERS = /[\/\\?#@:%]/;

const CYRILLIC_LATIN_CONFUSABLES = new Set([
  "\u0410",
  "\u0412",
  "\u0415",
  "\u0401",
  "\u041A",
  "\u041C",
  "\u041D",
  "\u041E",
  "\u0420",
  "\u0421",
  "\u0422",
  "\u0423",
  "\u0425",
  "\u0405",
  "\u0406",
  "\u0408",
  "\u04BA",
  "\u0500",
  "\u051A",
  "\u051C",
  "\u04C0",
  "\u0430",
  "\u0432",
  "\u0435",
  "\u0451",
  "\u043A",
  "\u043C",
  "\u043D",
  "\u043E",
  "\u0440",
  "\u0441",
  "\u0442",
  "\u0443",
  "\u0445",
  "\u0455",
  "\u0456",
  "\u0458",
  "\u04BB",
  "\u0501",
  "\u051B",
  "\u051D",
  "\u04CF",
  "\u0448",
  "\u044C",
]);

function invalidAnalysis(
  input: string,
  detectedReasons: readonly IdnRejectionReason[] = [],
): IdnDisplayAnalysis {
  const reasons: IdnRejectionReason[] =
    detectedReasons.length > 0
      ? [...detectedReasons, "invalid-hostname"]
      : ["invalid-hostname"];

  return {
    input,
    ascii: "",
    unicode: input,
    display: "",
    safeForUnicode: false,
    rejected: true,
    reasons,
    primaryReason: reasons[0] ?? null,
  };
}

function fallbackAsciiHostname(input: string): string {
  const candidate = input.trim().replace(/\.$/, "").toLowerCase();
  if (candidate.length === 0 || FORBIDDEN_HOSTNAME_CHARACTERS.test(candidate)) {
    throw new TypeError("Input must be a hostname");
  }

  return candidate
    .split(".")
    .map((label) => {
      if (label.length === 0) {
        throw new TypeError("Hostname contains an empty label");
      }
      if (
        Array.from(label, (character) => character.codePointAt(0) ?? 0).every(
          (codePoint) => codePoint < 0x80,
        )
      ) {
        return label;
      }
      return `xn--${encodePunycode(label)}`;
    })
    .join(".");
}

/**
 * Converts a hostname to its canonical A-label form with the browser URL parser.
 * The parser applies the platform's IDNA/UTS #46 mapping before we inspect the
 * label contents.
 */
export function toAsciiHostname(input: string): string {
  const candidate = input.trim();
  if (candidate.length === 0 || FORBIDDEN_HOSTNAME_CHARACTERS.test(candidate)) {
    throw new TypeError("Input must be a hostname without credentials, port, path, query, or fragment");
  }

  const url = new URL(`http://${candidate}/`);
  if (
    url.username !== "" ||
    url.password !== "" ||
    url.port !== "" ||
    url.pathname !== "/" ||
    url.search !== "" ||
    url.hash !== ""
  ) {
    throw new TypeError("Input must be a hostname without credentials, port, path, query, or fragment");
  }

  const ascii = url.hostname.replace(/\.$/, "");
  if (ascii.length === 0) {
    throw new TypeError("Hostname is empty");
  }
  return ascii;
}

/**
 * Converts an A-label hostname back to its canonical Unicode form.
 */
export function toUnicodeHostname(asciiHostname: string): string {
  return asciiHostname
    .split(".")
    .map((label) => {
      if (!label.toLowerCase().startsWith("xn--")) {
        return label;
      }
      const decoded = decodePunycode(label.slice(4));
      if (decoded.length === 0) {
        throw new TypeError("Invalid Punycode label");
      }
      return decoded;
    })
    .join(".");
}

function scriptOf(codePoint: string): string {
  for (const [script, pattern] of SCRIPT_PATTERNS) {
    if (pattern.test(codePoint)) {
      return script;
    }
  }
  return LETTER_RE.test(codePoint) ? "Other" : "Common";
}

function containsInvisibleCharacter(value: string): boolean {
  return INVISIBLE_RE.test(value);
}

function hasLeadingCombiningMark(value: string): boolean {
  for (const label of value.split(".")) {
    if (label !== "" && LEADING_MARK_RE.test(label)) {
      return true;
    }
  }
  return false;
}

function hasDisallowedScriptMix(label: string): boolean {
  const scripts = new Set<string>();

  for (const codePoint of label) {
    const script = scriptOf(codePoint);
    if (script !== "Common") {
      scripts.add(script);
    }
  }

  if (scripts.size <= 1) {
    return false;
  }

  for (const script of scripts) {
    if (script !== "Latin" && !CJK_SCRIPTS.has(script)) {
      return true;
    }
  }
  return false;
}

function isAllCyrillicAndConfusable(label: string): boolean {
  let sawCyrillic = false;

  for (const codePoint of label) {
    if (CYRILLIC_LATIN_CONFUSABLES.has(codePoint)) {
      sawCyrillic = true;
      continue;
    }
    if (scriptOf(codePoint) === "Cyrillic") {
      return false;
    }
    if (MARK_RE.test(codePoint) || NEUTRAL_IN_DOMAIN_RE.test(codePoint)) {
      continue;
    }
    return false;
  }

  return sawCyrillic;
}

function sourcePolicyReasons(sourceHostnames: readonly string[]): IdnRejectionReason[] {
  const reasons: IdnRejectionReason[] = [];

  if (sourceHostnames.some(containsInvisibleCharacter)) {
    reasons.push("invisible-character");
  }
  if (sourceHostnames.some(hasLeadingCombiningMark)) {
    reasons.push("leading-combining-mark");
  }
  return reasons;
}

function collectReasons(
  sourceHostnames: readonly string[],
  unicodeHostname: string,
): IdnRejectionReason[] {
  const reasons: IdnRejectionReason[] = [];

  if (
    sourceHostnames.some(containsInvisibleCharacter) ||
    containsInvisibleCharacter(unicodeHostname)
  ) {
    reasons.push("invisible-character");
  }
  if (sourceHostnames.some(hasLeadingCombiningMark) || hasLeadingCombiningMark(unicodeHostname)) {
    reasons.push("leading-combining-mark");
  }

  for (const label of unicodeHostname.split(".")) {
    if (label === "") {
      continue;
    }
    if (hasDisallowedScriptMix(label)) {
      reasons.push("mixed-script");
      break;
    }
  }

  for (const label of unicodeHostname.split(".")) {
    if (label !== "" && isAllCyrillicAndConfusable(label)) {
      reasons.push("cyrillic-confusable");
      break;
    }
  }

  return reasons;
}

/**
 * Decides whether a hostname may be shown in Unicode, or must be shown as an
 * A-label (Punycode). The decision is made from canonical A-label output.
 */
export function analyzeIdnHostname(input: string): IdnDisplayAnalysis {
  if (typeof input !== "string") {
    return invalidAnalysis("");
  }

  const rawSource = input;
  const source = input.trim();
  if (source.length === 0) {
    return invalidAnalysis(rawSource, sourcePolicyReasons([rawSource]));
  }

  let ascii: string;
  try {
    ascii = toAsciiHostname(source);
  } catch {
    const detectedReasons = sourcePolicyReasons([rawSource, source]);
    if (detectedReasons.length > 0) {
      try {
        const fallbackAscii = fallbackAsciiHostname(source);
        const reasons: IdnRejectionReason[] = [
          ...detectedReasons,
          "invalid-hostname",
        ];
        return {
          input: rawSource,
          ascii: fallbackAscii,
          unicode: source,
          display: fallbackAscii,
          safeForUnicode: false,
          rejected: true,
          reasons,
          primaryReason: reasons[0] ?? null,
        };
      } catch {
        return invalidAnalysis(rawSource, detectedReasons);
      }
    }
    return invalidAnalysis(rawSource);
  }

  let unicode: string;
  try {
    unicode = toUnicodeHostname(ascii);
  } catch {
    return invalidAnalysis(rawSource);
  }

  try {
    if (toAsciiHostname(unicode) !== ascii) {
      return invalidAnalysis(rawSource);
    }
  } catch {
    return invalidAnalysis(rawSource);
  }

  const reasons = collectReasons(
    [rawSource, source.replace(/\.$/, "")],
    unicode,
  );
  const rejected = reasons.length > 0;

  return {
    input: rawSource,
    ascii,
    unicode,
    display: rejected ? ascii : unicode,
    safeForUnicode: !rejected,
    rejected,
    reasons,
    primaryReason: reasons[0] ?? null,
  };
}

/**
 * Returns the only hostname string a UI should display: Unicode when the IDN
 * passes the policy, otherwise its canonical A-label.
 */
export function hostnameForDisplay(input: string): string {
  return analyzeIdnHostname(input).display;
}

export function shouldDisplayUnicode(input: string): boolean {
  return analyzeIdnHostname(input).safeForUnicode;
}
