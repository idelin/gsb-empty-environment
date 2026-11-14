/**
 * Account identifier normalization and generation-aware lockout tracking.
 *
 * Declared normalization rules
 * ----------------------------
 * Usernames:
 *   - trim surrounding whitespace / invisible format characters;
 *   - apply Unicode NFKC;
 *   - apply Unicode-style case folding (including ß -> ss and final sigma);
 *   - treat dot-like characters as "." and remove trailing dots;
 *   - reject controls, invisible characters, mixed scripts and known
 *     Latin/Greek/Cyrillic homoglyphs.
 *
 * Emails:
 *   - the local part is Unicode-normalized but its case is preserved;
 *   - provider aliases are collapsed for lockout/audit identity:
 *     "+tag" is removed and Gmail/Googlemail dot aliases are removed;
 *   - the domain is Unicode-normalized, case-folded and its trailing dots
 *     are removed;
 *   - Googlemail is mapped to Gmail for the canonical lockout key.
 *
 * The normalized key is the only value used for lockout counters and audit
 * correlation. The original display value is kept on every state/audit record.
 */

export type AccountKind = "username" | "email";

export interface NormalizeOptions {
  /** Reject homoglyphs, mixed scripts and non-ASCII decimal digits. Default true. */
  rejectConfusables?: boolean;
  /** Remove "+tag" aliases from email local parts. Default true. */
  collapseEmailAliases?: boolean;
  /** Remove provider dot aliases (Gmail/Googlemail). Default true. */
  collapseDotAliases?: boolean;
}

export interface AccountNormalizeOptions extends NormalizeOptions {
  kind?: AccountKind;
}

export interface AccountIdentifier {
  kind: AccountKind;
  /** Normalized lockout/audit key. */
  key: string;
  /** Alias of key, convenient when the result is serialized. */
  normalized: string;
  /** Original value, kept only for display/audit. */
  display: string;
  /** Alias of display. */
  original: string;
  /** Alias of display. */
  raw: string;
}

export interface AccountInput {
  raw?: string;
  identifier?: string;
  value?: string;
  username?: string;
  email?: string;
  kind?: AccountKind;
  type?: AccountKind;
  generation?: number;
  method?: string;
  authMethod?: string;
  /** A key that has already been normalized by this module. */
  key?: string;
  normalized?: string;
  display?: string;
}

export type LockoutInput = string | AccountInput;

export interface AttemptOptions extends AccountNormalizeOptions {
  method?: string;
  authMethod?: string;
  generation?: number;
}

export type IdentifierNormalizer =
  | ((raw: string, kind: AccountKind) => string)
  | {
      normalizeUsername?: (raw: string) => string;
      normalizeEmail?: (raw: string) => string;
      normalize?: (raw: string, kind?: AccountKind) => string;
    };

export interface LockoutOptions extends NormalizeOptions {
  /** Number of consecutive failures that locks the account. Default 5. */
  threshold?: number;
  /** Alias of threshold. */
  maxFailures?: number;
  /** Alias of threshold. */
  maxAttempts?: number;
  /** Alias of threshold. */
  failureThreshold?: number;
  /** Alias of threshold. */
  limit?: number;
  /** Alias of threshold. */
  lockThreshold?: number;
  /** Alias of threshold. */
  maxFailedAttempts?: number;
  /** Lock duration. 0/undefined means until an explicit unlock. */
  lockoutDurationMs?: number;
  /** Alias of lockoutDurationMs. */
  durationMs?: number;
  /** Alias of lockoutDurationMs. */
  duration?: number;
  /** Injectable clock, primarily for deterministic tests. */
  now?: () => number;
  /** Alias of now. */
  clock?: () => number;
  /** Optional hard limit for the in-memory audit log. */
  auditLimit?: number;
  /** Optional normalizer; defaults to the rules declared by this module. */
  normalizer?: IdentifierNormalizer;
  /** Alias of normalizer. */
  normalize?: IdentifierNormalizer;
}

export interface LockoutState {
  key: string;
  normalized: string;
  normalizedKey: string;
  accountKey: string;
  kind: AccountKind;
  display: string;
  displayValue: string;
  original: string;
  firstDisplay: string;
  generation: number;
  failureCount: number;
  failures: number;
  consecutiveFailures: number;
  successCount: number;
  attempts: number;
  attemptCount: number;
  count: number;
  totalAttempts: number;
  locked: boolean;
  isLocked: boolean;
  lockedUntil: number | null;
  remainingAttempts: number;
  lastMethod: string | null;
  lastAttemptAt: number | null;
}

export interface AttemptResult extends LockoutState {
  outcome: "success" | "failure";
  method: string;
  timestamp: number;
  triggeredLock: boolean;
  stale: boolean;
  ignored: boolean;
}

export interface UnlockResult {
  key: string;
  normalized: string;
  normalizedKey: string;
  accountKey: string;
  kind: AccountKind;
  display: string;
  displayValue: string;
  original: string;
  previousGeneration: number;
  generation: number;
  failureCount: number;
  consecutiveFailures: number;
  locked: boolean;
  unlocked: boolean;
  stale: boolean;
  timestamp: number;
}

export interface AuditEntry {
  key: string;
  normalized: string;
  normalizedKey: string;
  accountKey: string;
  kind: AccountKind;
  display: string;
  displayValue: string;
  original: string;
  raw: string;
  rawValue: string;
  method: string;
  outcome: "success" | "failure" | "unlock";
  generation: number;
  timestamp: number;
  at: number;
  iso: string;
  failureCount: number;
  consecutiveFailures: number;
  locked: boolean;
  triggeredLock: boolean;
  stale: boolean;
}

export type AccountIdentifierErrorCode =
  | "INVALID_IDENTIFIER"
  | "EMPTY_IDENTIFIER"
  | "INVALID_USERNAME"
  | "INVALID_EMAIL"
  | "INVISIBLE_CHARACTER"
  | "CONFUSABLE_CHARACTER"
  | "CONFUSABLE_DIGIT"
  | "MIXED_SCRIPTS";

export class AccountIdentifierError extends Error {
  code: AccountIdentifierErrorCode;
  identifier: string;
  kind: AccountKind;

  constructor(
    code: AccountIdentifierErrorCode,
    message: string,
    identifier = "",
    kind: AccountKind = "username",
  ) {
    super(message);
    this.name = "AccountIdentifierError";
    this.code = code;
    this.identifier = identifier;
    this.kind = kind;
  }
}

interface InternalState {
  key: string;
  accountKey: string;
  kind: AccountKind;
  display: string;
  firstDisplay: string;
  generation: number;
  failureCount: number;
  consecutiveFailures: number;
  successCount: number;
  attemptCount: number;
  locked: boolean;
  lockedUntil: number | null;
  lastMethod: string | null;
  lastAttemptAt: number | null;
}

interface ResolvedInput {
  identity: AccountIdentifier;
  generation?: number;
}

const DOT_LIKE = /[\u3002\uFF61\u2024\u2026\uFE52\uFF0E]/gu;
const EDGE_INVISIBLE = /^[\s\p{Cf}\uFEFF]+|[\s\p{Cf}\uFEFF]+$/gu;
const UNSAFE_CHARACTER =
  /[\s\p{Cc}\p{Cf}\p{Cs}\p{Co}\u034F\uFEFF\uFE00-\uFE0F\u{E0100}-\u{E01EF}]/u;
const USERNAME_CHARACTERS = /^[\p{L}\p{N}\p{M}._-]+$/u;
const DOMAIN_LABEL = /^[\p{L}\p{N}](?:[\p{L}\p{N}-]*[\p{L}\p{N}])?$/u;
const DECIMAL_DIGIT = /\p{Nd}/u;
const LETTER = /\p{L}/u;
const ASCII_DIGIT = /^[0-9]$/;

const SCRIPT_TESTS: Array<[string, RegExp]> = [
  ["Latin", /\p{Script=Latin}/u],
  ["Greek", /\p{Script=Greek}/u],
  ["Cyrillic", /\p{Script=Cyrillic}/u],
  ["Armenian", /\p{Script=Armenian}/u],
  ["Hebrew", /\p{Script=Hebrew}/u],
  ["Arabic", /\p{Script=Arabic}/u],
  ["Syriac", /\p{Script=Syriac}/u],
  ["Devanagari", /\p{Script=Devanagari}/u],
  ["Bengali", /\p{Script=Bengali}/u],
  ["Thai", /\p{Script=Thai}/u],
  ["Han", /\p{Script=Han}/u],
  ["Hiragana", /\p{Script=Hiragana}/u],
  ["Katakana", /\p{Script=Katakana}/u],
  ["Bopomofo", /\p{Script=Bopomofo}/u],
  ["Hangul", /\p{Script=Hangul}/u],
  ["Georgian", /\p{Script=Georgian}/u],
  ["Ethiopic", /\p{Script=Ethiopic}/u],
];

const JAPANESE_SCRIPTS = new Set(["Han", "Hiragana", "Katakana"]);

const SPECIAL_CASE_FOLDS = new Map<string, string>([
  ["ß", "ss"],
  ["ς", "σ"],
  ["ı", "i"],
  ["ȷ", "j"],
  ["ɑ", "a"],
  ["ᴀ", "a"],
  ["ʙ", "b"],
  ["ᴄ", "c"],
  ["ᴅ", "d"],
  ["ᴇ", "e"],
  ["ꜰ", "f"],
  ["ɡ", "g"],
  ["ɢ", "g"],
  ["ʜ", "h"],
  ["ɩ", "i"],
  ["ɪ", "i"],
  ["ᴊ", "j"],
  ["ᴋ", "k"],
  ["ʟ", "l"],
  ["ᴍ", "m"],
  ["ɴ", "n"],
  ["ᴏ", "o"],
  ["ǫ", "o"],
  ["ᴘ", "p"],
  ["ʀ", "r"],
  ["ꜱ", "s"],
  ["ᴛ", "t"],
  ["ᴜ", "u"],
  ["ᴠ", "v"],
  ["ᴡ", "w"],
  ["ʏ", "y"],
  ["ᴢ", "z"],
]);

const CONFUSABLE_CODE_POINTS = [
  // Cyrillic upper/lowercase characters that closely resemble Latin letters.
  0x0405, 0x0406, 0x0408, 0x0410, 0x0412, 0x0415, 0x041a, 0x041c,
  0x041d, 0x041e, 0x0420, 0x0421, 0x0422, 0x0423, 0x0425, 0x04ae,
  0x04ba, 0x04c0, 0x04cf, 0x051a, 0x051c, 0x0430, 0x0435, 0x043e,
  0x0440, 0x0441, 0x0443, 0x0445, 0x0455, 0x0456, 0x0458, 0x04bb,
  0x0501, 0x051b, 0x051d,
  // Greek upper/lowercase characters that closely resemble Latin letters.
  0x0372, 0x0391, 0x0392, 0x0395, 0x0396, 0x0397, 0x0399, 0x039a,
  0x039c, 0x039d, 0x039f, 0x03a1, 0x03a4, 0x03a5, 0x03a7, 0x03b1,
  0x03b5, 0x03b9, 0x03ba, 0x03bd, 0x03bf, 0x03c1, 0x03c4, 0x03c5,
  0x03c7, 0x03f2, 0x03f3, 0x03f9,
  // Cherokee capitals that are deliberately indistinguishable from Latin.
  0x13aa, 0x13ab, 0x13ac, 0x13b3, 0x13b4, 0x13b7, 0x13bb, 0x13c0,
  0x13c2, 0x13c3, 0x13cf, 0x13d2, 0x13d4, 0x13da, 0x13e3, 0x13ef,
  0x13f4,
];

const CONFUSABLE_CHARACTERS = new Set(
  CONFUSABLE_CODE_POINTS.map((codePoint) => String.fromCodePoint(codePoint)),
);

const DOT_ALIAS_DOMAINS = new Set(["gmail.com", "googlemail.com"]);

export const DEFAULT_LOCKOUT_THRESHOLD = 5;
export const DEFAULT_LOCKOUT_DURATION_MS = 0;

function fail(
  code: AccountIdentifierErrorCode,
  message: string,
  identifier: string,
  kind: AccountKind,
): never {
  throw new AccountIdentifierError(code, message, identifier, kind);
}

function requireString(value: unknown, label: string): string {
  if (typeof value !== "string") {
    throw new TypeError(`${label} must be a string`);
  }
  return value;
}

function trimEdgeInvisibles(value: string): string {
  return value.replace(EDGE_INVISIBLE, "");
}

function mapDotLike(value: string): string {
  return value.replace(DOT_LIKE, ".");
}

function stripTrailingDots(value: string): string {
  return value.replace(/\.+$/u, "");
}

function foldCase(value: string): string {
  let folded = value.toLowerCase();
  folded = folded.replace(/i\u0307/gu, "i");
  folded = Array.from(folded)
    .map((character) => SPECIAL_CASE_FOLDS.get(character) ?? character)
    .join("");
  return folded.normalize("NFKC");
}

function unicodeNormalize(value: string): string {
  return mapDotLike(value.normalize("NFKC"));
}

function collectScripts(value: string): Set<string> {
  const scripts = new Set<string>();
  for (const character of value) {
    if (!LETTER.test(character)) {
      continue;
    }
    let matched = false;
    for (const [name, pattern] of SCRIPT_TESTS) {
      if (pattern.test(character)) {
        scripts.add(name);
        matched = true;
        break;
      }
    }
    if (!matched) {
      scripts.add("Other");
    }
  }
  return scripts;
}

function validateCommonSafety(
  value: string,
  original: string,
  kind: AccountKind,
  options: NormalizeOptions,
): void {
  if (UNSAFE_CHARACTER.test(value)) {
    fail(
      "INVISIBLE_CHARACTER",
      "Identifier contains whitespace, a control character, or an invisible character",
      original,
      kind,
    );
  }

  if (options.rejectConfusables === false) {
    return;
  }

  for (const character of value) {
    if (CONFUSABLE_CHARACTERS.has(character)) {
      const codePoint = character.codePointAt(0)!
        .toString(16)
        .toUpperCase()
        .padStart(4, "0");
      fail(
        "CONFUSABLE_CHARACTER",
        `Identifier contains a confusable character: U+${codePoint}`,
        original,
        kind,
      );
    }
    if (DECIMAL_DIGIT.test(character) && !ASCII_DIGIT.test(character)) {
      fail(
        "CONFUSABLE_DIGIT",
        "Identifier contains a non-ASCII decimal digit",
        original,
        kind,
      );
    }
  }

  const scripts = collectScripts(value);
  if (scripts.size > 1) {
    const japaneseOnly = Array.from(scripts).every((script) =>
      JAPANESE_SCRIPTS.has(script),
    );
    if (!japaneseOnly) {
      fail(
        "MIXED_SCRIPTS",
        `Identifier mixes scripts: ${Array.from(scripts).sort().join(", ")}`,
        original,
        kind,
      );
    }
  }
}

/**
 * Normalize a username to the canonical lockout/audit key.
 */
export function normalizeUsername(
  raw: string,
  options: NormalizeOptions = {},
): string {
  const original = requireString(raw, "username");
  const trimmed = trimEdgeInvisibles(original);
  if (trimmed.length === 0) {
    fail("EMPTY_IDENTIFIER", "Username is empty", original, "username");
  }

  const compatibility = unicodeNormalize(trimmed);
  validateCommonSafety(compatibility, original, "username", options);

  let normalized = foldCase(compatibility);
  normalized = unicodeNormalize(normalized);
  normalized = stripTrailingDots(normalized);
  normalized = foldCase(normalized);
  normalized = stripTrailingDots(normalized);

  if (normalized.length === 0) {
    fail("EMPTY_IDENTIFIER", "Username is empty after normalization", original, "username");
  }
  if (!USERNAME_CHARACTERS.test(normalized)) {
    fail(
      "INVALID_USERNAME",
      "Username contains characters outside letters, numbers, marks, '.', '_' or '-'",
      original,
      "username",
    );
  }
  if (!/[\p{L}\p{N}]/u.test(normalized)) {
    fail(
      "INVALID_USERNAME",
      "Username must contain at least one letter or number",
      original,
      "username",
    );
  }
  validateCommonSafety(normalized, original, "username", options);
  return normalized;
}

function validateEmailLocal(
  local: string,
  original: string,
  options: NormalizeOptions,
): void {
  if (UNSAFE_CHARACTER.test(local)) {
    fail(
      "INVISIBLE_CHARACTER",
      "Email local part contains whitespace, a control character, or an invisible character",
      original,
      "email",
    );
  }
  if (options.rejectConfusables === false) {
    return;
  }
  for (const character of local) {
    if (CONFUSABLE_CHARACTERS.has(character)) {
      fail(
        "CONFUSABLE_CHARACTER",
        "Email local part contains a confusable character",
        original,
        "email",
      );
    }
  }
  const scripts = collectScripts(local);
  if (scripts.size > 1) {
    const japaneseOnly = Array.from(scripts).every((script) =>
      JAPANESE_SCRIPTS.has(script),
    );
    if (!japaneseOnly) {
      fail(
        "MIXED_SCRIPTS",
        `Email local part mixes scripts: ${Array.from(scripts).sort().join(", ")}`,
        original,
        "email",
      );
    }
  }
}

function normalizeDomain(original: string, options: NormalizeOptions): string {
  let domain = unicodeNormalize(trimEdgeInvisibles(original));
  domain = stripTrailingDots(domain);
  domain = foldCase(domain);
  domain = stripTrailingDots(domain);

  if (domain.length === 0) {
    fail("INVALID_EMAIL", "Email domain is empty", original, "email");
  }
  if (UNSAFE_CHARACTER.test(domain)) {
    fail(
      "INVISIBLE_CHARACTER",
      "Email domain contains whitespace or an invisible character",
      original,
      "email",
    );
  }
  if (options.rejectConfusables !== false) {
    for (const character of domain) {
      if (CONFUSABLE_CHARACTERS.has(character)) {
        fail(
          "CONFUSABLE_CHARACTER",
          "Email domain contains a confusable character",
          original,
          "email",
        );
      }
    }
  }

  const labels = domain.split(".");
  if (labels.some((label) => label.length === 0 || !DOMAIN_LABEL.test(label))) {
    fail("INVALID_EMAIL", "Email domain contains an invalid label", original, "email");
  }
  if (options.collapseEmailAliases !== false && domain === "googlemail.com") {
    domain = "gmail.com";
  }
  return domain;
}

/**
 * Normalize an email address to the canonical lockout/audit key.
 *
 * The local part keeps its case. The domain is case-folded. For the lockout
 * key, "+tag" aliases are removed (all domains) and dot aliases are removed
 * for Gmail/Googlemail.
 */
export function normalizeEmail(
  raw: string,
  options: NormalizeOptions = {},
): string {
  const original = requireString(raw, "email");
  const trimmed = trimEdgeInvisibles(original);
  if (trimmed.length === 0) {
    fail("EMPTY_IDENTIFIER", "Email is empty", original, "email");
  }

  const compatibility = unicodeNormalize(trimmed);
  const at = compatibility.indexOf("@");
  if (at < 0 || at !== compatibility.lastIndexOf("@")) {
    fail(
      "INVALID_EMAIL",
      "Email must contain exactly one '@' separator",
      original,
      "email",
    );
  }

  let local = compatibility.slice(0, at);
  let domain = compatibility.slice(at + 1);
  if (local.length === 0 || domain.length === 0) {
    fail("INVALID_EMAIL", "Email local part and domain are required", original, "email");
  }

  validateEmailLocal(local, original, options);
  local = stripTrailingDots(local);

  if (options.collapseEmailAliases !== false) {
    const plus = local.indexOf("+");
    if (plus >= 0) {
      local = local.slice(0, plus);
    }
  }
  local = stripTrailingDots(local);
  if (local.length === 0) {
    fail(
      "INVALID_EMAIL",
      "Email local part is empty after alias normalization",
      original,
      "email",
    );
  }

  domain = normalizeDomain(domain, options);

  if (
    options.collapseEmailAliases !== false &&
    options.collapseDotAliases !== false &&
    DOT_ALIAS_DOMAINS.has(domain)
  ) {
    local = local.replace(/\./gu, "");
  }
  if (local.length === 0) {
    fail(
      "INVALID_EMAIL",
      "Email local part is empty after alias normalization",
      original,
      "email",
    );
  }

  return `${local}@${domain}`;
}

export function detectAccountKind(raw: string): AccountKind {
  const value = requireString(raw, "identifier");
  const compatibility = trimEdgeInvisibles(value).normalize("NFKC");
  return compatibility.includes("@") ? "email" : "username";
}

/**
 * Normalize either kind of account identifier and return its lockout key.
 *
 * If kind is omitted, an "@" selects email normalization.
 */
export function normalizeAccountIdentifier(
  raw: string,
  kindOrOptions: AccountKind | AccountNormalizeOptions = {},
): string {
  const options =
    kindOrOptions === null || typeof kindOrOptions === "string"
      ? {}
      : kindOrOptions;
  const kind =
    typeof kindOrOptions === "string"
      ? kindOrOptions
      : kindOrOptions.kind ?? detectAccountKind(raw);
  return kind === "email"
    ? normalizeEmail(raw, options)
    : normalizeUsername(raw, options);
}

export const normalizeIdentifier = normalizeAccountIdentifier;
export const deriveLockoutKey = normalizeAccountIdentifier;
export const accountKey = normalizeAccountIdentifier;
export const lockoutKey = normalizeAccountIdentifier;
export const auditKey = normalizeAccountIdentifier;
export const getCanonicalKey = normalizeAccountIdentifier;
export const normalizeAccountId = normalizeAccountIdentifier;
export const normalizeIdentity = normalizeAccountIdentifier;
export const normalize = normalizeAccountIdentifier;
export const normalizeUser = normalizeUsername;
export const normalizeEmailAddress = normalizeEmail;
export const toAccountIdentifier = normalizeAccountIdentifier;

/**
 * Return both the normalized key and the separately retained display value.
 */
export function accountIdentity(
  raw: string,
  kindOrOptions: AccountKind | AccountNormalizeOptions = {},
): AccountIdentifier {
  const options =
    kindOrOptions === null || typeof kindOrOptions === "string"
      ? {}
      : kindOrOptions;
  const kind =
    typeof kindOrOptions === "string"
      ? kindOrOptions
      : kindOrOptions.kind ?? detectAccountKind(raw);
  const key = normalizeAccountIdentifier(raw, { ...options, kind });
  return {
    kind,
    key,
    normalized: key,
    display: raw,
    original: raw,
    raw,
  };
}

export const canonicalizeAccountIdentifier = accountIdentity;
export const describeAccountIdentifier = accountIdentity;
export const parseAccountIdentifier = accountIdentity;
export const normalizeAccount = accountIdentity;
export const normalizeAccountWithDisplay = accountIdentity;
export const normalizeAccountWithMetadata = accountIdentity;
export const toAccountIdentity = accountIdentity;
export const canonicalize = accountIdentity;
export const identity = accountIdentity;

export function canonicalizeUsername(raw: string, options: NormalizeOptions = {}): AccountIdentifier {
  return accountIdentity(raw, { ...options, kind: "username" });
}

export function canonicalizeEmail(raw: string, options: NormalizeOptions = {}): AccountIdentifier {
  return accountIdentity(raw, { ...options, kind: "email" });
}

export function parseUsername(raw: string, options: NormalizeOptions = {}): AccountIdentifier {
  return canonicalizeUsername(raw, options);
}

export function parseEmail(raw: string, options: NormalizeOptions = {}): AccountIdentifier {
  return canonicalizeEmail(raw, options);
}

export const normalizeUsernameWithDisplay = canonicalizeUsername;
export const normalizeEmailWithDisplay = canonicalizeEmail;

/**
 * Configurable normalizer for dependency injection into an auth service.
 */
export class AccountIdentifierNormalizer {
  options: NormalizeOptions;

  constructor(options: NormalizeOptions = {}) {
    this.options = { ...options };
  }

  normalizeUsername(raw: string): string {
    return normalizeUsername(raw, this.options);
  }

  normalizeEmail(raw: string): string {
    return normalizeEmail(raw, this.options);
  }

  normalize(
    raw: string,
    kindOrOptions: AccountKind | AccountNormalizeOptions = {},
  ): string {
    const provided = kindOrOptions ?? {};
    const options =
      typeof provided === "string"
        ? this.options
        : { ...this.options, ...provided };
    const kind =
      typeof provided === "string" ? provided : provided.kind;
    return normalizeAccountIdentifier(raw, { ...options, kind });
  }

  identity(
    raw: string,
    kindOrOptions: AccountKind | AccountNormalizeOptions = {},
  ): AccountIdentifier {
    const provided = kindOrOptions ?? {};
    const options =
      typeof provided === "string"
        ? this.options
        : { ...this.options, ...provided };
    const kind =
      typeof provided === "string" ? provided : provided.kind;
    return accountIdentity(raw, { ...options, kind });
  }

  username(raw: string): string {
    return this.normalizeUsername(raw);
  }

  email(raw: string): string {
    return this.normalizeEmail(raw);
  }

  key(raw: string, kind?: AccountKind): string {
    return this.normalize(raw, kind);
  }
}

export function createAccountNormalizer(
  options: NormalizeOptions = {},
): AccountIdentifierNormalizer {
  return new AccountIdentifierNormalizer(options);
}

function resolveAccountInput(
  input: LockoutInput,
  kind?: AccountKind,
  normalizer?: IdentifierNormalizer,
): ResolvedInput {
  if (typeof input === "string") {
    return {
      identity: normalizeWithNormalizer(
        input,
        kind ?? detectAccountKind(input),
        normalizer,
      ),
    };
  }
  if (input === null || typeof input !== "object") {
    throw new TypeError("account identifier must be a string or an object");
  }

  const explicitKind =
    input.kind ?? input.type ??
    (input.username !== undefined ? "username" : undefined) ??
    (input.email !== undefined ? "email" : undefined) ?? kind;

  if (input.key !== undefined || input.normalized !== undefined) {
    if (
      input.raw !== undefined || input.identifier !== undefined ||
      input.value !== undefined || input.username !== undefined ||
      input.email !== undefined
    ) {
      const raw = input.raw ?? input.identifier ?? input.value ??
        input.username ?? input.email;
      return {
        identity: normalizeWithNormalizer(
          raw!,
          explicitKind ?? detectAccountKind(raw!),
          normalizer,
        ),
        generation: input.generation,
      };
    }
    const key = requireString(input.key ?? input.normalized, "key");
    const resolvedKind =
      explicitKind ?? (key.includes("@") ? "email" : "username");
    const display = input.display ?? key;
    return {
      identity: {
        kind: resolvedKind,
        key,
        normalized: key,
        display,
        original: display,
        raw: display,
      },
      generation: input.generation,
    };
  }

  const raw =
    input.raw ?? input.identifier ?? input.value ?? input.username ?? input.email;
  if (typeof raw !== "string") {
    throw new TypeError("account input is missing a string identifier");
  }
  return {
    identity: normalizeWithNormalizer(
      raw,
      explicitKind ?? detectAccountKind(raw),
      normalizer,
    ),
    generation: input.generation,
  };
}

function normalizeWithNormalizer(
  raw: string,
  kind: AccountKind,
  normalizer?: IdentifierNormalizer,
): AccountIdentifier {
  if (normalizer === undefined) {
    return accountIdentity(raw, kind);
  }

  let key: string | undefined;
  if (typeof normalizer === "function") {
    key = normalizer(raw, kind);
  } else if (kind === "email" && normalizer.normalizeEmail !== undefined) {
    key = normalizer.normalizeEmail(raw);
  } else if (kind === "username" && normalizer.normalizeUsername !== undefined) {
    key = normalizer.normalizeUsername(raw);
  } else if (normalizer.normalize !== undefined) {
    key = normalizer.normalize(raw, kind);
  }
  if (typeof key !== "string") {
    throw new TypeError("custom normalizer must return a string key");
  }
  return {
    kind,
    key,
    normalized: key,
    display: raw,
    original: raw,
    raw,
  };
}

function parseAttemptArguments(
  methodOrOptions?: string | AttemptOptions,
  maybeKind?: AccountKind | number,
): { method: string; options: AttemptOptions; kind?: AccountKind } {
  const kind = typeof maybeKind === "number" ? undefined : maybeKind;
  const generationOptions =
    typeof maybeKind === "number" ? { generation: maybeKind } : {};
  if (methodOrOptions === undefined) {
    return { method: "password", options: generationOptions, kind };
  }
  if (typeof methodOrOptions === "string") {
    return { method: methodOrOptions, options: generationOptions, kind };
  }
  return {
    method: methodOrOptions.method ?? methodOrOptions.authMethod ?? "password",
    options: { ...generationOptions, ...methodOrOptions },
    kind: methodOrOptions.kind ?? kind,
  };
}

function attemptMethod(input: LockoutInput, fallback: string): string {
  if (typeof input === "object" && input !== null) {
    return input.method ?? input.authMethod ?? fallback;
  }
  return fallback;
}

function internalAccountKey(identity: AccountIdentifier): string {
  return identity.key;
}

function makeState(identity: AccountIdentifier): InternalState {
  return {
    key: identity.key,
    accountKey: internalAccountKey(identity),
    kind: identity.kind,
    display: identity.display,
    firstDisplay: identity.display,
    generation: 0,
    failureCount: 0,
    consecutiveFailures: 0,
    successCount: 0,
    attemptCount: 0,
    locked: false,
    lockedUntil: null,
    lastMethod: null,
    lastAttemptAt: null,
  };
}

/**
 * In-memory, generation-aware lockout store.
 *
 * Authentication methods are deliberately not part of the state key. Password,
 * OTP, recovery-code and other failures therefore share one counter.
 */
export class AccountLockoutTracker {
  threshold: number;
  lockoutDurationMs: number;
  auditLimit: number;
  rejectConfusables: boolean;
  collapseEmailAliases: boolean;
  collapseDotAliases: boolean;
  normalizer: IdentifierNormalizer | undefined;
  now: () => number;
  states: Map<string, InternalState>;
  audit: AuditEntry[];

  constructor(options: LockoutOptions = {}) {
    const threshold =
      options.threshold ?? options.maxFailures ?? options.maxAttempts ??
      options.failureThreshold ?? options.limit ?? options.lockThreshold ??
      options.maxFailedAttempts ?? DEFAULT_LOCKOUT_THRESHOLD;
    if (!Number.isInteger(threshold) || threshold < 1) {
      throw new RangeError("lockout threshold must be a positive integer");
    }
    const duration =
      options.lockoutDurationMs ?? options.durationMs ?? options.duration ??
      DEFAULT_LOCKOUT_DURATION_MS;
    if (!Number.isFinite(duration) || duration < 0) {
      throw new RangeError("lockout duration must be a non-negative number");
    }

    this.threshold = threshold;
    this.lockoutDurationMs = duration;
    this.auditLimit = options.auditLimit ?? Number.POSITIVE_INFINITY;
    this.rejectConfusables = options.rejectConfusables !== false;
    this.collapseEmailAliases = options.collapseEmailAliases !== false;
    this.collapseDotAliases = options.collapseDotAliases !== false;
    this.normalizer = options.normalizer ?? options.normalize;
    this.now = options.now ?? options.clock ?? (() => Date.now());
    this.states = new Map<string, InternalState>();
    this.audit = [];
  }

  normalizeOptions(kind: AccountKind): AccountNormalizeOptions {
    return {
      kind,
      rejectConfusables: this.rejectConfusables,
      collapseEmailAliases: this.collapseEmailAliases,
      collapseDotAliases: this.collapseDotAliases,
    };
  }

  ensureState(identity: AccountIdentifier): InternalState {
    const accountKey = internalAccountKey(identity);
    let state = this.states.get(accountKey);
    if (state === undefined) {
      state = makeState(identity);
      this.states.set(accountKey, state);
    } else {
      state.display = identity.display;
    }
    return state;
  }

  refresh(state: InternalState, now: number): void {
    if (
      state.locked &&
      state.lockedUntil !== null &&
      state.lockedUntil <= now
    ) {
      state.locked = false;
      state.lockedUntil = null;
      state.failureCount = 0;
      state.consecutiveFailures = 0;
    }
  }

  snapshot(state: InternalState): LockoutState {
    const remainingAttempts = state.locked
      ? 0
      : Math.max(0, this.threshold - state.consecutiveFailures);
    return {
      key: state.key,
      normalized: state.key,
      normalizedKey: state.key,
      accountKey: state.accountKey,
      kind: state.kind,
      display: state.display,
      displayValue: state.display,
      original: state.display,
      firstDisplay: state.firstDisplay,
      generation: state.generation,
      failureCount: state.failureCount,
      failures: state.failureCount,
      consecutiveFailures: state.consecutiveFailures,
      successCount: state.successCount,
      attempts: state.attemptCount,
      attemptCount: state.attemptCount,
      count: state.attemptCount,
      totalAttempts: state.attemptCount,
      locked: state.locked,
      isLocked: state.locked,
      lockedUntil: state.lockedUntil,
      remainingAttempts,
      lastMethod: state.lastMethod,
      lastAttemptAt: state.lastAttemptAt,
    };
  }

  appendAudit(entry: AuditEntry): void {
    this.audit.push(entry);
    if (Number.isFinite(this.auditLimit) && this.audit.length > this.auditLimit) {
      this.audit.splice(0, this.audit.length - this.auditLimit);
    }
  }

  makeAuditEntry(
    identity: AccountIdentifier,
    state: InternalState,
    method: string,
    outcome: "success" | "failure" | "unlock",
    timestamp: number,
    triggeredLock: boolean,
    stale: boolean,
  ): AuditEntry {
    return {
      key: identity.key,
      normalized: identity.key,
      normalizedKey: identity.key,
      accountKey: internalAccountKey(identity),
      kind: identity.kind,
      display: identity.display,
      displayValue: identity.display,
      original: identity.display,
      raw: identity.display,
      rawValue: identity.display,
      method,
      outcome,
      generation: state.generation,
      timestamp,
      at: timestamp,
      iso: new Date(timestamp).toISOString(),
      failureCount: state.failureCount,
      consecutiveFailures: state.consecutiveFailures,
      locked: state.locked,
      triggeredLock,
      stale,
    };
  }

  result(
    identity: AccountIdentifier,
    state: InternalState,
    outcome: "success" | "failure",
    method: string,
    timestamp: number,
    triggeredLock: boolean,
    stale = false,
  ): AttemptResult {
    return {
      ...this.snapshot(state),
      outcome,
      method,
      timestamp,
      triggeredLock,
      stale,
      ignored: stale,
    };
  }

  recordFailure(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind | number,
  ): AttemptResult {
    const parsed = parseAttemptArguments(methodOrOptions, maybeKind);
    const resolved = resolveAccountInput(input, parsed.kind, this.normalizer);
    const identity = resolved.identity;
    const expectedGeneration = parsed.options.generation ?? resolved.generation;
    const state = this.ensureState(identity);
    const now = this.now();
    const method = attemptMethod(input, parsed.method);
    this.refresh(state, now);

    if (
      expectedGeneration !== undefined &&
      expectedGeneration !== state.generation
    ) {
      this.appendAudit(
        this.makeAuditEntry(
          identity,
          state,
          method,
          "failure",
          now,
          false,
          true,
        ),
      );
      return this.result(
        identity,
        state,
        "failure",
        method,
        now,
        false,
        true,
      );
    }

    state.attemptCount += 1;
    state.failureCount += 1;
    state.consecutiveFailures += 1;
    state.lastMethod = method;
    state.lastAttemptAt = now;

    let triggeredLock = false;
    if (!state.locked && state.consecutiveFailures >= this.threshold) {
      state.locked = true;
      state.lockedUntil =
        this.lockoutDurationMs > 0 ? now + this.lockoutDurationMs : null;
      triggeredLock = true;
    }

    this.appendAudit(
      this.makeAuditEntry(
        identity,
        state,
        method,
        "failure",
        now,
        triggeredLock,
        false,
      ),
    );
    return this.result(
      identity,
      state,
      "failure",
      method,
      now,
      triggeredLock,
    );
  }

  recordSuccess(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind | number,
  ): AttemptResult {
    const parsed = parseAttemptArguments(methodOrOptions, maybeKind);
    const resolved = resolveAccountInput(input, parsed.kind, this.normalizer);
    const identity = resolved.identity;
    const expectedGeneration = parsed.options.generation ?? resolved.generation;
    const state = this.ensureState(identity);
    const now = this.now();
    const method = attemptMethod(input, parsed.method);
    this.refresh(state, now);

    if (
      expectedGeneration !== undefined &&
      expectedGeneration !== state.generation
    ) {
      this.appendAudit(
        this.makeAuditEntry(
          identity,
          state,
          method,
          "success",
          now,
          false,
          true,
        ),
      );
      return this.result(
        identity,
        state,
        "success",
        method,
        now,
        false,
        true,
      );
    }

    state.attemptCount += 1;
    state.successCount += 1;
    state.failureCount = 0;
    state.consecutiveFailures = 0;
    state.locked = false;
    state.lockedUntil = null;
    state.lastMethod = method;
    state.lastAttemptAt = now;

    this.appendAudit(
      this.makeAuditEntry(
        identity,
        state,
        method,
        "success",
        now,
        false,
        false,
      ),
    );
    return this.result(
      identity,
      state,
      "success",
      method,
      now,
      false,
    );
  }

  attempt(
    input: LockoutInput,
    options:
      | (AttemptOptions & { outcome?: "success" | "failure" })
      | "success"
      | "failure" = {},
  ): AttemptResult {
    if (options === "success") {
      return this.recordSuccess(input);
    }
    if (options === "failure") {
      return this.recordFailure(input);
    }
    if (options.outcome === "success") {
      return this.recordSuccess(input, options);
    }
    return this.recordFailure(input, options);
  }

  recordAttempt(
    input: LockoutInput,
    options: AttemptOptions & { outcome?: "success" | "failure" } = {},
  ): AttemptResult {
    return this.attempt(input, options);
  }

  recordFailedAttempt(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordFailure(input, methodOrOptions, maybeKind);
  }

  registerFailure(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordFailure(input, methodOrOptions, maybeKind);
  }

  onFailure(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordFailure(input, methodOrOptions, maybeKind);
  }

  trackFailure(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordFailure(input, methodOrOptions, maybeKind);
  }

  failedAttempt(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordFailure(input, methodOrOptions, maybeKind);
  }

  failure(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordFailure(input, methodOrOptions, maybeKind);
  }

  recordSuccessfulAttempt(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordSuccess(input, methodOrOptions, maybeKind);
  }

  registerSuccess(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordSuccess(input, methodOrOptions, maybeKind);
  }

  onSuccess(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordSuccess(input, methodOrOptions, maybeKind);
  }

  trackSuccess(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordSuccess(input, methodOrOptions, maybeKind);
  }

  successfulAttempt(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordSuccess(input, methodOrOptions, maybeKind);
  }

  success(
    input: LockoutInput,
    methodOrOptions?: string | AttemptOptions,
    maybeKind?: AccountKind,
  ): AttemptResult {
    return this.recordSuccess(input, methodOrOptions, maybeKind);
  }

  isLocked(input: LockoutInput, generation?: number): boolean {
    const resolved = resolveAccountInput(input, undefined, this.normalizer);
    const state = this.states.get(internalAccountKey(resolved.identity));
    if (state === undefined) {
      return false;
    }
    this.refresh(state, this.now());
    const expected = generation ?? resolved.generation;
    if (expected !== undefined && expected !== state.generation) {
      return false;
    }
    return state.locked;
  }

  checkLocked(input: LockoutInput, generation?: number): boolean {
    return this.isLocked(input, generation);
  }

  isAccountLocked(input: LockoutInput, generation?: number): boolean {
    return this.isLocked(input, generation);
  }

  locked(input: LockoutInput, generation?: number): boolean {
    return this.isLocked(input, generation);
  }

  getState(input: LockoutInput): LockoutState | undefined {
    const resolved = resolveAccountInput(input, undefined, this.normalizer);
    const state = this.states.get(internalAccountKey(resolved.identity));
    if (state === undefined) {
      return undefined;
    }
    this.refresh(state, this.now());
    return this.snapshot(state);
  }

  getAccountState(input: LockoutInput): LockoutState | undefined {
    return this.getState(input);
  }

  getLockoutState(input: LockoutInput): LockoutState | undefined {
    return this.getState(input);
  }

  getSnapshot(input: LockoutInput): LockoutState | undefined {
    return this.getState(input);
  }

  getFailureCount(input: LockoutInput): number {
    return this.getState(input)?.failureCount ?? 0;
  }

  getAttemptCount(input: LockoutInput): number {
    return this.getState(input)?.attemptCount ?? 0;
  }

  getRemainingAttempts(input: LockoutInput): number {
    return this.getState(input)?.remainingAttempts ?? this.threshold;
  }

  status(input: LockoutInput): LockoutState {
    const state = this.getState(input);
    if (state !== undefined) {
      return state;
    }
    const resolved = resolveAccountInput(input, undefined, this.normalizer);
    const empty = makeState(resolved.identity);
    return this.snapshot(empty);
  }

  getStatus(input: LockoutInput): LockoutState {
    return this.status(input);
  }

  getGeneration(input: LockoutInput): number {
    const state = this.getState(input);
    return state?.generation ?? 0;
  }

  isGenerationCurrent(input: LockoutInput, generation: number): boolean {
    return this.getGeneration(input) === generation;
  }

  unlock(input: LockoutInput, expectedGeneration?: number): UnlockResult {
    const resolved = resolveAccountInput(input, undefined, this.normalizer);
    const identity = resolved.identity;
    const state = this.ensureState(identity);
    const now = this.now();
    const expected = expectedGeneration ?? resolved.generation;
    const previousGeneration = state.generation;

    if (
      expected !== undefined &&
      expected !== previousGeneration
    ) {
      return {
        key: state.key,
        normalized: state.key,
        normalizedKey: state.key,
        accountKey: state.accountKey,
        kind: state.kind,
        display: state.display,
        displayValue: state.display,
        original: state.display,
        previousGeneration,
        generation: state.generation,
        failureCount: state.failureCount,
        consecutiveFailures: state.consecutiveFailures,
        locked: state.locked,
        unlocked: false,
        stale: true,
        timestamp: now,
      };
    }

    state.generation += 1;
    state.failureCount = 0;
    state.consecutiveFailures = 0;
    state.successCount = 0;
    state.locked = false;
    state.lockedUntil = null;
    state.lastMethod = "administrator";
    state.lastAttemptAt = now;

    this.appendAudit(
      this.makeAuditEntry(
        identity,
        state,
        "administrator",
        "unlock",
        now,
        false,
        false,
      ),
    );

    return {
      key: state.key,
      normalized: state.key,
      normalizedKey: state.key,
      accountKey: state.accountKey,
      kind: state.kind,
      display: state.display,
      displayValue: state.display,
      original: state.display,
      previousGeneration,
      generation: state.generation,
      failureCount: 0,
      consecutiveFailures: 0,
      locked: false,
      unlocked: true,
      stale: false,
      timestamp: now,
    };
  }

  adminUnlock(input: LockoutInput, expectedGeneration?: number): UnlockResult {
    return this.unlock(input, expectedGeneration);
  }

  administratorUnlock(input: LockoutInput, expectedGeneration?: number): UnlockResult {
    return this.unlock(input, expectedGeneration);
  }

  unlockAccount(input: LockoutInput, expectedGeneration?: number): UnlockResult {
    return this.unlock(input, expectedGeneration);
  }

  resetLockout(input: LockoutInput, expectedGeneration?: number): UnlockResult {
    return this.unlock(input, expectedGeneration);
  }

  reset(input: LockoutInput, expectedGeneration?: number): UnlockResult {
    return this.unlock(input, expectedGeneration);
  }

  getAuditLog(input?: LockoutInput): AuditEntry[] {
    if (input === undefined) {
      return this.audit.map((entry) => ({ ...entry }));
    }

    let identity: AccountIdentifier | undefined;
    try {
      identity = resolveAccountInput(input, undefined, this.normalizer).identity;
    } catch {
      identity = undefined;
    }
    return this.audit
      .filter((entry) => {
        if (identity !== undefined) {
          return entry.accountKey === internalAccountKey(identity!);
        }
        if (typeof input === "string") {
          return entry.key === input || entry.accountKey === input;
        }
        return entry.key === input.key || entry.accountKey === input.key;
      })
      .map((entry) => ({ ...entry }));
  }

  auditLog(input?: LockoutInput): AuditEntry[] {
    return this.getAuditLog(input);
  }

  getAuditEntries(input?: LockoutInput): AuditEntry[] {
    return this.getAuditLog(input);
  }

  getAuditTrail(input?: LockoutInput): AuditEntry[] {
    return this.getAuditLog(input);
  }

  clearAuditLog(): void {
    this.audit.length = 0;
  }

  getDisplayValue(input: LockoutInput): string | undefined {
    return this.getState(input)?.display;
  }

  listStates(): LockoutState[] {
    for (const state of this.states.values()) {
      this.refresh(state, this.now());
    }
    return Array.from(this.states.values()).map((state) => this.snapshot(state));
  }

  clear(): void {
    this.states.clear();
    this.audit.length = 0;
  }
}

export const LockoutTracker = AccountLockoutTracker;
export const LockoutStore = AccountLockoutTracker;
export const AccountLockoutStore = AccountLockoutTracker;
export const AccountLockoutManager = AccountLockoutTracker;
export const AccountLockoutPolicy = AccountLockoutTracker;
export const LockoutPolicy = AccountLockoutTracker;
export const LoginAttemptTracker = AccountLockoutTracker;
export const LoginLockoutTracker = AccountLockoutTracker;
export const AuthenticationLockoutTracker = AccountLockoutTracker;
export const AuthLockoutTracker = AccountLockoutTracker;
export const AccountLockout = AccountLockoutTracker;
export const LockoutCounter = AccountLockoutTracker;

export function createLockoutTracker(
  options: LockoutOptions = {},
): AccountLockoutTracker {
  return new AccountLockoutTracker(options);
}

export const createAccountLockoutTracker = createLockoutTracker;
export const createLockoutStore = createLockoutTracker;
export const createAccountLockout = createLockoutTracker;

/** Shared singleton used by the convenience functions below. */
export const defaultLockoutTracker = new AccountLockoutTracker();

function defaultAttempt(
  input: LockoutInput,
  methodOrOptions?: string | AttemptOptions,
  kind?: AccountKind,
) {
  return defaultLockoutTracker.recordFailure(input, methodOrOptions, kind);
}

export const recordFailure = defaultAttempt;
export const registerFailure = defaultAttempt;
export const recordFailedAttempt = defaultAttempt;
export const trackFailure = defaultAttempt;
export const recordLoginFailure = defaultAttempt;

function defaultSuccess(
  input: LockoutInput,
  methodOrOptions?: string | AttemptOptions,
  kind?: AccountKind,
) {
  return defaultLockoutTracker.recordSuccess(input, methodOrOptions, kind);
}

export const recordSuccess = defaultSuccess;
export const registerSuccess = defaultSuccess;
export const recordSuccessfulAttempt = defaultSuccess;
export const trackSuccess = defaultSuccess;
export const recordLoginSuccess = defaultSuccess;

export function isAccountLocked(input: LockoutInput, generation?: number): boolean {
  return defaultLockoutTracker.isLocked(input, generation);
}

export function checkLockout(input: LockoutInput, generation?: number): boolean {
  return defaultLockoutTracker.isLocked(input, generation);
}

export function adminUnlockAccount(
  input: LockoutInput,
  expectedGeneration?: number,
): UnlockResult {
  return defaultLockoutTracker.unlock(input, expectedGeneration);
}

export function unlockAccount(
  input: LockoutInput,
  expectedGeneration?: number,
): UnlockResult {
  return defaultLockoutTracker.unlock(input, expectedGeneration);
}

export function getLockoutState(input: LockoutInput): LockoutState | undefined {
  return defaultLockoutTracker.getState(input);
}

function defaultNormalize(
  raw: string,
  kindOrOptions: AccountKind | AccountNormalizeOptions = {},
): string {
  return normalizeAccountIdentifier(raw, kindOrOptions);
}

const accountIdentifierNormalizer = Object.assign(defaultNormalize, {
  AccountIdentifierError,
  AccountIdentifierNormalizer,
  AccountLockoutTracker,
  LockoutTracker,
  LockoutStore,
  AccountLockoutStore,
  AccountLockoutManager,
  AccountLockoutPolicy,
  AccountLockout,
  AuthLockoutTracker,
  AuthenticationLockoutTracker,
  LoginAttemptTracker,
  LoginLockoutTracker,
  LockoutCounter,
  LockoutPolicy,
  accountIdentity,
  accountKey,
  auditKey,
  adminUnlockAccount,
  canonicalize,
  canonicalizeAccountIdentifier,
  canonicalizeEmail,
  canonicalizeUsername,
  createAccountLockoutTracker,
  createAccountLockout,
  createAccountNormalizer,
  createLockoutTracker,
  createLockoutStore,
  DEFAULT_LOCKOUT_DURATION_MS,
  DEFAULT_LOCKOUT_THRESHOLD,
  defaultLockoutTracker,
  deriveLockoutKey,
  describeAccountIdentifier,
  detectAccountKind,
  getLockoutState,
  getCanonicalKey,
  identity,
  isAccountLocked,
  checkLockout,
  normalizeAccount,
  normalize,
  normalizeAccountId,
  normalizeAccountWithDisplay,
  normalizeAccountWithMetadata,
  normalizeAccountIdentifier,
  normalizeEmail,
  normalizeEmailWithDisplay,
  normalizeEmailAddress,
  normalizeIdentifier,
  normalizeIdentity,
  normalizeUser,
  normalizeUsername,
  normalizeUsernameWithDisplay,
  lockoutKey,
  parseEmail,
  parseAccountIdentifier,
  parseUsername,
  recordFailure,
  recordLoginFailure,
  recordLoginSuccess,
  recordSuccess,
  toAccountIdentifier,
  toAccountIdentity,
  unlockAccount,
});

export default accountIdentifierNormalizer;
