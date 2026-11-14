import type { BoundaryError } from "./errors.ts";

export type HeaderEntry = readonly [name: string, value: string];

export interface ParsedRequestHead {
  readonly method: string;
  readonly target: string;
  readonly version: "HTTP/1.0" | "HTTP/1.1";
  readonly headers: ReadonlyArray<HeaderEntry>;
}

export interface BoundaryPolicy {
  /**
   * Maximum bytes accepted before the end of the request head.
   * Defaults to 16 KiB.
   */
  readonly maxHeaderBytes?: number;
  /**
   * Maximum decoded request body size. Defaults to 64 MiB.
   */
  readonly maxContentLength?: number;
  /**
   * Maximum size of a single chunk. Defaults to maxContentLength.
   */
  readonly maxChunkSize?: number;
  /**
   * Trailer fields can be interpreted differently by intermediaries. They are
   * rejected unless this is explicitly enabled.
   */
  readonly allowTrailers?: boolean;
  /**
   * A request carrying `Upgrade: h2c` is rejected unless this is true. When
   * true, the upgrade is removed and the request is forwarded as HTTP/1.1.
   */
  readonly allowH2cDowngrade?: boolean;
  /**
   * Non-h2c Upgrade requests are rejected unless this is explicitly enabled.
   * When enabled, the request is forwarded in upgrade mode and subsequent
   * bytes are opaque protocol data, never HTTP requests.
   */
  readonly allowGenericUpgrade?: boolean;
  /**
   * Pipelined bytes are rejected (and the connection is closed) by default.
   * Enabling this only affects bytes that arrive together with a completed
   * message; they are then parsed as a separate request.
   */
  readonly allowPipelining?: boolean;
}

export interface ResolvedBoundaryPolicy {
  readonly maxHeaderBytes: number;
  readonly maxContentLength: number;
  readonly maxChunkSize: number;
  readonly allowTrailers: boolean;
  readonly allowH2cDowngrade: boolean;
  readonly allowGenericUpgrade: boolean;
  readonly allowPipelining: boolean;
}

export type FramingKind = "none" | "content-length" | "chunked" | "upgrade";

export interface UpgradeDecision {
  readonly protocol: string;
  readonly h2cDowngraded: boolean;
  readonly h2cSettings?: string;
}

export interface FramingDecision {
  readonly kind: FramingKind;
  readonly contentLength: number | null;
  readonly transferCodings: ReadonlyArray<string>;
  readonly upgrade: UpgradeDecision | null;
  /**
   * Headers after hop-by-hop removal and framing canonicalization, but before
   * the final Content-Length for a decoded chunked body is known.
   */
  readonly normalizedHeaders: ReadonlyArray<HeaderEntry>;
}

export interface ForwardingBoundary {
  readonly mode: "none" | "content-length" | "upgrade";
  readonly contentLength: number;
}

export interface MessageBoundary {
  readonly kind: FramingKind;
  /**
   * Declared Content-Length, or the decoded entity-body length for a chunked
   * request. Zero for requests without a body.
   */
  readonly contentLength: number;
  /**
   * Absolute byte offsets in the buffer supplied to `parseHttp1Message`.
   */
  readonly headEnd: number;
  readonly bodyStart: number;
  readonly bodyEnd: number;
  readonly wireBodyLength: number;
  readonly forwarding: ForwardingBoundary;
  readonly upgrade: UpgradeDecision | null;
}

export interface ForwardedRequest {
  readonly requestNumber: number;
  readonly method: string;
  readonly target: string;
  readonly version: "HTTP/1.0" | "HTTP/1.1";
  readonly headers: ReadonlyArray<HeaderEntry>;
  readonly body: Buffer;
  readonly boundary: MessageBoundary;
}

export interface Rejection {
  readonly error: BoundaryError;
  readonly requestNumber: number;
  readonly bufferedBytes: number;
  readonly closeConnection: true;
}
