import { ProtocolError } from './errors.ts';
import { parseTagString } from './tags.ts';
import type { TagMap } from './tags.ts';

export interface IrcMessage {
  /** Unescaped message tags keyed by tag name; absent when no `@` section. */
  tags?: TagMap;
  /** Optional source prefix without the leading `:`. */
  prefix?: string;
  /** Command verb, e.g. `PRIVMSG`, in upper case. */
  command: string;
  /** Command parameters, including any trailing parameter without `:`. */
  params: string[];
}

/**
 * Parse one complete IRC line. The line must not contain a CR, LF or NUL
 * byte: framing is responsible for splitting on `\r\n`, and any bare CR/LF or
 * NUL inside a line is treated as a protocol violation and rejected outright
 * so it can never be interpreted as an embedded command.
 */
export function parseLine(line: string): IrcMessage {
  if (line.length === 0) {
    throw new ProtocolError('empty line');
  }
  for (const ch of line) {
    if (ch === '\r' || ch === '\n' || ch === '\0') {
      throw new ProtocolError(
        `line contains forbidden byte 0x${ch.charCodeAt(0).toString(16).padStart(2, '0')}`,
      );
    }
  }

  let rest = line;
  let tags: TagMap | undefined;
  if (rest.startsWith('@')) {
    const sp = rest.indexOf(' ');
    if (sp === -1) {
      throw new ProtocolError('message-tag section is not terminated by SPACE');
    }
    tags = parseTagString(rest.slice(1, sp));
    rest = rest.slice(sp + 1);
    if (rest.length === 0) {
      throw new ProtocolError('line ends after message-tag section');
    }
  }

  let prefix: string | undefined;
  if (rest.startsWith(':')) {
    const sp = rest.indexOf(' ');
    if (sp === -1) {
      throw new ProtocolError('source prefix is not terminated by SPACE');
    }
    prefix = rest.slice(1, sp);
    if (prefix.length === 0) {
      throw new ProtocolError('empty source prefix');
    }
    rest = rest.slice(sp + 1);
    if (rest.length === 0) {
      throw new ProtocolError('line ends after source prefix');
    }
  }

  const sp = rest.indexOf(' ');
  const command = (sp === -1 ? rest : rest.slice(0, sp)).toUpperCase();
  if (command.length === 0 || !/^[A-Z]+$|^\d{3}$/.test(command)) {
    throw new ProtocolError(`invalid command: ${JSON.stringify(command)}`);
  }

  const params: string[] = [];
  if (sp !== -1) {
    let section = rest.slice(sp + 1);
    while (section.length > 0) {
      if (section.startsWith(':')) {
        params.push(section.slice(1));
        break;
      }
      const next = section.indexOf(' ');
      if (next === -1) {
        params.push(section);
        break;
      }
      params.push(section.slice(0, next));
      section = section.slice(next + 1);
      while (section.startsWith(' ')) {
        section = section.slice(1);
      }
    }
  }

  return { tags, prefix, command, params };
}

export interface DecoderResult {
  /** Lines that passed framing validation. */
  lines: string[];
  /** Forbidden lines that were rejected instead of being parsed. */
  rejected: string[];
}

/**
 * Streaming framer. It splits incoming data on `\n` (an optional preceding
 * `\r` is stripped) and rejects any line containing a bare CR, an embedded LF
 * or a NUL byte, including data still buffered when the stream is flushed.
 */
export class LineDecoder {
  private buffer = '';

  /** Called for every accepted line. */
  onLine: ((line: string) => void) | undefined;
  /** Called for every rejected raw line (still containing the bad byte). */
  onReject: ((rawLine: string, reason: string) => void) | undefined;

  feed(chunk: string): DecoderResult {
    this.buffer += chunk;
    const result: DecoderResult = { lines: [], rejected: [] };
    let nl: number;
    while ((nl = this.buffer.indexOf('\n')) !== -1) {
      let rawLine = this.buffer.slice(0, nl);
      this.buffer = this.buffer.slice(nl + 1);
      if (rawLine.endsWith('\r')) {
        rawLine = rawLine.slice(0, -1);
      }
      this.dispatch(rawLine, result);
    }
    return result;
  }

  /** Flush any buffered data without a trailing newline. */
  flush(): DecoderResult {
    const result: DecoderResult = { lines: [], rejected: [] };
    if (this.buffer.length > 0) {
      this.dispatch(this.buffer, result);
      this.buffer = '';
    }
    return result;
  }

  private dispatch(rawLine: string, result: DecoderResult): void {
    if (rawLine.includes('\r') || rawLine.includes('\n') || rawLine.includes('\0')) {
      const reason = rawLine.includes('\0')
        ? 'line contains NUL byte'
        : rawLine.includes('\n')
          ? 'line contains embedded LF'
          : 'line contains bare CR';
      result.rejected.push(rawLine);
      this.onReject?.(rawLine, reason);
      return;
    }
    if (rawLine.length === 0) {
      return;
    }
    result.lines.push(rawLine);
    this.onLine?.(rawLine);
  }
}
