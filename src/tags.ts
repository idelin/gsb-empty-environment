import { ProtocolError } from "./errors.ts";

/**
 * Parsed message tags keyed by their full tag name (including any vendor
 * prefix and/or leading `+` for client-only tags). A missing value is
 * represented by `true`; present values are always stored *unescaped*.
 */
export type TagMap = Map<string, string | true>;

// tag-name = [ vendor "/" ] key
// vendor   = host
// key      = 1*<a-z A-Z 0-9 -> . _ +>
// A leading '+' on the key marks a client-only tag; it is part of the name.
const VENDOR = "[A-Za-z0-9.\\-]+";
const KEY = "[+]?[A-Za-z0-9.\\-_]+";
const TAG_NAME_RE = new RegExp(`^(?:${VENDOR}/)?${KEY}$`);

/**
 * Unescape a tag value per the message-tags specification.
 *
 *   \: -> ;    \s -> SPACE    \\ -> \    \r -> CR    \n -> LF
 *
 * Any other backslash sequence drops the backslash (e.g. `\x` -> `x`), and a
 * trailing backslash is dropped as well.
 */
export function unescapeTagValue(raw: string): string {
  let out = "";
  for (let i = 0; i < raw.length; i += 1) {
    const char = raw[i];
    if (char !== "\\") {
      out += char;
      continue;
    }
    const next = raw[i + 1];
    if (next === undefined) {
      break;
    }
    i += 1;
    switch (next) {
      case ":":
        out += ";";
        break;
      case "s":
        out += " ";
        break;
      case "r":
        out += "\r";
        break;
      case "n":
        out += "\n";
        break;
      case "\\":
        out += "\\";
        break;
      default:
        out += next;
    }
  }
  return out;
}

/**
 * Escape a tag value for writing back onto the wire per the message-tags
 * specification. This is the security-critical direction: every character
 * that could be mistaken for tag/message syntax is encoded so an attacker
 * cannot inject a `;`, space, CR or LF into the serialized line.
 */
export function escapeTagValue(value: string): string {
  let out = "";
  for (const char of value) {
    switch (char) {
      case ";":
        out += "\\:";
        break;
      case " ":
        out += "\\s";
        break;
      case "\\":
        out += "\\\\";
        break;
      case "\r":
        out += "\\r";
        break;
      case "\n":
        out += "\\n";
        break;
      case "\0":
        // NUL must never reach the wire; line framing rejects it regardless,
        // but fail closed here too.
        throw new ProtocolError("tag value contains a NUL octet");
      default:
        out += char;
    }
  }
  return out;
}

/**
 * Parse the body of an `@` tag string (without the leading `@`) into a map.
 * Throws {@link ProtocolError} on an empty tag name, an illegal name or a
 * duplicate tag.
 */
export function parseTags(tagString: string): TagMap {
  const tags: TagMap = new Map();
  if (tagString.length === 0) {
    throw new ProtocolError("empty tag section");
  }

  for (const part of tagString.split(";")) {
    if (part.length === 0) {
      throw new ProtocolError("empty tag name");
    }
    const eq = part.indexOf("=");
    const name = eq === -1 ? part : part.slice(0, eq);
    if (!TAG_NAME_RE.test(name)) {
      throw new ProtocolError(`invalid tag name: ${name}`);
    }
    if (tags.has(name)) {
      throw new ProtocolError(`duplicate tag: ${name}`);
    }
    tags.set(name, eq === -1 ? true : unescapeTagValue(part.slice(eq + 1)));
  }
  return tags;
}

/** Serialize a tag map back into an `@`-less wire string (escaped values). */
export function serializeTags(tags: TagMap): string {
  const parts: string[] = [];
  for (const [name, value] of tags) {
    if (!TAG_NAME_RE.test(name)) {
      throw new ProtocolError(`invalid tag name: ${name}`);
    }
    parts.push(value === true ? name : `${name}=${escapeTagValue(value)}`);
  }
  return parts.join(";");
}

/** A client-only tag (message-tags) has a `+` immediately after any vendor. */
export function isClientOnlyTag(name: string): boolean {
  const slash = name.indexOf("/");
  return name.charAt(slash + 1) === "+";
}

/**
 * Return a new tag map containing only the tags that may be relayed to a
 * given recipient:
 *   - if the recipient has ACKed `message-tags`, client-only (`+`) tags are
 *     kept alongside server tags;
 *   - otherwise every client-only tag is stripped (server tags remain, as
 *     those are governed by the server's own policy).
 */
export function tagsForRecipient(tags: TagMap, recipientHasMessageTags: boolean): TagMap {
  const filtered: TagMap = new Map();
  for (const [name, value] of tags) {
    if (isClientOnlyTag(name) && !recipientHasMessageTags) {
      continue;
    }
    filtered.set(name, value);
  }
  return filtered;
}
