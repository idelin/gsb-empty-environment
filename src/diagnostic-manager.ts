import { VersionVector } from "./version-vector.ts";
import type { Diagnostic, FileStamp, VectorEntry } from "./types.ts";

export type { Diagnostic, FileStamp, VectorEntry };
export { VersionVector };

/** Provides the direct dependencies (imports, includes, ...) of a file. */
export interface DependencyProvider {
  getDependencies(uri: string): Iterable<string>;
}

/** Background diagnostics engine. May resolve out of order and may ignore cancellation. */
export interface DiagnosticsProvider {
  provideDiagnostics(uri: string, vector: VersionVector, signal: AbortSignal): Promise<Diagnostic[]>;
}

export interface DiagnosticsPublishedEvent {
  readonly uri: string;
  readonly diagnostics: readonly Diagnostic[];
  readonly vector: VersionVector;
}

interface CachedDiagnostics {
  readonly vector: VersionVector;
  readonly diagnostics: readonly Diagnostic[];
}

interface PendingRequest {
  readonly id: number;
  readonly controller: AbortController;
  superseded: boolean;
}

export interface DiagnosticManagerOptions {
  readonly provider: DiagnosticsProvider;
  readonly dependencies?: DependencyProvider;
}

/**
 * Manages diagnostics for an editor that receives a continuous stream of
 * file versions while a background engine computes diagnostics
 * asynchronously (and possibly out of order).
 *
 * Core invariant: a diagnostic result is published or reused only while the
 * version vector over its full transitive dependency closure still matches
 * the current file state exactly. Matching the target file's own version
 * alone is never sufficient.
 */
export class DiagnosticManager {
  private readonly provider: DiagnosticsProvider;
  private readonly dependencies: DependencyProvider;

  /** Currently open files and their stamps. */
  private readonly files = new Map<string, FileStamp>();
  /**
   * Per-file generation counter. Survives close/delete so a reopened or
   * recreated file never collides with stamps issued to its previous life.
   */
  private readonly epochCounters = new Map<string, number>();

  private readonly cache = new Map<string, CachedDiagnostics>();
  private readonly pending = new Map<string, PendingRequest>();
  private requestSeq = 0;

  private readonly listeners = new Set<(event: DiagnosticsPublishedEvent) => void>();

  constructor(options: DiagnosticManagerOptions) {
    this.provider = options.provider;
    this.dependencies = options.dependencies ?? { getDependencies: () => [] };
  }

  // ---------------------------------------------------------------- lifecycle

  /** Registers a freshly opened file. Starts a new epoch for the uri. */
  openFile(uri: string, initialVersion: number): void {
    if (this.files.has(uri)) {
      throw new Error(`file is already open: ${uri}`);
    }
    const epoch = (this.epochCounters.get(uri) ?? 0) + 1;
    this.epochCounters.set(uri, epoch);
    this.files.set(uri, { epoch, version: initialVersion });
  }

  /**
   * Records a new version of an open file. Versions must arrive as a
   * continuous, strictly increasing sequence; stale or duplicate versions
   * are rejected.
   */
  updateFile(uri: string, version: number): void {
    const current = this.files.get(uri);
    if (current === undefined) {
      throw new Error(`cannot update a file that is not open: ${uri}`);
    }
    if (version <= current.version) {
      throw new Error(
        `non-continuous version for ${uri}: got ${version}, current is ${current.version}`,
      );
    }
    this.files.set(uri, { epoch: current.epoch, version });
  }

  /** Closes a file. Pending requests for it are cancelled and will never publish. */
  closeFile(uri: string): void {
    this.retireFile(uri);
  }

  /**
   * Deletes a file from the workspace. Any diagnostics whose dependency
   * closure contains the file become invalid immediately (their vector
   * recorded a present stamp that no longer exists).
   */
  deleteFile(uri: string): void {
    this.retireFile(uri);
  }

  private retireFile(uri: string): void {
    this.files.delete(uri);
    const request = this.pending.get(uri);
    if (request !== undefined) {
      request.superseded = true;
      request.controller.abort();
      this.pending.delete(uri);
    }
  }

  // ------------------------------------------------------------- inspection

  getStamp(uri: string): FileStamp | undefined {
    return this.files.get(uri);
  }

  /**
   * Computes the transitive dependency closure of `uri` (including `uri`
   * itself) and snapshots the current stamp of every member. Absent files
   * are recorded as `null` entries.
   */
  snapshotVector(uri: string): VersionVector {
    const entries = new Map<string, VectorEntry>();
    const queue: string[] = [uri];
    while (queue.length > 0) {
      const current = queue.pop() as string;
      if (entries.has(current)) {
        continue;
      }
      entries.set(current, this.files.get(current) ?? null);
      for (const dep of this.dependencies.getDependencies(current)) {
        if (!entries.has(dep)) {
          queue.push(dep);
        }
      }
    }
    return new VersionVector(entries);
  }

  /** Whether every entry of `vector` still matches the current file state. */
  isVectorCurrent(vector: VersionVector): boolean {
    for (const [uri, entry] of vector.entries) {
      if (!this.files.has(uri) && entry === null) {
        continue;
      }
      const current = this.files.get(uri) ?? null;
      if (current === null || entry === null) {
        return false;
      }
      if (current.epoch !== entry.epoch || current.version !== entry.version) {
        return false;
      }
    }
    return true;
  }

  // ------------------------------------------------------------- diagnostics

  /**
   * Returns cached diagnostics for `uri` iff they are still exactly valid:
   * the cached version vector must equal a freshly snapshotted vector over
   * the current dependency closure. This is the precise reuse condition —
   * any edit, close/reopen, deletion, or dependency-graph change anywhere
   * in the closure invalidates the cache.
   */
  getReusableDiagnostics(uri: string): readonly Diagnostic[] | undefined {
    const cached = this.cache.get(uri);
    if (cached === undefined) {
      return undefined;
    }
    if (!this.files.has(uri)) {
      return undefined;
    }
    const current = this.snapshotVector(uri);
    return cached.vector.equals(current) ? cached.diagnostics : undefined;
  }

  /**
   * Requests diagnostics for `uri`.
   *
   * - Returns cached diagnostics synchronously (via a resolved promise) when
   *   the reuse condition holds, without calling the provider.
   * - Otherwise cancels any previous pending request for the same uri and
   *   issues a new one. If the stale request's cancellation fails and it
   *   still resolves, its result is discarded (superseded + vector check).
   * - The result is published and cached only if the version vector
   *   captured at request time still matches when the provider resolves.
   *
   * Resolves to `undefined` when the result was discarded.
   */
  async requestDiagnostics(uri: string): Promise<readonly Diagnostic[] | undefined> {
    if (!this.files.has(uri)) {
      throw new Error(`cannot request diagnostics for a file that is not open: ${uri}`);
    }

    const vector = this.snapshotVector(uri);

    const cached = this.cache.get(uri);
    if (cached !== undefined && cached.vector.equals(vector)) {
      return cached.diagnostics;
    }

    const previous = this.pending.get(uri);
    if (previous !== undefined) {
      previous.superseded = true;
      previous.controller.abort();
    }

    const record: PendingRequest = {
      id: ++this.requestSeq,
      controller: new AbortController(),
      superseded: false,
    };
    this.pending.set(uri, record);

    let diagnostics: Diagnostic[];
    try {
      diagnostics = await this.provider.provideDiagnostics(uri, vector, record.controller.signal);
    } catch (error) {
      if (record.controller.signal.aborted || record.superseded) {
        return undefined;
      }
      throw error;
    } finally {
      if (this.pending.get(uri) === record) {
        this.pending.delete(uri);
      }
    }

    // Guard 1: a newer request (or a close/delete) superseded this one.
    // Handles cancellation failure: even if the provider ignored abort()
    // and resolved anyway, the stale result never publishes.
    if (record.superseded) {
      return undefined;
    }

    // Guard 2: the world moved on while computing — an edit, a
    // close/reopen (epoch bump), or a deleted dependency anywhere in the
    // closure invalidates the result, even if this file's own version is
    // unchanged.
    if (!this.isVectorCurrent(vector)) {
      return undefined;
    }

    this.cache.set(uri, { vector, diagnostics });
    const event: DiagnosticsPublishedEvent = { uri, diagnostics, vector };
    for (const listener of this.listeners) {
      listener(event);
    }
    return diagnostics;
  }

  /** Last published diagnostics for `uri`, or undefined if none are valid. */
  getPublishedDiagnostics(uri: string): readonly Diagnostic[] | undefined {
    return this.getReusableDiagnostics(uri);
  }

  onDiagnosticsPublished(listener: (event: DiagnosticsPublishedEvent) => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
}
