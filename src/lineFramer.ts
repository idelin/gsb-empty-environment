import { ProtocolError } from "./errors.ts";

/**
 * Maximum length of an IRC message in octets, excluding the terminating CRLF
 * (RFC 2812 section 2.3). Lines at or above this size are rejected.
 */
export const MAX_LINE_LENGTH = 512;

const CR = 0x0d;
const LF = 0x0a;
const NUL = 0x00;

/**
 * Stateful CRLF framer for a single TCP connection.
 *
 * Every accepted line is guaranteed:
 *   - to contain no CR, LF or NUL anywhere (a bare CR, a bare LF or an
 *     embedded NUL rejects the whole line and the connection), and
 *   - to be at most {@link MAX_LINE_LENGTH} octets long.
 *
 * Call {@link feed} with each incoming chunk; complete lines are delivered to
 * the `onLine` callback. A {@link ProtocolError} is thrown (and internal state
 * is reset) as soon as an illegal framing condition is observed.
 */
export class LineFramer {
  private buffer: number[] = [];

  private readonly onLine: (line: string) => void;

  constructor(onLine: (line: string) => void) {
    this.onLine = onLine;
  }

  feed(chunk: Buffer | string): void {
    const data = typeof chunk === "string" ? Buffer.from(chunk, "utf8") : chunk;

    for (const octet of data) {
      if (octet === NUL) {
        this.buffer = [];
        throw new ProtocolError("line contains a NUL octet");
      }

      // A CR is only legal as the first half of a CRLF terminator. If a CR is
      // already pending, another CR (or any non-LF octet) is a bare CR.
      if (this.lastIsCr()) {
        if (octet === LF) {
          const content = Buffer.from(this.buffer.slice(0, -1));
          this.buffer = [];
          this.onLine(content.toString("utf8"));
        } else {
          this.buffer = [];
          throw new ProtocolError("line contains a bare CR");
        }
        continue;
      }

      if (octet === CR) {
        // Tentatively buffer the CR; the next octet must be LF.
        this.buffer.push(octet);
        continue;
      }

      if (octet === LF) {
        // LF not preceded by CR.
        this.buffer = [];
        throw new ProtocolError("line must be terminated by CRLF");
      }

      this.buffer.push(octet);
      if (this.buffer.length > MAX_LINE_LENGTH) {
        this.buffer = [];
        throw new ProtocolError(`line exceeds ${MAX_LINE_LENGTH} octets`);
      }
    }
  }

  private lastIsCr(): boolean {
    return this.buffer.length > 0 && this.buffer[this.buffer.length - 1] === CR;
  }
}

/**
 * One-shot guard used by tests and by direct callers that already hold a
 * complete line: rejects any line that contains a CR, LF or NUL.
 */
export function assertCleanLine(line: string): void {
  for (const char of line) {
    if (char === "\r" || char === "\n" || char === "\0") {
      throw new ProtocolError("line must not contain CR, LF or NUL");
    }
  }
}
