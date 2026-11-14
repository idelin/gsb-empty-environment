import { BoundaryError, fail } from "./errors.ts";
import type { HeaderEntry, ParsedRequestHead } from "./types.ts";

const TOKEN_CHARS = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
const HEADER_VALUE_CHARS = /^[\t\x20-\x7e]*$/;
const REQUEST_TARGET_CHARS = /^[\x21-\x7e]+$/;
const REQUEST_LINE =
  /^([!#$%&'*+\-.^_`|~0-9A-Za-z]+) ([\x21-\x7e]+) (HTTP\/1\.[01])$/;

const MAX_HEADER_COUNT = 256;

export function isToken(value: string): boolean {
  return TOKEN_CHARS.test(value);
}

export function isTokenChar(code: number): boolean {
  if (code >= 0x30 && code <= 0x39) return true;
  if (code >= 0x41 && code <= 0x5a) return true;
  if (code >= 0x61 && code <= 0x7a) return true;
  switch (code) {
    case 0x21: // !
    case 0x23: // #
    case 0x24: // $
    case 0x25: // %
    case 0x26: // &
    case 0x27: // '
    case 0x2a: // *
    case 0x2b: // +
    case 0x2d: // -
    case 0x2e: // .
    case 0x5e: // ^
    case 0x5f: // _
    case 0x60: // `
    case 0x7c: // |
    case 0x7e: // ~
      return true;
    default:
      return false;
  }
}

/**
 * Locate the end of an HTTP/1 head. The returned index is just after the
 * terminating CRLFCRLF. Bare LF and invalid CR sequences are rejected as soon
 * as they are visible, rather than being deferred until EOF.
 */
export function findHeadEnd(buffer: Buffer, offset = 0, limit = 16 * 1024): number {
  const terminator = buffer.indexOf("\r\n\r\n", offset, "latin1");
  const scanEnd = terminator === -1 ? buffer.length : terminator + 4;
  if (scanEnd - offset > limit) {
    fail("HEADER_TOO_LARGE", `request head exceeds ${limit} bytes`);
  }

  let previousWasCr = false;

  for (let index = offset; index < scanEnd; index += 1) {
    const byte = buffer[index];
    if (byte === 0x0a) {
      if (!previousWasCr) {
        fail("INVALID_HEADER", "bare LF in request head");
      }
    } else if (previousWasCr) {
      fail("INVALID_HEADER", "CR not followed by LF in request head");
    }
    previousWasCr = byte === 0x0d;
  }

  return terminator === -1 ? -1 : terminator + 4;
}

export function parseRequestHead(rawHead: string | Buffer): ParsedRequestHead {
  let text =
    typeof rawHead === "string" ? rawHead : rawHead.toString("latin1");

  if (text.endsWith("\r\n\r\n")) {
    text = text.slice(0, -4);
  } else if (text.endsWith("\r\n")) {
    text = text.slice(0, -2);
  }

  validateCrLf(text);
  if (text.length === 0) {
    fail("INVALID_REQUEST_LINE", "empty request head");
  }

  const lines = text.split("\r\n");
  const requestLine = lines[0];
  const match = REQUEST_LINE.exec(requestLine);
  if (match === null) {
    fail("INVALID_REQUEST_LINE", "expected METHOD SP target SP HTTP/1.x");
  }

  const method = match[1];
  const target = match[2];
  const version = match[3] as "HTTP/1.0" | "HTTP/1.1";
  if (!REQUEST_TARGET_CHARS.test(target)) {
    fail("INVALID_REQUEST_LINE", "non-ASCII or control byte in request target");
  }

  const headers: HeaderEntry[] = [];
  for (let index = 1; index < lines.length; index += 1) {
    const line = lines[index];
    if (line.length === 0) {
      fail("INVALID_HEADER", "unexpected empty header line");
    }
    if (line.startsWith(" ") || line.startsWith("\t")) {
      fail("OBSOLETE_LINE_FOLDING", "obsolete line folding is not accepted");
    }
    headers.push(parseHeaderField(line));
    if (headers.length > MAX_HEADER_COUNT) {
      fail("HEADER_TOO_LARGE", "too many request headers");
    }
  }

  return {
    method,
    target,
    version,
    headers,
  };
}

export function parseHeaderField(line: string): HeaderEntry {
  const colon = line.indexOf(":");
  if (colon <= 0) {
    fail("INVALID_HEADER", "header field must contain a colon");
  }

  const rawName = line.slice(0, colon);
  if (!isToken(rawName)) {
    fail("INVALID_HEADER", `invalid header name ${JSON.stringify(rawName)}`);
  }
  const value = trimOws(line.slice(colon + 1));
  if (!HEADER_VALUE_CHARS.test(value)) {
    fail("INVALID_HEADER_VALUE", `invalid value for header ${rawName}`);
  }

  return [rawName.toLowerCase(), value];
}

export function validateCrLf(text: string): void {
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index);
    if (code === 0x0a) {
      if (index === 0 || text.charCodeAt(index - 1) !== 0x0d) {
        fail("INVALID_HEADER", "bare LF in request head");
      }
    } else if (code === 0x0d) {
      if (index + 1 >= text.length || text.charCodeAt(index + 1) !== 0x0a) {
        fail("INVALID_HEADER", "CR not followed by LF in request head");
      }
    }
  }
}

export function trimOws(value: string): string {
  return value.replace(/^[ \t]+/, "").replace(/[ \t]+$/, "");
}

export function headerValues(
  headers: ReadonlyArray<HeaderEntry>,
  name: string,
): string[] {
  const values: string[] = [];
  for (const [headerName, value] of headers) {
    if (headerName === name) values.push(value);
  }
  return values;
}

export function splitCommaList(value: string): string[] {
  const parts = value.split(",");
  const tokens: string[] = [];
  for (const part of parts) {
    const token = trimOws(part);
    if (token.length === 0) {
      fail("INVALID_HEADER", "empty item in comma-separated header");
    }
    tokens.push(token);
  }
  return tokens;
}

export function connectionTokens(
  headers: ReadonlyArray<HeaderEntry>,
): Set<string> {
  const tokens = new Set<string>();
  for (const value of headerValues(headers, "connection")) {
    for (const token of splitCommaList(value)) {
      if (!isToken(token)) {
        fail("INVALID_HEADER", "invalid Connection token");
      }
      const lower = token.toLowerCase();
      if (lower === "content-length" || lower === "transfer-encoding") {
        fail(
          "INVALID_HEADER",
          `Connection must not nominate framing header ${lower}`,
        );
      }
      tokens.add(lower);
    }
  }
  return tokens;
}

export function isBoundaryError(error: unknown): error is BoundaryError {
  return error instanceof BoundaryError;
}
