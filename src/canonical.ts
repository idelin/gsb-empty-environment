import type { CanonicalEventTuple } from './types.ts';

/**
 * The only characters NIP-01 permits escaping inside a JSON string:
 * newline, double quote, backslash, carriage return, tab, backspace and
 * form feed. Every other code unit is emitted verbatim, including control
 * characters such as U+0000 and the solidus `/`, which MUST NOT be turned
 * into `\/`.
 */
function escapeJsonString(value: string): string {
  let out = '"';
  for (const ch of value) {
    switch (ch) {
      case '\n':
        out += '\\n';
        break;
      case '"':
        out += '\\"';
        break;
      case '\\':
        out += '\\\\';
        break;
      case '\r':
        out += '\\r';
        break;
      case '\t':
        out += '\\t';
        break;
      case '\b':
        out += '\\b';
        break;
      case '\f':
        out += '\\f';
        break;
      default:
        out += ch;
    }
  }
  return out + '"';
}

function serializeTags(
  tags: ReadonlyArray<ReadonlyArray<string>>,
): string {
  let out = '[';
  for (let i = 0; i < tags.length; i++) {
    if (i > 0) {
      out += ',';
    }
    const tag = tags[i];
    out += '[';
    for (let j = 0; j < tag.length; j++) {
      if (j > 0) {
        out += ',';
      }
      out += escapeJsonString(tag[j]);
    }
    out += ']';
  }
  return out + ']';
}

/**
 * Serializes the fixed NIP-01 tuple with no whitespace anywhere and the
 * restricted escape set above. Producing this string ourselves — instead of
 * trusting the sender's wire bytes or a generic JSON encoder — is what makes
 * the id binding unambiguous: two events with the same logical content always
 * hash to the same id, and an id computed with a non-canonical escape style
 * (e.g. `\\u000a` or `\\/`) fails verification.
 */
export function serializeCanonicalEvent(
  tuple: CanonicalEventTuple,
): string {
  const [, pubkey, createdAt, kind, tags, content] = tuple;
  return (
    '[' +
    '0,' +
    escapeJsonString(pubkey) +
    ',' +
    String(createdAt) +
    ',' +
    String(kind) +
    ',' +
    serializeTags(tags) +
    ',' +
    escapeJsonString(content) +
    ']'
  );
}
