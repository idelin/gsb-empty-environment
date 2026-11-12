export interface AcctIdentifier {
  /** The URI exactly as supplied to the parser. */
  readonly uri: string;
  /** The local part of the acct URI. */
  readonly user: string;
  /** Normalized authority used for WebFinger queries (host plus non-default port). */
  readonly host: string;
  /** Normalized hostname without a port. */
  readonly hostname: string;
  /** Non-default port, or the empty string. */
  readonly port: string;
}

export interface JrdLink {
  readonly rel: string;
  readonly href?: string;
  readonly template?: string;
  readonly type?: string;
  readonly titles?: Record<string, string>;
  readonly properties?: Record<string, string>;
  readonly [member: string]: unknown;
}

export interface Jrd {
  readonly subject: string;
  readonly aliases?: readonly string[];
  readonly properties?: Record<string, string>;
  readonly links?: readonly JrdLink[];
  readonly [member: string]: unknown;
}

export interface HostMetaLink {
  readonly rel: string;
  readonly href?: string;
  readonly template?: string;
  readonly type?: string;
  readonly [member: string]: unknown;
}

export type FetchHeaders = {
  get(name: string): string | null;
};

/**
 * The small part of the Fetch API used by this package. Keeping the transport
 * injectable lets callers use their own hardened HTTP stack and lets tests
 * assert exactly which hosts are contacted.
 */
export interface FetchResponse {
  readonly status: number;
  readonly headers: FetchHeaders;
  text(): Promise<string>;
}

export interface FetchInit {
  readonly method?: string;
  readonly redirect?: "manual";
  readonly headers?: Record<string, string>;
  readonly signal?: AbortSignal;
}

export type FetchLike = (url: string, init: FetchInit) => Promise<FetchResponse>;

export interface WebFingerOptions {
  /** Transport override. Defaults to the platform Fetch API. */
  readonly fetch?: FetchLike;
  /** Additional request headers. Policy headers cannot be removed. */
  readonly headers?: Record<string, string>;
  /** Request timeout in milliseconds. Defaults to 10 seconds. */
  readonly timeoutMs?: number;
  /**
   * Hosts (hostname or hostname:port) whose external `rel=self` links may be
   * retained. Matching is case-insensitive and exact on the authority.
   */
  readonly allowedSelfHosts?: readonly string[];
  /**
   * Consult `/.well-known/host-meta` before the standard WebFinger endpoint.
   * An invalid host-meta document is ignored and resolution falls back to the
   * standard same-host endpoint.
   */
  readonly useHostMeta?: boolean;
  /**
   * A caller-supplied lrdd template. It is still subject to the same
   * `{uri}`, HTTPS, and same-host checks as a discovered template.
   */
  readonly lrddTemplate?: string;
}

export interface AccountResolution {
  readonly acct: AcctIdentifier;
  readonly subject: string;
  readonly jrd: Jrd;
  readonly links: readonly JrdLink[];
  readonly endpoint: string;
  readonly redirects: number;
  readonly hostMetaEndpoint?: string;
}
