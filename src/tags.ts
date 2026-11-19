import { ProtocolError, SerializationError } from './errors.ts';

/**
 * A parsed message-tag collection. Tag values are stored *unescaped*; use
 * {@link serializeTags} to escape them again before writing to the wire.
 */
export type TagMap = Map<string, string | true>;

/**
 * Vendor/client tags are namespaced under a vendor prefix. Client-only tags
 * additionally carry a leading `+` before the vendor name, e.g.
 * `+example.com/foo`.
 */
const VALID_TAG_NAME = /^\+?[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*(\/[A-Za-z0-9-]+)*$/;

/**
 * Decode an IRCv3 message-tag value from its on-wire escaped form.
 *
 * Escapes (https://ircv3.net/specs/extensions/message-tags):
 *   `\:` -> `;`, `\s` -> space, `\\` -> `\`, `\r` -> CR, `\n` -> LF.
 * A trailing backslash and any other `\x` sequence are treated literally.
 */
export function unescapeTagValue(value: string): string {
  let out = '';
  for (let i = 0; i < value.length; i += 1) {
    const ch = value[i];
    if (ch !== '\\') {
      out += ch;
      continue;
    }
    const next = value[i + 1];
    if (next === undefined) {
      out += '\\';
      break;
    }
    switch (next) {
      case ':':
        out += ';';
        break;
      case 's':
        out += ' ';
        break;
      case 'r':
        out += '\r';
        break;
      case 'n':
        out += '\n';
        break;
      case '\\':
        out += '\\';
        break;
      default:
        out += ch + next;
        break;
    }
    i += 1;
  }
  return out;
}

/**
 * Encode a tag value for the wire. Every character that has tag-escaping
 * meaning is escaped, so a value containing `;`, a space, CR, LF or a
 * backslash can never introduce an extra tag or break out of the tag section.
 */
export function escapeTagValue(value: string): string {
  let out = '';
  for (const ch of value) {
    switch (ch) {
      case ';':
        out += '\\:';
        break;
      case ' ':
        out += '\\s';
        break;
      case '\\':
        out += '\\\\';
        break;
      case '\r':
        out += '\\r';
        break;
      case '\n':
        out += '\\n';
        break;
      default:
        if (ch === '\0') {
          throw new SerializationError('NUL byte is not allowed in a tag value');
        }
        out += ch;
        break;
    }
  }
  return out;
}

function assertValidTagName(name: string): void {
  if (!VALID_TAG_NAME.test(name)) {
    throw new ProtocolError(`invalid message-tag name: ${JSON.stringify(name)}`);
  }
}

/**
 * Parse the content of a `@` tag section (without the leading `@` and the
 * trailing space). Values are unescaped. A bare tag name yields `true`.
 */
export function parseTagString(tagString: string): TagMap {
  const tags: TagMap = new Map();
  if (tagString.length === 0) {
    throw new ProtocolError('empty message-tag section');
  }
  for (const item of splitTagEntries(tagString)) {
    if (item.length === 0) {
      throw new ProtocolError('empty message-tag entry');
    }
    const eq = item.indexOf('=');
    const name = eq === -1 ? item : item.slice(0, eq);
    assertValidTagName(name);
    if (eq === -1) {
      tags.set(name, true);
    } else {
      tags.set(name, unescapeTagValue(item.slice(eq + 1)));
    }
  }
  return tags;
}

/**
 * Split a tag section on unescaped semicolons only. A semicolon inside an
 * escaped value (`\:` on the wire) must never start a new tag, which is the
 * core injection guard for relayed tag values.
 */
function splitTagEntries(tagString: string): string[] {
  const entries: string[] = [];
  let start = 0;
  for (let i = 0; i < tagString.length; i += 1) {
    const ch = tagString[i];
    if (ch === '\\') {
      i += 1;
      continue;
    }
    if (ch === ';') {
      entries.push(tagString.slice(start, i));
      start = i + 1;
    }
  }
  entries.push(tagString.slice(start));
  return entries;
}

/**
 * Serialize a tag collection into the content of a `@` section (without the
 * leading `@`). Values are escaped per the message-tags specification.
 */
export function serializeTags(tags: TagMap | Record<string, string | true>): string {
  const entries = tags instanceof Map ? [...tags.entries()] : Object.entries(tags);
  if (entries.length === 0) {
    return '';
  }
  const parts: string[] = [];
  for (const [name, value] of entries) {
    assertValidTagName(name);
    if (value === true) {
      parts.push(name);
    } else {
      parts.push(`${name}=${escapeTagValue(value)}`);
    }
  }
  return parts.join(';');
}

/**
 * Keep only client-only tags, i.e. tags whose name starts with `+`. These are
 * the only tags clients are allowed to send and the server to relay.
 */
export function selectClientTags(tags: TagMap): TagMap {
  const result: TagMap = new Map();
  for (const [name, value] of tags) {
    if (name.startsWith('+')) {
      result.set(name, value);
    }
  }
  return result;
}
