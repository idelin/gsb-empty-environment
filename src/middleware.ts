import { decideRequestFraming, resolvePolicy } from "./framing.ts";
import { parseRequestHead } from "./headers.ts";
import { parseHttp1Message } from "./message.ts";
import type { MessageParseResult } from "./message.ts";
import type {
  BoundaryPolicy,
  FramingDecision,
  ParsedRequestHead,
  ResolvedBoundaryPolicy,
} from "./types.ts";

export interface HeadInspection {
  readonly head: ParsedRequestHead;
  readonly decision: FramingDecision;
}

/**
 * Importable boundary middleware. It can be used directly with buffers or as
 * the single framing authority for a proxy connection.
 */
export class MessageBoundaryMiddleware {
  readonly policy: ResolvedBoundaryPolicy;

  constructor(policy: BoundaryPolicy = {}) {
    this.policy = Object.freeze(resolvePolicy(policy));
  }

  inspectHead(rawHead: string | Buffer): HeadInspection {
    const head = parseRequestHead(rawHead);
    return {
      head,
      decision: decideRequestFraming(head, this.policy),
    };
  }

  parseMessage(buffer: Buffer, offset = 0): MessageParseResult {
    return parseHttp1Message(buffer, offset, this.policy);
  }
}

export function createMessageBoundaryMiddleware(
  policy: BoundaryPolicy = {},
): MessageBoundaryMiddleware {
  return new MessageBoundaryMiddleware(policy);
}
