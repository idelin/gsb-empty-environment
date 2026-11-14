import { parseChunkedBody } from "./chunked.ts";
import {
  decideRequestFraming,
  finalizeHeaders,
} from "./framing.ts";
import { findHeadEnd, parseRequestHead } from "./headers.ts";
import type {
  FramingDecision,
  HeaderEntry,
  MessageBoundary,
  ParsedRequestHead,
  ResolvedBoundaryPolicy,
} from "./types.ts";

export interface ParsedWireMessage {
  readonly head: ParsedRequestHead;
  readonly decision: FramingDecision;
  readonly headers: ReadonlyArray<HeaderEntry>;
  readonly body: Buffer;
  readonly boundary: MessageBoundary;
  /**
   * Absolute offset in the input buffer immediately after this message.
   */
  readonly nextOffset: number;
}

export type MessageParseResult =
  | {
      readonly status: "complete";
      readonly value: ParsedWireMessage;
    }
  | { readonly status: "incomplete" };

/**
 * Parse exactly one HTTP/1 message from `buffer` beginning at `offset`.
 *
 * The body boundary comes from the single framing decision; no byte-pattern
 * scan is used to guess where the next request starts.
 */
export function parseHttp1Message(
  buffer: Buffer,
  offset: number,
  policy: ResolvedBoundaryPolicy,
): MessageParseResult {
  if (offset >= buffer.length) {
    return { status: "incomplete" };
  }

  const headEnd = findHeadEnd(buffer, offset, policy.maxHeaderBytes);
  if (headEnd === -1) {
    return { status: "incomplete" };
  }

  const head = parseRequestHead(buffer.subarray(offset, headEnd - 4));
  const decision = decideRequestFraming(head, policy);
  const bodyStart = headEnd;

  if (decision.kind === "none") {
    return {
      status: "complete",
      value: makeMessage(
        head,
        decision,
        finalizeHeaders(decision, 0),
        Buffer.alloc(0),
        makeBoundary(decision, 0, headEnd, headEnd, headEnd, 0),
        headEnd,
      ),
    };
  }

  if (decision.kind === "upgrade") {
    return {
      status: "complete",
      value: makeMessage(
        head,
        decision,
        finalizeHeaders(decision, 0),
        Buffer.alloc(0),
        makeBoundary(decision, 0, headEnd, headEnd, headEnd, 0),
        headEnd,
      ),
    };
  }

  if (decision.kind === "content-length") {
    const contentLength = decision.contentLength ?? 0;
    if (buffer.length - bodyStart < contentLength) {
      return { status: "incomplete" };
    }
    const bodyEnd = bodyStart + contentLength;
    const body = Buffer.from(buffer.subarray(bodyStart, bodyEnd));

    return {
      status: "complete",
      value: makeMessage(
        head,
        decision,
        finalizeHeaders(decision, contentLength),
        body,
        makeBoundary(
          decision,
          contentLength,
          headEnd,
          bodyStart,
          bodyEnd,
          contentLength,
        ),
        bodyEnd,
      ),
    };
  }

  const chunked = parseChunkedBody(buffer, bodyStart, policy);
  if (chunked.status === "incomplete") {
    return { status: "incomplete" };
  }

  const bodyEnd = bodyStart + chunked.value.wireLength;
  return {
    status: "complete",
    value: makeMessage(
      head,
      decision,
      finalizeHeaders(decision, chunked.value.body.length),
      chunked.value.body,
      makeBoundary(
        decision,
        chunked.value.body.length,
        headEnd,
        bodyStart,
        bodyEnd,
        chunked.value.wireLength,
      ),
      bodyEnd,
    ),
  };
}

function makeMessage(
  head: ParsedRequestHead,
  decision: FramingDecision,
  headers: ReadonlyArray<HeaderEntry>,
  body: Buffer,
  boundary: MessageBoundary,
  nextOffset: number,
): ParsedWireMessage {
  return Object.freeze({
    head,
    decision,
    headers,
    body,
    boundary,
    nextOffset,
  });
}

export function makeBoundary(
  decision: FramingDecision,
  contentLength: number,
  headEnd: number,
  bodyStart: number,
  bodyEnd: number,
  wireBodyLength: number,
): MessageBoundary {
  const forwarding =
    decision.kind === "upgrade"
      ? Object.freeze({
          mode: "upgrade" as const,
          contentLength: 0,
        })
      : decision.kind === "none"
        ? Object.freeze({
            mode: "none" as const,
            contentLength: 0,
          })
        : Object.freeze({
            mode: "content-length" as const,
            contentLength,
          });

  return Object.freeze({
    kind: decision.kind,
    contentLength,
    headEnd,
    bodyStart,
    bodyEnd,
    wireBodyLength,
    forwarding,
    upgrade: decision.upgrade,
  });
}
