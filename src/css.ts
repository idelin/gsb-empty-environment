/**
 * Zero-dependency CSS lexical / syntax security auditor for user-controlled
 * style fragments.
 *
 * Two complementary layers:
 *
 *  1. An obfuscation-resistant normalized text scan that first strips comments,
 *     removes line continuations and decodes CSS escape sequences (including
 *     `\75 rl(`, `\000075rl(`, comment-split and newline-split payloads), then
 *     hard-blocks `@import`, `expression()`, `-moz-binding`, dangerous schemes
 *     and external `url()` against an origin allowlist.
 *  2. A hand-written CSS tokenizer / parser (CSS Syntax 3 style) that recovers
 *     selectors and declarations so attribute-selector exfiltration can be
 *     detected: sensitive attribute selectors (`[value^=...]`,
 *     `[name*=token]`, ...) that load external resources via `url()` or
 *     `@font-face`.
 */

import type { Severity } from './csp.ts';

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export interface CssFinding {
  code:
    | 'AT_IMPORT'
    | 'CSS_EXPRESSION'
    | 'MOZ_BINDING'
    | 'EXTERNAL_URL'
    | 'DANGEROUS_SCHEME'
    | 'ATTRIBUTE_EXFIL'
    | 'FONT_FACE_EXFIL'
    | 'DATA_URL_BLOCKED';
  severity: Severity;
  message: string;
  /** Offset in the ORIGINAL source (best-effort mapped from normalized text). */
  offset: number;
  snippet: string;
}

export interface CssAuditOptions {
  /** Allowed hosts (exact match) or suffixes starting with `.` (sub-domains). */
  allowedHosts?: string[];
  /** Block `data:` URLs entirely (default false: data: is same-origin-safe). */
  blockDataUrls?: boolean;
  /** Attribute names that carry secrets / tokens (lower-cased). */
  sensitiveAttributes?: string[];
}

export interface CssAuditResult {
  allowed: boolean;
  findings: CssFinding[];
  /** Comment-stripped, escape-decoded representation used by layer 1. */
  normalized: string;
}

export const DEFAULT_SENSITIVE_ATTRIBUTES: readonly string[] = [
  'value',
  'name',
  'id',
  'csrf',
  'csrf-token',
  'csrfmiddlewaretoken',
  'authenticity_token',
  'token',
  'x-csrf-token',
  'x-xsrf-token',
  'xsrf',
  'secret',
  'password',
  'apikey',
  'api-key',
  'session',
];

// ---------------------------------------------------------------------------
// Normalization: comment stripping, line continuation, escape decoding
// ---------------------------------------------------------------------------

export interface NormalizedCss {
  text: string;
  /** normalized offset -> original offset mapping. */
  map: number[];
}

const isHex = (char: string): boolean => /[0-9a-fA-F]/.test(char);

/**
 * Produce an obfuscation-resistant view of the CSS while retaining a mapping
 * back to the original offsets:
 *  - comments `/* ... *\/` are removed (also defeats comment splitting);
 *  - backslash + newline line continuations are removed;
 *  - CSS escape sequences (`\75 `, `\75rl`, `\000075`, `\2d `, `\6d ...) are
 *    decoded to their literal characters, matching browser interpretation.
 */
export function normalizeCss(input: string): NormalizedCss {
  let text = '';
  const map: number[] = [];
  const emit = (char: string, originalIndex: number): void => {
    text += char;
    map.push(originalIndex);
  };

  let i = 0;
  const length = input.length;

  while (i < length) {
    // Strip comments, including unterminated ones (CSS treats EOF as end).
    if (input[i] === '/' && input[i + 1] === '*') {
      i += 2;
      while (i < length && !(input[i] === '*' && input[i + 1] === '/')) i++;
      i = Math.min(length, i + 2);
      continue;
    }

    // Line continuation: backslash followed by newline sequence.
    if (
      input[i] === '\\' &&
      (input[i + 1] === '\n' ||
        input[i + 1] === '\r' ||
        input[i + 1] === '\f')
    ) {
      i += input[i + 1] === '\r' && input[i + 2] === '\n' ? 3 : 2;
      continue;
    }

    // CSS escape sequence.
    if (input[i] === '\\') {
      const start = i;
      i++;
      let hex = '';
      while (i < length && isHex(input[i]) && hex.length < 6) {
        hex += input[i];
        i++;
      }
      if (hex.length > 0) {
        // Single whitespace after a hex escape is part of the escape.
        if (i < length && /[ \t\n\r\f]/.test(input[i])) i++;
        const codePoint = parseInt(hex, 16);
        if (Number.isFinite(codePoint) && codePoint > 0) {
          emit(String.fromCodePoint(codePoint), start);
        }
        continue;
      }
      if (i < length) {
        // Non-hex escape: the next char is literal (`\(`, `\u`, ...).
        emit(input[i], start);
        i++;
        continue;
      }
      continue;
    }

    emit(input[i], i);
    i++;
  }

  return { text, map };
}

// ---------------------------------------------------------------------------
// Tokenizer (CSS Syntax Level 3 subset, zero dependencies)
// ---------------------------------------------------------------------------

export type CssTokenType =
  | 'ident'
  | 'function'
  | 'at-keyword'
  | 'hash'
  | 'string'
  | 'url'
  | 'number'
  | 'percentage'
  | 'dimension'
  | 'delim'
  | 'whitespace'
  | 'colon'
  | 'semicolon'
  | 'bracket'
  | 'brace'
  | 'paren'
  | 'comma';

export interface CssToken {
  type: CssTokenType;
  value: string;
  /** 'open' | 'close' for bracket/brace/paren tokens. */
  bracket?: 'open' | 'close';
  offset: number;
}

const NAME_START = /[a-zA-Z_\\\u00A0-\uFFFF]/;
const NAME_CODE = /[a-zA-Z0-9_\-\\\u00A0-\uFFFF]/;

export function tokenizeCss(input: string): CssToken[] {
  const tokens: CssToken[] = [];
  let i = 0;
  const length = input.length;

  const isNameStart = (char: string | undefined): boolean =>
    !!char && NAME_START.test(char);
  const isNameCode = (char: string | undefined): boolean =>
    !!char && NAME_CODE.test(char);

  const consumeName = (): string => {
    let value = '';
    while (i < length && isNameCode(input[i])) {
      value += input[i];
      i++;
    }
    return value;
  };

  const consumeString = (quote: string): string => {
    let value = '';
    i++; // opening quote
    while (i < length && input[i] !== quote) {
      if (input[i] === '\\' && i + 1 < length) {
        value += input[i] + input[i + 1];
        i += 2;
        continue;
      }
      if (input[i] === '\n' || input[i] === '\r' || input[i] === '\f') {
        break; // bad string on raw newline
      }
      value += input[i];
      i++;
    }
    if (i < length && input[i] === quote) i++;
    return value;
  };

  while (i < length) {
    const char = input[i];

    if (char === ' ' || char === '\t' || char === '\n' || char === '\r' || char === '\f') {
      const start = i;
      while (i < length && /[ \t\n\r\f]/.test(input[i])) i++;
      tokens.push({ type: 'whitespace', value: input.slice(start, i), offset: start });
      continue;
    }

    if (char === '"' || char === "'") {
      const start = i;
      const value = consumeString(char);
      tokens.push({ type: 'string', value, offset: start });
      continue;
    }

    if (char === '#') {
      const start = i;
      i++;
      if (isNameCode(input[i]) || input[i] === '\\') {
        const value = consumeName();
        tokens.push({ type: 'hash', value, offset: start });
        continue;
      }
      tokens.push({ type: 'delim', value: char, offset: start });
      continue;
    }

    if (char === '@') {
      const start = i;
      i++;
      if (isNameStart(input[i])) {
        const value = consumeName();
        tokens.push({ type: 'at-keyword', value, offset: start });
        continue;
      }
      tokens.push({ type: 'delim', value: char, offset: start });
      continue;
    }

    if (char === '+' || char === '-' || char === '.' || /[0-9]/.test(char)) {
      const start = i;
      const numeric = /^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?/.exec(
        input.slice(i),
      );
      if (numeric && numeric[0].length > 0) {
        i += numeric[0].length;
        if (input[i] === '%') {
          i++;
          tokens.push({ type: 'percentage', value: numeric[0], offset: start });
        } else if (isNameStart(input[i])) {
          const unit = consumeName();
          tokens.push({ type: 'dimension', value: `${numeric[0]}${unit}`, offset: start });
        } else {
          tokens.push({ type: 'number', value: numeric[0], offset: start });
        }
        continue;
      }
    }

    if (isNameStart(char) || (char === '-' && isNameStart(input[i + 1]))) {
      const start = i;
      const value = consumeName();
      if (input[i] === '(') {
        i++;
        tokens.push({ type: 'function', value: `${value}(`, offset: start });
      } else {
        tokens.push({ type: 'ident', value, offset: start });
      }
      continue;
    }

    switch (char) {
      case ':':
        tokens.push({ type: 'colon', value: char, offset: i });
        i++;
        continue;
      case ';':
        tokens.push({ type: 'semicolon', value: char, offset: i });
        i++;
        continue;
      case ',':
        tokens.push({ type: 'comma', value: char, offset: i });
        i++;
        continue;
      case '[':
        tokens.push({ type: 'bracket', value: char, bracket: 'open', offset: i });
        i++;
        continue;
      case ']':
        tokens.push({ type: 'bracket', value: char, bracket: 'close', offset: i });
        i++;
        continue;
      case '{':
        tokens.push({ type: 'brace', value: char, bracket: 'open', offset: i });
        i++;
        continue;
      case '}':
        tokens.push({ type: 'brace', value: char, bracket: 'close', offset: i });
        i++;
        continue;
      case '(':
        tokens.push({ type: 'paren', value: char, bracket: 'open', offset: i });
        i++;
        continue;
      case ')':
        tokens.push({ type: 'paren', value: char, bracket: 'close', offset: i });
        i++;
        continue;
      default:
        tokens.push({ type: 'delim', value: char, offset: i });
        i++;
        continue;
    }
  }

  return tokens;
}

// ---------------------------------------------------------------------------
// Lightweight syntax parser: rules, at-rules, declarations
// ---------------------------------------------------------------------------

export interface CssDeclaration {
  property: string;
  value: string;
  offset: number;
}

export interface CssRule {
  type: 'qualified' | 'at-rule';
  /** Lower-cased at-rule name without `@`, undefined for qualified rules. */
  atName?: string;
  /** Raw prelude text (selector list or at-rule prelude). */
  prelude: string;
  preludeOffset: number;
  declarations: CssDeclaration[];
  /** Nested rules for block at-rules such as @media / @font-face. */
  rules: CssRule[];
  offset: number;
}

export interface CssStylesheet {
  rules: CssRule[];
}

export function parseCss(input: string): CssStylesheet {
  const tokens = tokenizeCss(input);
  let index = 0;

  const readPrelude = (): { prelude: string; offset: number } => {
    const start = tokens[index]?.offset ?? 0;
    const parts: string[] = [];
    let depth = 0;
    while (index < tokens.length) {
      const token = tokens[index];
      if (token.type === 'brace' && token.bracket === 'open' && depth === 0) break;
      if (token.type === 'semicolon' && depth === 0) break;
      if (token.type === 'paren' || token.type === 'bracket' || token.type === 'function') {
        if (token.type === 'function' || token.bracket === 'open') depth++;
        else depth--;
      }
      parts.push(token.value);
      index++;
    }
    return { prelude: parts.join('').trim(), offset: start };
  };

  const parseDeclarations = (): CssDeclaration[] => {
    const declarations: CssDeclaration[] = [];
    // Tracks braces of nested blocks (e.g. nested at-rules) encountered inside
    // this declaration block. The first closing brace at depth 0 belongs to
    // this block and must be left for the caller.
    let nestedBraceDepth = 0;
    while (index < tokens.length) {
      const token = tokens[index];
      if (token.type === 'whitespace' || token.type === 'semicolon') {
        index++;
        continue;
      }
      if (token.type === 'brace' && token.bracket === 'close' && nestedBraceDepth === 0) {
        // Closing brace of THIS block: stop without consuming it; the rule
        // parser owns block boundary consumption.
        return declarations;
      }
      if (token.type === 'at-keyword') {
        // At-rule nested inside a block: skip prelude and any block body.
        index++;
        while (
          index < tokens.length &&
          !(
            tokens[index].type === 'semicolon' ||
            (tokens[index].type === 'brace' && tokens[index].bracket === 'open')
          )
        ) {
          index++;
        }
        if (tokens[index]?.type === 'brace') {
          index++;
          let depth = 1;
          while (index < tokens.length && depth > 0) {
            if (tokens[index].type === 'brace') {
              depth += tokens[index].bracket === 'open' ? 1 : -1;
            }
            index++;
          }
        } else {
          index++;
        }
        continue;
      }
      if (token.type !== 'ident') {
        index++;
        continue;
      }

      const property = token.value;
      const declOffset = token.offset;
      index++;
      while (index < tokens.length && tokens[index].type === 'whitespace') index++;
      if (tokens[index]?.type !== 'colon') {
        continue;
      }
      index++;
      const valueParts: string[] = [];
      let depth = 0;
      while (index < tokens.length) {
        const current = tokens[index];
        if (current.type === 'semicolon' && depth === 0) {
          index++;
          break;
        }
        if (current.type === 'brace' && current.bracket === 'close' && depth === 0) {
          break;
        }
        if (
          current.type === 'paren' ||
          current.type === 'bracket' ||
          current.type === 'function'
        ) {
          if (current.type === 'function' || current.bracket === 'open') depth++;
          else depth--;
        }
        valueParts.push(current.value);
        index++;
      }
      declarations.push({
        property: property.toLowerCase(),
        value: valueParts.join('').trim(),
        offset: declOffset,
      });
    }
    return declarations;
  };

  const parseRule = (): CssRule | undefined => {
    while (index < tokens.length && tokens[index].type === 'whitespace') index++;
    if (index >= tokens.length) return undefined;

    const ruleOffset = tokens[index].offset;
    let atName: string | undefined;
    if (tokens[index].type === 'at-keyword') {
      atName = tokens[index].value.toLowerCase();
      index++;
    }

    const { prelude, offset } = readPrelude();

    // Statement at-rule without a block (`@import x;`).
    if (tokens[index]?.type === 'semicolon') {
      index++;
      return {
        type: 'at-rule',
        atName,
        prelude,
        preludeOffset: offset,
        declarations: [],
        rules: [],
        offset: ruleOffset,
      };
    }

    // Block rule.
    if (tokens[index]?.type === 'brace' && tokens[index].bracket === 'open') {
      index++;
      if (atName === 'font-face' || atName === 'page' || !atName) {
        const declarations = parseDeclarations();
        // Consume this block's own closing brace.
        if (tokens[index]?.type === 'brace' && tokens[index].bracket === 'close') {
          index++;
        }
        return {
          type: atName ? 'at-rule' : 'qualified',
          atName,
          prelude,
          preludeOffset: offset,
          declarations,
          rules: [],
          offset: ruleOffset,
        };
      }
      // Conditional group at-rule (@media, @supports, ...): parse nested rules.
      const nested: CssRule[] = [];
      while (index < tokens.length) {
        if (tokens[index].type === 'whitespace' || tokens[index].type === 'semicolon') {
          index++;
          continue;
        }
        if (tokens[index].type === 'brace' && tokens[index].bracket === 'close') {
          break;
        }
        const child = parseRule();
        if (child) nested.push(child);
        else index++;
      }
      // Consume the conditional group's own closing brace.
      if (tokens[index]?.type === 'brace') index++;
      return {
        type: 'at-rule',
        atName,
        prelude,
        preludeOffset: offset,
        declarations: [],
        rules: nested,
        offset: ruleOffset,
      };
    }

    return undefined;
  };

  const rules: CssRule[] = [];
  while (index < tokens.length) {
    const rule = parseRule();
    if (rule) rules.push(rule);
    else index++;
  }

  return { rules };
}

// ---------------------------------------------------------------------------
// URL allowlist handling
// ---------------------------------------------------------------------------

export type UrlVerdict =
  | 'relative'
  | 'allowed-scheme'
  | 'blocked-scheme'
  | 'external-allowed'
  | 'external-blocked';

export interface UrlEvaluation {
  verdict: UrlVerdict;
  host?: string;
}

function hostAllowed(host: string, allowedHosts: readonly string[]): boolean {
  const normalized = host.toLowerCase();
  return allowedHosts.some((entry) => {
    const allowed = entry.toLowerCase();
    if (allowed.startsWith('.')) {
      return normalized === allowed.slice(1) || normalized.endsWith(allowed);
    }
    return normalized === allowed;
  });
}

/**
 * Classify a URL extracted from `url(...)` against the configured origin
 * allowlist. Relative URLs are treated as same-origin and allowed; `data:`,
 * `blob:` and `about:` are non-network schemes; `javascript:` / `vbscript:`
 * are always blocked.
 */
export function evaluateUrl(rawUrl: string, options: CssAuditOptions): UrlEvaluation {
  const url = rawUrl.trim().replace(/^["']|["']$/g, '');
  if (url === '') return { verdict: 'relative' };

  if (url.startsWith('#') || url.startsWith('/') || !/^[a-z][a-z0-9+.-]*:/i.test(url)) {
    return { verdict: 'relative' };
  }

  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return { verdict: 'external-blocked' };
  }

  const scheme = parsed.protocol.replace(':', '').toLowerCase();
  if (scheme === 'javascript' || scheme === 'vbscript') {
    return { verdict: 'blocked-scheme', host: parsed.hostname };
  }
  if (scheme === 'data' || scheme === 'blob' || scheme === 'about') {
    if (scheme === 'data' && options.blockDataUrls) {
      return { verdict: 'blocked-scheme', host: parsed.hostname };
    }
    return { verdict: 'allowed-scheme', host: parsed.hostname };
  }

  const allowedHosts = options.allowedHosts ?? [];
  if (hostAllowed(parsed.hostname, allowedHosts)) {
    return { verdict: 'external-allowed', host: parsed.hostname };
  }
  return { verdict: 'external-blocked', host: parsed.hostname };
}

// ---------------------------------------------------------------------------
// Attribute-selector exfiltration analysis
// ---------------------------------------------------------------------------

const ATTRIBUTE_SELECTOR_REGEX = /\[\s*(-{0,2}[a-zA-Z_][a-zA-Z0-9_-]*)(?:[^[\]]*)?\]/g;

export interface AttributeSelectorHit {
  attribute: string;
  raw: string;
  offset: number;
}

/** Extract sensitive attribute selectors from a (normalized) selector text. */
export function findSensitiveAttributeSelectors(
  selector: string,
  sensitive: readonly string[],
): AttributeSelectorHit[] {
  const hits: AttributeSelectorHit[] = [];
  ATTRIBUTE_SELECTOR_REGEX.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = ATTRIBUTE_SELECTOR_REGEX.exec(selector)) !== null) {
    const attribute = match[1].replace(/^--?/, '').toLowerCase();
    if (sensitive.includes(attribute)) {
      hits.push({
        attribute,
        raw: match[0],
        offset: match.index,
      });
    }
  }
  return hits;
}

const URL_VALUE_REGEX = /url\(\s*(?:(["'])(.*?)\1|([^)\s"'][^)]*?))\s*\)/gi;

function extractUrls(text: string): { url: string; index: number }[] {
  const results: { url: string; index: number }[] = [];
  URL_VALUE_REGEX.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = URL_VALUE_REGEX.exec(text)) !== null) {
    results.push({ url: (match[2] ?? match[3] ?? '').trim(), index: match.index });
  }
  return results;
}

function containsExternalUrl(text: string, options: CssAuditOptions): boolean {
  return extractUrls(text).some(({ url }) => {
    const evaluation = evaluateUrl(url, options);
    return (
      evaluation.verdict === 'external-blocked' ||
      evaluation.verdict === 'blocked-scheme'
    );
  });
}

// ---------------------------------------------------------------------------
// Main audit entry
// ---------------------------------------------------------------------------

function snippetAround(input: string, offset: number, radius = 48): string {
  const start = Math.max(0, offset - radius);
  const end = Math.min(input.length, offset + radius);
  return input
    .slice(start, end)
    .replace(/\s+/g, ' ')
    .trim();
}

function walkRules(rules: CssRule[], callback: (rule: CssRule) => void): void {
  for (const rule of rules) {
    callback(rule);
    if (rule.rules.length > 0) walkRules(rule.rules, callback);
  }
}

/**
 * Audit a user-controlled CSS fragment.
 *
 * @returns `allowed === true` only when no finding was produced. Findings
 * cover hard blocks (`@import`, `expression()`, `-moz-binding`, external
 * `url()`) and CSS exfiltration via sensitive attribute selectors / fonts.
 */
export function auditCss(input: string, options: CssAuditOptions = {}): CssAuditResult {
  const sensitive = options.sensitiveAttributes ?? DEFAULT_SENSITIVE_ATTRIBUTES;
  const findings: CssFinding[] = [];
  const seen = new Set<string>();

  const normalized = normalizeCss(input);
  const toOriginalOffset = (normalizedOffset: number): number =>
    normalized.map[normalizedOffset] ?? Math.max(0, input.length - 1);

  const add = (
    finding: Omit<CssFinding, 'offset' | 'snippet'> & {
      normalizedOffset?: number;
      originalOffset?: number;
    },
  ): void => {
    const offset =
      finding.originalOffset ??
      toOriginalOffset(finding.normalizedOffset ?? 0);
    const snippet = snippetAround(input, offset);
    const key = `${finding.code}|${offset}|${snippet}`;
    if (seen.has(key)) return;
    seen.add(key);
    const { normalizedOffset: _n, originalOffset: _o, ...rest } = finding;
    void _n;
    void _o;
    findings.push({ ...rest, offset, snippet });
  };

  // --- Layer 1: obfuscation-resistant normalized text scan ---------------
  const scanRegex = (
    regex: RegExp,
    build: (match: RegExpExecArray) => Omit<CssFinding, 'offset' | 'snippet'>,
  ): void => {
    regex.lastIndex = 0;
    let match: RegExpExecArray | null;
    while ((match = regex.exec(normalized.text)) !== null) {
      add({ ...build(match), normalizedOffset: match.index });
      if (match[0].length === 0) regex.lastIndex++;
    }
  };

  scanRegex(/@import\b/gi, () => ({
    code: 'AT_IMPORT',
    severity: 'high',
    message:
      '@import is forbidden: it can pull external stylesheets and bypass origin scoping',
  }));

  scanRegex(/expression\s*\(/gi, () => ({
    code: 'CSS_EXPRESSION',
    severity: 'high',
    message: 'CSS expression() enables script execution in legacy/embedded engines',
  }));

  scanRegex(/-moz-binding\s*:/gi, () => ({
    code: 'MOZ_BINDING',
    severity: 'high',
    message:
      '-moz-binding loads external XBL and is a known remote code execution vector',
  }));

  for (const { url, index } of extractUrls(normalized.text)) {
    const evaluation = evaluateUrl(url, options);
    if (evaluation.verdict === 'blocked-scheme') {
      add({
        code: url.trim().replace(/^["']|["']$/g, '').toLowerCase().startsWith('data:')
          ? 'DATA_URL_BLOCKED'
          : 'DANGEROUS_SCHEME',
        severity: 'high',
        message: url
            .trim()
            .replace(/^["']|["']$/g, '')
            .toLowerCase()
            .startsWith('data:')
          ? `data: url() is blocked by policy in "${url}"`
          : `url() uses executable scheme in "${url}"`,
        normalizedOffset: index,
      });
    } else if (evaluation.verdict === 'external-blocked') {
      add({
        code: evaluation.host ? 'EXTERNAL_URL' : 'DANGEROUS_SCHEME',
        severity: 'high',
        message:
          evaluation.host
            ? `url() points to non-allowlisted external host "${evaluation.host}" in "${url}"`
            : `url() target "${url}" cannot be resolved against the allowlist`,
        normalizedOffset: index,
      });
    }
  }

  // --- Layer 2: structural analysis for exfiltration ----------------------
  const stylesheet = parseCss(input);
  let sensitiveSelectorPresent = false;
  let externalFontFace = false;

  walkRules(stylesheet.rules, (rule) => {
    // @font-face with an external src is itself an exfiltration channel.
    if (rule.type === 'at-rule' && rule.atName === 'font-face') {
      for (const declaration of rule.declarations) {
        if (declaration.property !== 'src') continue;
        const normalizedValue = normalizeCss(declaration.value).text;
        if (containsExternalUrl(normalizedValue, options)) {
          externalFontFace = true;
          add({
            code: 'FONT_FACE_EXFIL',
            severity: 'medium',
            message:
              '@font-face src loads an external resource; font requests can exfiltrate page data via the request path',
            originalOffset: rule.offset,
          });
        }
      }
    }

    if (rule.type !== 'qualified') return;

    // prelude offsets come from the original tokenizer; run the selector
    // detection on the original (obfuscated) prelude via normalized decoding
    // but keep offsets in the original coordinate space.
    const normalizedPrelude = normalizeCss(rule.prelude);
    const selectorHits = findSensitiveAttributeSelectors(
      normalizedPrelude.text,
      sensitive,
    );
    if (selectorHits.length === 0) return;
    sensitiveSelectorPresent = true;

    const loadsExternal = rule.declarations.some((declaration) =>
      containsExternalUrl(normalizeCss(declaration.value).text, options),
    );
    if (loadsExternal) {
      for (const hit of selectorHits) {
        add({
          code: 'ATTRIBUTE_EXFIL',
          severity: 'high',
          message: `Sensitive attribute selector "${hit.raw}" combined with an external url() performs CSS token exfiltration of the ${hit.attribute} value`,
          originalOffset:
            rule.preludeOffset + (normalizedPrelude.map[hit.offset] ?? 0),
        });
      }
    }
  });

  // Sensitive selectors anywhere + external @font-face = exfiltration chain.
  if (sensitiveSelectorPresent && externalFontFace) {
    add({
      code: 'ATTRIBUTE_EXFIL',
      severity: 'high',
      message:
        'Sensitive attribute selectors combined with an external @font-face src enable CSS token exfiltration',
      normalizedOffset: 0,
    });
  }

  return {
    allowed: findings.length === 0,
    findings,
    normalized: normalized.text,
  };
}
