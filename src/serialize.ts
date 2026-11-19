import { SerializationError } from './errors.ts';
import { serializeTags } from './tags.ts';
import type { TagMap } from './tags.ts';

export interface OutgoingMessage {
  tags?: TagMap;
  prefix?: string;
  command: string;
  params?: string[];
}

const COMMAND_RE = /^[A-Za-z]+$|^\d{3}$/;

function assertNoForbiddenByte(value: string, field: string): void {
  for (const ch of value) {
    if (ch === '\r' || ch === '\n' || ch === '\0') {
      throw new SerializationError(
        `${field} contains forbidden byte 0x${ch.charCodeAt(0).toString(16).padStart(2, '0')}`,
      );
    }
  }
}

/**
 * Serialize an IRC message into a single line *without* the trailing CRLF.
 * Tag values are escaped, and every field is validated so that no CR, LF or
 * NUL can ever reach the wire. The last parameter is always sent as a
 * trailing parameter when it contains a space, starts with `:` or is empty.
 */
export function serializeMessage(message: OutgoingMessage): string {
  const parts: string[] = [];

  if (message.tags && message.tags.size > 0) {
    const tagSection = serializeTags(message.tags);
    parts.push(`@${tagSection}`);
  }

  if (message.prefix !== undefined) {
    assertNoForbiddenByte(message.prefix, 'prefix');
    if (message.prefix.length === 0 || /\s/.test(message.prefix)) {
      throw new SerializationError('invalid source prefix');
    }
    parts.push(`:${message.prefix}`);
  }

  const command = message.command;
  if (!COMMAND_RE.test(command)) {
    throw new SerializationError(`invalid command: ${JSON.stringify(command)}`);
  }
  parts.push(command.toUpperCase());

  const params = message.params ?? [];
  params.forEach((param, index) => {
    assertNoForbiddenByte(param, 'parameter');
    const isLast = index === params.length - 1;
    if (!isLast && param.includes(' ')) {
      throw new SerializationError('only the last parameter may contain a space');
    }
    if (isLast || param.length === 0 || param.startsWith(':')) {
      parts.push(`:${param}`);
    } else {
      parts.push(param);
    }
  });

  return parts.join(' ');
}
