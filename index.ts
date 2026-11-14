export {
  BoundaryError,
  fail,
} from "./src/errors.ts";
export type { BoundaryErrorCode } from "./src/errors.ts";

export {
  createMessageBoundaryMiddleware,
  MessageBoundaryMiddleware,
} from "./src/middleware.ts";
export type { HeadInspection } from "./src/middleware.ts";

export {
  decideRequestFraming,
  finalizeHeaders,
  resolvePolicy,
} from "./src/framing.ts";
export {
  findHeadEnd,
  parseHeaderField,
  parseRequestHead,
  splitCommaList,
} from "./src/headers.ts";
export {
  parseChunkSizeLine,
  parseChunkedBody,
  validateChunkExtensions,
} from "./src/chunked.ts";
export type {
  ChunkedParseResult,
  DecodedChunkedBody,
} from "./src/chunked.ts";
export { parseHttp1Message } from "./src/message.ts";
export type {
  MessageParseResult,
  ParsedWireMessage,
} from "./src/message.ts";
export {
  RecordingBackend,
  serializeForwardedRequest,
  verifyForwardedRequest,
} from "./src/backend.ts";
export { ProxyConnection } from "./src/connection.ts";
export type {
  BackendHandler,
  CloseInfo,
  ProxyConnectionOptions,
} from "./src/connection.ts";
export type {
  BoundaryPolicy,
  ForwardedRequest,
  ForwardingBoundary,
  FramingDecision,
  FramingKind,
  HeaderEntry,
  MessageBoundary,
  ParsedRequestHead,
  Rejection,
  ResolvedBoundaryPolicy,
  UpgradeDecision,
} from "./src/types.ts";
