export type Site = string & { readonly __site: unique symbol };

export type FrameId = string;

export interface GrantKey {
  embeddedSite: Site;
  topLevelSite: Site;
}

export type SandboxTokenSet = ReadonlySet<string>;

export type StorageAccessErrorName =
  | "SecurityError"
  | "InvalidStateError"
  | "NotAllowedError";

export interface Clock {
  now(): number;
}

export interface FrameContext {
  readonly id: FrameId;
  readonly origin: string;
  readonly topLevelOrigin: string;
  readonly isTopLevel: boolean;
  /** null = 未加 sandbox（不受 sandbox 限制）；空 Set = sandbox 但无任何令牌 */
  readonly sandboxTokens: SandboxTokenSet | null;
}

export interface StorageAccessHostOptions {
  clock?: Clock;
  activationTtlMs?: number;
  frames?: Iterable<FrameContext>;
}
