import { originsEqual } from "./context.ts";
import {
  AddressDeniedError,
  AddressNotFoundError,
  AddressRevokedError,
  ReadAbortedError,
} from "./errors.ts";
import { LeasedReadStream } from "./leased-stream.ts";
import type {
  AddressBinding,
  Clock,
  CreateAddressOptions,
  DocumentContext,
  OpenReadOptions,
  RegistryEventMap,
  RegistryEventType,
  RegistryOptions,
  RevokeOptions,
} from "./types.ts";

interface AddressRecord {
  binding: AddressBinding;
  data: Uint8Array;
  revoked: boolean;
}

type Listener<T extends RegistryEventType> = (
  event: RegistryEventMap[T],
) => void;

const SYSTEM_CLOCK: Clock = { now: () => Date.now() };
const BLOB_SCHEME = "blob:";

/**
 * Registry of sandbox blob addresses.
 *
 * Possession of an address string is never enough to read. Each address is
 * bound to the creating origin, storage partition and instance generation,
 * and every read first proves that the calling document matches the binding
 * and is still active. Unauthorised failures collapse to a single denial
 * error so tenants cannot probe one another's address space.
 */
export class BlobAddressRegistry {
  private readonly records = new Map<string, AddressRecord>();
  private readonly activeStreams = new Map<string, Set<LeasedReadStream>>();
  private readonly listeners = new Map<
    RegistryEventType,
    Set<Listener<never>>
  >();
  private readonly defaultLeaseMs: number;
  private readonly defaultChunkSize: number;
  private readonly clock: Clock;
  private readonly tokenSource: () => string;
  private readonly urlPrefix: string;

  constructor(options: RegistryOptions = {}) {
    this.defaultLeaseMs = options.defaultLeaseMs ?? 30_000;
    this.defaultChunkSize = options.defaultChunkSize ?? 64 * 1024;
    this.clock = options.clock ?? SYSTEM_CLOCK;
    this.tokenSource = options.tokenSource ?? (() => crypto.randomUUID());
    this.urlPrefix = options.urlPrefix ?? BLOB_SCHEME;
  }

  /** Mint an address bound to the creating document's tenant tuple. */
  createAddress(options: CreateAddressOptions): AddressBinding {
    const { document } = options;
    this.requireActive(document);

    const data = this.normalizeData(options.data);
    const source = options.tokenSource ?? this.tokenSource;
    let token = source();
    while (this.records.has(token)) {
      token = source();
    }

    const now = this.clock.now();
    const binding: AddressBinding = {
      url: `${this.urlPrefix}${token}`,
      token,
      creatorOrigin: { ...document.origin },
      partition: document.partition,
      generation: document.generation,
      creatorDocumentId: document.id,
      contentType: options.contentType ?? "application/octet-stream",
      size: data.byteLength,
      createdAt: now,
    };
    this.records.set(token, { binding, data, revoked: false });
    return binding;
  }

  /**
   * Resolve an address into a leased read stream after verifying the
   * calling document. Revocation, missing addresses and tenant mismatch
   * all fail here; already-issued streams are unaffected by later changes.
   */
  async openRead(
    url: string,
    options: OpenReadOptions,
  ): Promise<LeasedReadStream> {
    const { document, signal } = options;

    if (signal?.aborted) {
      throw new ReadAbortedError();
    }

    const record = this.authorizedRecord(url, document);

    if (signal?.aborted) {
      throw new ReadAbortedError();
    }

    const leaseMs = options.leaseMs ?? this.defaultLeaseMs;
    if (!Number.isFinite(leaseMs) || leaseMs <= 0) {
      throw new RangeError("leaseMs must be a positive finite number");
    }
    const chunkSize = options.chunkSize ?? this.defaultChunkSize;
    if (!Number.isInteger(chunkSize) || chunkSize <= 0) {
      throw new RangeError("chunkSize must be a positive integer");
    }

    // Model the asynchronous open so an abort (for example document
    // navigation during the pending read) can cancel before issuance.
    await Promise.resolve();
    if (signal?.aborted) {
      throw new ReadAbortedError();
    }

    const stream = new LeasedReadStream({
      token: record.binding.token,
      data: record.data,
      absoluteExpiry: this.clock.now() + leaseMs,
      chunkSize,
      clock: this.clock,
      signal,
      onEnd: (detail) => {
        this.handleStreamEnd(stream, detail);
      },
    });

    let set = this.activeStreams.get(record.binding.token);
    if (!set) {
      set = new Set();
      this.activeStreams.set(record.binding.token, set);
    }
    set.add(stream);
    return stream;
  }

  /**
   * Revoke an address. New reads fail with {@link AddressRevokedError}
   * (or {@link AddressDeniedError} for other tenants); streams issued
   * before revocation keep running until their own leases end.
   */
  revoke(url: string, options: RevokeOptions): void {
    const record = this.authorizedRecord(url, options.document);
    if (record.revoked) {
      throw new AddressRevokedError();
    }
    record.revoked = true;
    this.emit("revoke", {
      token: record.binding.token,
      url: record.binding.url,
      revokedAt: this.clock.now(),
    });
  }

  /** Authorised inspection of a binding; never crosses tenant lines. */
  peek(url: string, document: DocumentContext): AddressBinding {
    return this.authorizedRecord(url, document).binding;
  }

  /** Existence check scoped to the calling document's tenant. */
  has(url: string, document: DocumentContext): boolean {
    try {
      this.authorizedRecord(url, document);
      return true;
    } catch {
      return false;
    }
  }

  /** Streams currently inside their lease for the given address. */
  activeStreamCount(url: string, document: DocumentContext): number {
    const token = this.authorizedToken(url, document);
    return this.activeStreams.get(token)?.size ?? 0;
  }

  /**
   * Drop every address belonging to an instance generation. Simulates the
   * registry state of a fresh sandbox instance: prior addresses simply do
   * not exist for documents carrying the new generation id.
   */
  purgeGeneration(generationId: string): number {
    let removed = 0;
    for (const [token, record] of this.records) {
      if (record.binding.generation.id === generationId) {
        this.records.delete(token);
        removed += 1;
      }
    }
    return removed;
  }

  addEventListener<T extends RegistryEventType>(
    type: T,
    listener: Listener<T>,
  ): void {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
    }
    set.add(listener as Listener<never>);
  }

  removeEventListener<T extends RegistryEventType>(
    type: T,
    listener: Listener<T>,
  ): void {
    this.listeners.get(type)?.delete(listener as Listener<never>);
  }

  private emit<T extends RegistryEventType>(
    type: T,
    event: RegistryEventMap[T],
  ): void {
    const set = this.listeners.get(type);
    if (!set) {
      return;
    }
    for (const listener of [...set]) {
      (listener as Listener<T>)(event);
    }
  }

  private handleStreamEnd(
    stream: LeasedReadStream,
    detail: RegistryEventMap["leaseend"],
  ): void {
    const set = this.activeStreams.get(stream.token);
    set?.delete(stream);
    if (set && set.size === 0) {
      this.activeStreams.delete(stream.token);
    }
    this.emit("leaseend", detail);
  }

  /**
   * Locate a record the document is authorised for. All unauthorised
   * outcomes (bad URL, missing, revoked elsewhere, tenant mismatch,
   * inactive document) surface as {@link AddressDeniedError}; authorised
   * callers receive the precise not-found/revoked error instead.
   */
  private authorizedRecord(
    url: string,
    document: DocumentContext,
  ): AddressRecord {
    const token = this.extractToken(url);
    const record = token === null ? undefined : this.records.get(token);
    this.assertAuthorized(record, document);
    if (record.revoked) {
      throw new AddressRevokedError();
    }
    return record;
  }

  /**
   * Tenant check without the revoked gate, for observability methods that
   * must keep working after revocation.
   */
  private authorizedToken(url: string, document: DocumentContext): string {
    const token = this.extractToken(url);
    const record = token === null ? undefined : this.records.get(token);
    this.assertAuthorized(record, document);
    return record.binding.token;
  }

  /**
   * Uniform tenant gate. Any failure (bad URL, unknown address, inactive
   * document or origin/partition/generation mismatch) is the same denial,
   * so unauthorised callers cannot probe the address space.
   */
  private assertAuthorized(
    record: AddressRecord | undefined,
    document: DocumentContext,
  ): asserts record is AddressRecord {
    const authorised =
      record !== undefined &&
      document.active === true &&
      originsEqual(document.origin, record.binding.creatorOrigin) &&
      document.partition.id === record.binding.partition.id &&
      document.generation.id === record.binding.generation.id;

    if (!authorised) {
      throw new AddressDeniedError();
    }
  }

  private extractToken(url: string): string | null {
    if (typeof url !== "string" || !url.startsWith(this.urlPrefix)) {
      return null;
    }
    const token = url.slice(this.urlPrefix.length);
    return token.length > 0 ? token : null;
  }

  private requireActive(document: DocumentContext): void {
    if (!document.active) {
      throw new AddressDeniedError("document is no longer active");
    }
  }

  private normalizeData(data: Uint8Array): Uint8Array {
    if (!(data instanceof Uint8Array)) {
      throw new TypeError("data must be a Uint8Array");
    }
    // Copy the bytes so later caller-side mutation cannot change the blob.
    const copy = new Uint8Array(data.byteLength);
    copy.set(data);
    return copy;
  }
}
