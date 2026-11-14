import { BoundaryError, fail } from "./errors.ts";
import { isTokenChar, parseHeaderField, trimOws } from "./headers.ts";
import type {
  HeaderEntry,
  ResolvedBoundaryPolicy,
} from "./types.ts";

const MAX_CHUNK_LINE_BYTES = 8 * 1024;
const MAX_TRAILER_COUNT = 128;
const MAX_TRAILER_BYTES = 16 * 1024;

export interface DecodedChunkedBody {
  readonly body: Buffer;
  readonly trailers: ReadonlyArray<HeaderEntry>;
  readonly nextOffset: number;
  readonly wireLength: number;
}

export type ChunkedParseResult =
  | { readonly status: "complete"; readonly value: DecodedChunkedBody }
  | { readonly status: "incomplete" };

interface ChunkSize {
  readonly size: number;
  readonly extensionCount: number;
}

/**
 * Parse a chunked body. The returned nextOffset is the byte immediately after
 * the final CRLF of the trailer section. Any bytes after that offset belong to
 * the next message and are never consumed here.
 */
export function parseChunkedBody(
  buffer: Buffer,
  start: number,
  policy: ResolvedBoundaryPolicy,
): ChunkedParseResult {
  let offset = start;
  const chunks: Buffer[] = [];
  let decodedLength = 0;

  for (;;) {
    const lineEnd = findLineEnd(buffer, offset);
    if (lineEnd === -1) return { status: "incomplete" };

    const line = buffer.subarray(offset, lineEnd).toString("latin1");
    const chunkSize = parseChunkSizeLine(line, policy);
    offset = lineEnd + 2;

    if (chunkSize.size === 0) {
      const trailerResult = parseTrailerSection(buffer, offset, policy);
      if (trailerResult.status === "incomplete") {
        return { status: "incomplete" };
      }

      return {
        status: "complete",
        value: {
          body: Buffer.concat(chunks, decodedLength),
          trailers: trailerResult.trailers,
          nextOffset: trailerResult.nextOffset,
          wireLength: trailerResult.nextOffset - start,
        },
      };
    }

    if (chunkSize.size > policy.maxChunkSize) {
      fail(
        "CHUNK_TOO_LARGE",
        `chunk size ${chunkSize.size} exceeds ${policy.maxChunkSize}`,
      );
    }
    if (decodedLength + chunkSize.size > policy.maxContentLength) {
      fail(
        "BODY_TOO_LARGE",
        `decoded body exceeds ${policy.maxContentLength} bytes`,
      );
    }
    if (offset + chunkSize.size > buffer.length) {
      return { status: "incomplete" };
    }

    const dataEnd = offset + chunkSize.size;
    if (dataEnd + 2 > buffer.length) {
      if (dataEnd < buffer.length && buffer[dataEnd] !== 0x0d) {
        fail("INVALID_CHUNK_TERMINATOR", "chunk data must end with CRLF");
      }
      return { status: "incomplete" };
    }
    if (buffer[dataEnd] !== 0x0d || buffer[dataEnd + 1] !== 0x0a) {
      fail("INVALID_CHUNK_TERMINATOR", "chunk data must end with CRLF");
    }

    chunks.push(Buffer.from(buffer.subarray(offset, dataEnd)));
    decodedLength += chunkSize.size;
    offset = dataEnd + 2;
  }
}

export function parseChunkSizeLine(
  line: string,
  policy: ResolvedBoundaryPolicy,
): ChunkSize {
  if (line.length === 0) {
    fail("INVALID_CHUNK_SIZE", "empty chunk-size line");
  }
  if (line.length > MAX_CHUNK_LINE_BYTES) {
    fail("INVALID_CHUNK_SIZE", "chunk-size line is too long");
  }
  if (!/^[\x20-\x7e]+$/.test(line)) {
    fail("INVALID_CHUNK_SIZE", "non-ASCII or control byte in chunk-size line");
  }

  const semicolon = line.indexOf(";");
  const sizeText = semicolon === -1 ? line : line.slice(0, semicolon);
  if (!/^[0-9A-Fa-f]+$/.test(sizeText)) {
    fail(
      "INVALID_CHUNK_SIZE",
      "chunk size must be a non-empty hexadecimal integer with no whitespace",
    );
  }

  let sizeBig: bigint;
  try {
    sizeBig = BigInt(`0x${sizeText}`);
  } catch {
    fail("INVALID_CHUNK_SIZE", "invalid hexadecimal chunk size");
  }
  if (sizeBig > BigInt(Number.MAX_SAFE_INTEGER)) {
    fail("INVALID_CHUNK_SIZE", "chunk size exceeds safe integer range");
  }
  if (sizeBig > BigInt(policy.maxChunkSize)) {
    fail("CHUNK_TOO_LARGE", "chunk size exceeds configured maximum");
  }

  let extensionCount = 0;
  if (semicolon !== -1) {
    extensionCount = validateChunkExtensions(line.slice(semicolon));
  }

  return {
    size: Number(sizeBig),
    extensionCount,
  };
}

/**
 * Strict, canonical chunk extension parser.
 *
 * Legal: `;name`, `;name=token`, `;name="quoted value"`, and repetitions.
 * Rejected as ambiguous: any whitespace around separators, empty names or
 * values, duplicate names, control bytes, unterminated quoted strings, and
 * whitespace other than SP inside quoted strings.
 */
export function validateChunkExtensions(extensions: string): number {
  if (!extensions.startsWith(";")) {
    fail("INVALID_CHUNK_EXTENSION", "extensions must start with ';'");
  }

  const names = new Set<string>();
  let offset = 0;
  let count = 0;

  while (offset < extensions.length) {
    if (extensions[offset] !== ";") {
      fail("AMBIGUOUS_CHUNK_EXTENSION", "expected ';' between extensions");
    }
    offset += 1;
    if (offset >= extensions.length) {
      fail("INVALID_CHUNK_EXTENSION", "empty chunk extension");
    }

    const nameStart = offset;
    while (
      offset < extensions.length &&
      isTokenChar(extensions.charCodeAt(offset))
    ) {
      offset += 1;
    }
    if (offset === nameStart) {
      fail("AMBIGUOUS_CHUNK_EXTENSION", "empty or whitespace chunk-ext name");
    }
    const name = extensions.slice(nameStart, offset);
    const canonicalName = name.toLowerCase();
    if (names.has(canonicalName)) {
      fail("AMBIGUOUS_CHUNK_EXTENSION", `duplicate chunk extension ${name}`);
    }
    names.add(canonicalName);
    count += 1;

    if (offset >= extensions.length) continue;
    if (extensions[offset] !== "=") {
      if (extensions[offset] === ";") continue;
      fail(
        "AMBIGUOUS_CHUNK_EXTENSION",
        "whitespace or invalid separator after chunk-ext name",
      );
    }

    offset += 1;
    if (offset >= extensions.length) {
      fail("INVALID_CHUNK_EXTENSION", "empty chunk-ext value");
    }

    if (extensions[offset] === "\"") {
      offset = parseQuotedString(extensions, offset);
    } else {
      const valueStart = offset;
      while (
        offset < extensions.length &&
        isTokenChar(extensions.charCodeAt(offset))
      ) {
        offset += 1;
      }
      if (offset === valueStart) {
        fail(
          "AMBIGUOUS_CHUNK_EXTENSION",
          "chunk-ext token value must not start with whitespace",
        );
      }
    }

    if (offset < extensions.length && extensions[offset] !== ";") {
      fail(
        "AMBIGUOUS_CHUNK_EXTENSION",
        "whitespace or invalid data after chunk-ext value",
      );
    }
  }

  return count;
}

function parseQuotedString(value: string, quoteOffset: number): number {
  let offset = quoteOffset + 1;

  while (offset < value.length) {
    const code = value.charCodeAt(offset);
    if (code === 0x22) {
      return offset + 1;
    }
    if (code === 0x5c) {
      offset += 1;
      if (offset >= value.length) {
        fail("INVALID_CHUNK_EXTENSION", "unterminated quoted-pair");
      }
      const escaped = value.charCodeAt(offset);
      if (!isQuotedText(escaped)) {
        fail(
          "AMBIGUOUS_CHUNK_EXTENSION",
          "unsafe byte in escaped quoted string",
        );
      }
      offset += 1;
      continue;
    }
    if (!isQuotedText(code)) {
      fail(
        "AMBIGUOUS_CHUNK_EXTENSION",
        "control or non-ASCII byte in quoted chunk extension",
      );
    }
    offset += 1;
  }

  fail("INVALID_CHUNK_EXTENSION", "unterminated quoted chunk extension");
}

function isQuotedText(code: number): boolean {
  // SP, HTAB, and visible ASCII except DQUOTE and backslash.
  if (code === 0x09 || code === 0x20) return true;
  if (code < 0x21 || code > 0x7e) return false;
  return code !== 0x22 && code !== 0x5c;
}

interface TrailerResult {
  readonly status: "complete";
  readonly trailers: ReadonlyArray<HeaderEntry>;
  readonly nextOffset: number;
}

type TrailerParseResult =
  | TrailerResult
  | { readonly status: "incomplete" };

function parseTrailerSection(
  buffer: Buffer,
  start: number,
  policy: ResolvedBoundaryPolicy,
): TrailerParseResult {
  if (start >= buffer.length) {
    return { status: "incomplete" };
  }

  if (buffer[start] === 0x0d) {
    if (start + 1 >= buffer.length) return { status: "incomplete" };
    if (buffer[start + 1] !== 0x0a) {
      fail("INVALID_CHUNK_TERMINATOR", "invalid trailer terminator");
    }
    return {
      status: "complete",
      trailers: [],
      nextOffset: start + 2,
    };
  }

  if (buffer[start] === 0x0a) {
    fail("INVALID_CHUNK_TERMINATOR", "bare LF after final chunk");
  }

  if (!policy.allowTrailers) {
    fail(
      "TRAILERS_NOT_ALLOWED",
      "non-empty trailer section is rejected by policy",
    );
  }

  let offset = start;
  let trailerBytes = 0;
  const trailers: HeaderEntry[] = [];
  const names = new Set<string>();

  for (;;) {
    const lineEnd = findLineEnd(buffer, offset);
    if (lineEnd === -1) return { status: "incomplete" };
    trailerBytes += lineEnd + 2 - offset;
    if (trailerBytes > MAX_TRAILER_BYTES) {
      fail("INVALID_TRAILER", "trailer section is too large");
    }

    const line = buffer.subarray(offset, lineEnd).toString("latin1");
    offset = lineEnd + 2;
    if (line.length === 0) {
      return {
        status: "complete",
        trailers,
        nextOffset: offset,
      };
    }
    if (line.startsWith(" ") || line.startsWith("\t")) {
      fail("INVALID_TRAILER", "obsolete line folding in trailer");
    }

    let header: HeaderEntry;
    try {
      header = parseHeaderField(line);
    } catch (error) {
      if (error instanceof BoundaryError) {
        fail("INVALID_TRAILER", error.message);
      }
      throw error;
    }

    const [name, value] = header;
    if (
      name === "content-length" ||
      name === "transfer-encoding" ||
      name === "host" ||
      name === "connection" ||
      name === "te" ||
      name === "trailer" ||
      name === "upgrade" ||
      name === "http2-settings" ||
      name === "proxy-connection" ||
      name === "keep-alive"
    ) {
      fail("INVALID_TRAILER", `framing/hop-by-hop trailer ${name} is forbidden`);
    }
    if (names.has(name)) {
      fail("INVALID_TRAILER", `duplicate trailer ${name}`);
    }
    names.add(name);
    trailers.push([name, trimOws(value)]);

    if (trailers.length > MAX_TRAILER_COUNT) {
      fail("INVALID_TRAILER", "too many trailer fields");
    }
  }
}

function findLineEnd(buffer: Buffer, start: number): number {
  const lineEnd = buffer.indexOf("\r\n", start, "latin1");
  const scanEnd = lineEnd === -1 ? buffer.length : lineEnd;
  assertSafePartialLine(buffer, start, scanEnd, lineEnd !== -1);
  if (lineEnd === -1 && buffer.length - start > MAX_CHUNK_LINE_BYTES) {
    fail("INVALID_CHUNK_SIZE", "chunk-size line is too long");
  }
  return lineEnd;
}

function assertSafePartialLine(
  buffer: Buffer,
  start: number,
  end: number,
  completeLine: boolean,
): void {
  let previousWasCr = false;
  for (let index = start; index < end; index += 1) {
    const byte = buffer[index];
    if (byte === 0x0a) {
      if (!previousWasCr) {
        fail("INVALID_CHUNK_TERMINATOR", "bare LF in chunked body");
      }
    } else if (previousWasCr) {
      fail("INVALID_CHUNK_TERMINATOR", "CR not followed by LF in chunked body");
    }
    previousWasCr = byte === 0x0d;
  }
  if (completeLine && end > start && buffer[end - 1] === 0x0d) {
    fail(
      "INVALID_CHUNK_TERMINATOR",
      "CR immediately before CRLF in chunked body",
    );
  }
}
