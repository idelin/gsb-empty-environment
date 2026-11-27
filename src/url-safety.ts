import { NAMED_ENTITIES, NUMERIC_REFERENCE_OVERRIDES } from './entities.ts';

const CONTROL_AND_SPACE = /[\u0000-\u0020\u007F\u00A0\uFEFF]/g;

/**
 * Remove every character HTML treats as ignorable when locating a URL
 * scheme: ASCII whitespace, C0 controls, NBSP and the BOM. The check runs
 * before any scheme inspection so tab/newline/control obfuscation cannot
 * sneak past a prefix comparison.
 */
export function stripIgnorableUrlChars(value: string): string {
  return value.replace(CONTROL_AND_SPACE, '');
}

function decodeEntitiesOnce(value: string): string {
  let output = '';
  for (let i = 0; i < value.length; i += 1) {
    const ch = value[i];
    if (ch !== '&') {
      output += ch;
      continue;
    }
    const rest = value.slice(i + 1);
    const numeric = /^#(?:[xX]([0-9a-fA-F]{1,8})|([0-9]{1,8}));?/.exec(rest);
    if (numeric) {
      const codePoint = numeric[1] !== undefined
        ? parseInt(numeric[1], 16)
        : parseInt(numeric[2], 10);
      const override = NUMERIC_REFERENCE_OVERRIDES[codePoint];
      output += codePointToString(override ?? codePoint);
      i += numeric[0].length;
      continue;
    }
    const named = /^([A-Za-z][A-Za-z0-9]{1,31});?/.exec(rest);
    if (named && NAMED_ENTITIES[named[1]] !== undefined) {
      output += NAMED_ENTITIES[named[1]];
      i += named[0].length;
      continue;
    }
    output += ch;
  }
  return output;
}

function codePointToString(codePoint: number): string {
  if (Number.isNaN(codePoint) || codePoint > 0x10ffff ||
    (codePoint >= 0xd800 && codePoint <= 0xdfff)) {
    return '�';
  }
  try {
    return String.fromCodePoint(codePoint);
  } catch {
    return '�';
  }
}

const DANGEROUS_SCHEMES = new Set([
  'javascript:', 'vbscript:', 'livescript:', 'mocha:', 'data:',
]);

export interface UrlCheckResult {
  safe: boolean;
  /** The normalized URL used for the decision. */
  normalized: string;
  /** Detected scheme including the trailing colon, when present. */
  scheme?: string;
}

/**
 * Decide whether a URI-attribute value is safe. The pipeline:
 *   raw value -> decode entities once -> strip ignorable chars ->
 *   decode entities again -> strip again -> scheme decision
 * Double decoding catches `&amp;#106;...` style nested obfuscation, while
 * explicit dangerous-scheme blocking handles every case an allow-list might
 * fail to classify.
 */
export function checkUrl(
  rawValue: string,
  allowedProtocols: ReadonlySet<string>,
): UrlCheckResult {
  const firstPass = stripIgnorableUrlChars(decodeEntitiesOnce(rawValue));
  const secondPass = stripIgnorableUrlChars(decodeEntitiesOnce(firstPass));
  const normalized = secondPass;

  if (normalized.includes('�')) {
    return { safe: false, normalized };
  }

  const schemeMatch = /^([a-z][a-z0-9+.\-]*):/i.exec(normalized);
  if (!schemeMatch) {
    // Relative / fragment / protocol-relative URLs are acceptable as long as
    // they do not embed a dangerous marker after decoding.
    return { safe: true, normalized };
  }

  const scheme = schemeMatch[1].toLowerCase() + ':';
  if (DANGEROUS_SCHEMES.has(scheme)) {
    return { safe: false, normalized, scheme };
  }
  return {
    safe: allowedProtocols.has(scheme),
    normalized,
    scheme,
  };
}

/**
 * Conservative guard for the optional `style` attribute. Rejects anything
 * containing URL tokens, expression/script constructs or comment markers.
 */
export function isSafeStyleAttribute(value: string): boolean {
  const decoded = decodeEntitiesOnce(value);
  const lowered = decoded.toLowerCase();
  if (decoded.includes('�')) {
    return false;
  }
  if (lowered.includes('url(') || lowered.includes('@import') ||
    lowered.includes('expression(') || lowered.includes('javascript:') ||
    lowered.includes('vbscript:') || lowered.includes('data:') ||
    lowered.includes('<!--') || lowered.includes('-->') ||
    lowered.includes('</')) {
    return false;
  }
  return /^[\w\- ,.#%()'"+\u0080-\uFFFF:;=!*\/]*$/.test(decoded);
}
