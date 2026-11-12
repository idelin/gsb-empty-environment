import { ProtocolError } from "./errors.ts";
import { parseTags, serializeTags } from "./tags.ts";
import type { TagMap } from "./tags.ts";

export interface Message {
  /** Parsed, unescaped tags (empty map when the line had no `@` section). */
  tags: TagMap;
  /** `null` for client-originated lines, which are forbidden from a prefix. */
  prefix: string | null;
  /** Command verb, uppercased for letter commands (e.g. `PRIVMSG`, `001`). */
  command: string;
  /** Parameters; a trailing parameter may contain spaces. */
  params: string[];
}

const COMMAND_RE = /^[A-Za-z]+$|^\d{3}$/;

/**
 * Parse one complete, framing-clean line (no CR/LF/NUL) into a {@link Message}.
 */
export function parseMessage(line: string): Message {
  let rest = line;
  let tags: TagMap = new Map();

  if (rest.startsWith("@")) {
    const sp = rest.indexOf(" ");
    if (sp === -1) {
      throw new ProtocolError("tag section is not followed by a command");
    }
    tags = parseTags(rest.slice(1, sp));
    rest = rest.slice(sp + 1);
    rest = skipSpaces(rest);
  }

  let prefix: string | null = null;
  if (rest.startsWith(":")) {
    const sp = rest.indexOf(" ");
    if (sp === -1) {
      throw new ProtocolError("prefix is not followed by a command");
    }
    prefix = rest.slice(1, sp);
    if (prefix.length === 0) {
      throw new ProtocolError("empty prefix");
    }
    rest = rest.slice(sp + 1);
    rest = skipSpaces(rest);
  }

  const sp = rest.indexOf(" ");
  const command = sp === -1 ? rest : rest.slice(0, sp);
  if (command.length === 0 || !COMMAND_RE.test(command)) {
    throw new ProtocolError(`invalid command: ${command}`);
  }

  return {
    tags,
    prefix,
    command: /^\d+$/.test(command) ? command : command.toUpperCase(),
    params: sp === -1 ? [] : parseParams(rest.slice(sp + 1)),
  };
}

function skipSpaces(text: string): string {
  let i = 0;
  while (text.charCodeAt(i) === 0x20) i += 1;
  return text.slice(i);
}

function parseParams(text: string): string[] {
  const params: string[] = [];
  let rest = skipSpaces(text);

  while (rest.length > 0) {
    if (rest.startsWith(":")) {
      // Trailing parameter: remainder of the line, verbatim (may be empty).
      params.push(rest.slice(1));
      break;
    }
    const sp = rest.indexOf(" ");
    if (sp === -1) {
      params.push(rest);
      break;
    }
    params.push(rest.slice(0, sp));
    rest = skipSpaces(rest.slice(sp + 1));
  }
  return params;
}

/**
 * Serialize a message to a single line *without* the trailing CRLF. Tag
 * values are escaped here, so any `;`, space, CR or LF inside a value cannot
 * break out of the tag section.
 */
export function serializeMessage(message: Message): string {
  const parts: string[] = [];

  if (message.tags.size > 0) {
    parts.push(`@${serializeTags(message.tags)}`);
  }
  if (message.prefix !== null) {
    parts.push(`:${message.prefix}`);
  }
  parts.push(message.command);

  for (let i = 0; i < message.params.length; i += 1) {
    const param = message.params[i];
    const isLast = i === message.params.length - 1;
    if (isLast && (param.includes(" ") || param.startsWith(":"))) {
      parts.push(`:${param}`);
    } else if (param.length === 0) {
      // An empty parameter is only legal as a trailing parameter.
      if (!isLast) {
        throw new ProtocolError("empty middle parameter");
      }
      parts.push(":");
    } else {
      parts.push(param);
    }
  }

  return parts.join(" ");
}

/** Convenience builder for outbound (server) messages. */
export function buildMessage(
  prefix: string | null,
  command: string,
  params: string[],
  tags: TagMap = new Map(),
): Message {
  return { tags, prefix, command, params };
}
