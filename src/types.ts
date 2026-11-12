/**
 * Security origin of a document. Only `scheme`, `host` and `port` are
 * compared; paths and fragments are deliberately irrelevant.
 */
export interface SecurityOrigin {
  scheme: string;
  host: string;
  port: number | null;
}

/**
 * Storage partition (the tenant boundary). Documents in different
 * partitions are isolated even when their origins are identical.
 */
export interface StoragePartition {
 readonly id: string;
}

/**
 * Instance generation. Every sandbox instance gets a fresh generation id,
 * so addresses minted by a previous instance never resolve again.
 */
export interface InstanceGeneration {
  readonly id: string;
}

/**
 * A document that may attempt to read a blob address. The registry only
 * ever reads these fields; equality is structural.
 */
export interface DocumentContext {
  readonly id: string;
  readonly origin: SecurityOrigin;
  readonly partition: StoragePartition;
  readonly generation: InstanceGeneration;
  readonly active: boolean;
}

/**
 * Immutable binding attached to every minted address. The address string
 * alone is never sufficient to read: all four dimensions must match the
 * calling document at read time.
 */
export interface AddressBinding {
  readonly url: string;
  readonly token: string;
  readonly creatorOrigin: SecurityOrigin;
  readonly partition: StoragePartition;
  readonly generation: InstanceGeneration;
  readonly creatorDocumentId: string;
  readonly contentType: string;
  readonly size: number;
  readonly createdAt: number;
}

export interface CreateAddressOptions {
  /** Owning document; its origin/partition/generation become the binding. */
  readonly document: DocumentContext;
  readonly data: Uint8Array;
  readonly contentType?: string;
  /** Override the default token source (mainly for deterministic tests). */
  readonly tokenSource?: () => string;
}

export interface OpenReadOptions {
  readonly document: DocumentContext;
  /** Aborts the pending open; a pre-open signal never yields a stream. */
  readonly signal?: AbortSignal;
  /** Per-stream lease duration; defaults to the registry default. */
  readonly leaseMs?: number;
  /** Bytes delivered per `read()` call; defaults to the registry default. */
  readonly chunkSize?: number;
}

export interface RevokeOptions {
  readonly document: DocumentContext;
}

export interface RegistryOptions {
  /** Default lease duration in milliseconds (default 30_000). */
  readonly defaultLeaseMs?: number;
  /** Default bytes per read chunk (default 64 KiB). */
  readonly defaultChunkSize?: number;
  readonly clock?: Clock;
  /** Global token source; per-create `tokenSource` takes precedence. */
  readonly tokenSource?: () => string;
  /** URL prefix (default `blob:`). */
  readonly urlPrefix?: string;
}

export interface Clock {
  now(): number;
}

export type LeaseEndReason = "complete" | "expired" | "cancelled";

export interface LeaseEndDetail {
  readonly token: string;
  readonly reason: LeaseEndReason;
  readonly endedAt: number;
  readonly bytesRead: number;
}

export type RegistryEventType = "revoke" | "leaseend";

export interface RegistryEventMap {
  revoke: { token: string; url: string; revokedAt: number };
  leaseend: LeaseEndDetail;
}
