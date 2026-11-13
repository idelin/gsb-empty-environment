/**
 * Resource categories that participate in mixed-content decisions.
 *
 * Scripts, XHR/fetch, modules and WebSockets are active (optionally-blockable
 * in older terminology, but blocked outright here); images are passive and
 * get a single upgrade attempt instead of being blocked immediately.
 */
export type ResourceKind =
  | "script"
  | "xhr"
  | "fetch"
  | "module"
  | "websocket"
  | "image"
  | "audiovideo"
  | "style"
  | "font"
  | "other";

/** Why a request was blocked. */
export type BlockReason =
  | "active-mixed-content"
  | "upgrade-failed"
  | "invalid-url";

/** Inputs describing one outgoing request and the document that triggers it. */
export interface MixedContentRequest {
  /** Absolute URL of the embedding document. Relative resource URLs resolve against it. */
  documentUrl?: string | URL;
  /** Absolute or relative URL of the resource being fetched. */
  resourceUrl: string | URL;
  /** What the resource is used for; drives the upgrade-vs-block policy. */
  kind: ResourceKind;
  /**
   * Whether the embedding document is a secure context. Defaults to true when
   * the document URL itself is an https/wss URL.
   */
  isSecureContext?: boolean;
}

/** A request that survived the mixed-content check and is ready to be sent. */
export interface AllowedRequest {
  allowed: true;
  /** The URL that should actually be requested (upgraded when applicable). */
  url: URL;
  kind: ResourceKind;
  /** True when the original plaintext URL was upgraded to a secure scheme. */
  upgraded: boolean;
}

/** A request the browser must not send. */
export interface BlockedRequest {
  allowed: false;
  kind: ResourceKind;
  reason: BlockReason;
  /** The original resource URL, kept for diagnostics; never request it. */
  originalUrl: string;
  /**
   * The secure URL attempted for passive content (for example an image whose
   * https upgrade failed); absent when no upgrade was attempted.
   */
  upgradedUrl?: URL;
}

export type MixedContentDecision = AllowedRequest | BlockedRequest;

/** Minimal fetch-shaped hook used to probe an upgraded image URL. */
export type UpgradeProbe = (url: URL) => Promise<boolean>;

/** Options for the request-assembly helper. */
export interface AssembleOptions extends MixedContentRequest {
  /**
   * Probes whether an upgraded image URL is reachable. Defaults to an HTTP
   * HEAD/GET probe. A failed probe means the image is treated as failed: the
   * plaintext URL is never retried.
   */
  probe?: UpgradeProbe;
}
