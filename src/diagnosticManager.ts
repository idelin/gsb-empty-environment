import { CancellationTokenSource } from "./cancellation.ts";
import type { CancellationToken } from "./cancellation.ts";
import type { Diagnostic, DiagnosticsChangedEvent, Uri } from "./types.ts";
import {
  hashContent,
  vectorMatches,
  type FileStamp,
  type VersionVector,
} from "./versionVector.ts";

interface OpenDocument {
  content: string;
  stamp: FileStamp;
}

/**
 * Context handed to the diagnostic computer. Every `read` is recorded into
 * the request's version vector, so the vector reflects exactly the files the
 * computation actually depended on.
 */
export interface ComputeContext {
  /** Read a dependency file; records it into the request's version vector. */
  read(uri: Uri): string | undefined;
  token: CancellationToken;
}

export type DiagnosticComputer = (
  context: ComputeContext,
) => Promise<Diagnostic[]>;

interface DiagnosticEntry {
  diagnostics: readonly Diagnostic[];
  vector: VersionVector;
}

interface PendingRequest {
  id: number;
  primary: Uri;
  vector: Map<Uri, FileStamp | null>;
  source: CancellationTokenSource;
  settled: boolean;
}

type DiagnosticsChangedListener = (event: DiagnosticsChangedEvent) => void;

/**
 * Manages diagnostics for continuously edited files where background
 * computations may resolve out of order.
 *
 * Core invariant: a diagnostic set is displayed only while its version
 * vector (primary file + every dependency read during computation) still
 * matches the current document state. Equality of the primary file's
 * version alone is never sufficient.
 *
 * Reuse condition (exact): cached diagnostics for a file are reused without
 * recomputation iff their stored vector matches the current state under
 * `vectorMatches`, i.e.
 *   - the file itself is open with an identical (version, contentHash)
 *     stamp,
 *   - every recorded dependency is open with an identical stamp, and
 *   - every file recorded as absent is still absent.
 * This lets diagnostics survive a close/reopen cycle with unchanged content
 * and unchanged dependencies, while any content or dependency drift forces
 * recomputation.
 */
export class DiagnosticManager {
  private documents = new Map<Uri, OpenDocument>();
  /** Diagnostics currently safe to display; invalidated eagerly. */
  private published = new Map<Uri, DiagnosticEntry>();
  /** Last known good results; validated lazily, survives close/reopen. */
  private cache = new Map<Uri, DiagnosticEntry>();
  private pending = new Set<PendingRequest>();
  private listeners: Array<DiagnosticsChangedListener> = [];
  private nextRequestId = 1;
  private readonly computer: DiagnosticComputer;

  constructor(computer: DiagnosticComputer) {
    this.computer = computer;
  }

  // ---------------------------------------------------------------- documents

  open(uri: Uri, content: string): void {
    this.documents.set(uri, {
      content,
      stamp: { version: 1, contentHash: hashContent(content) },
    });
    this.onStateChanged(uri);
  }

  change(uri: Uri, content: string): void {
    const doc = this.documents.get(uri);
    if (!doc) throw new Error(`Cannot change a file that is not open: ${uri}`);
    doc.content = content;
    doc.stamp = {
      version: doc.stamp.version + 1,
      contentHash: hashContent(content),
    };
    this.onStateChanged(uri);
  }

  /** Closing and deleting are equivalent for validity: the file is gone. */
  close(uri: Uri): void {
    if (!this.documents.delete(uri)) return;
    this.onStateChanged(uri);
  }

  deleteFile(uri: Uri): void {
    this.close(uri);
  }

  isOpen(uri: Uri): boolean {
    return this.documents.has(uri);
  }

  currentStamp(uri: Uri): FileStamp | undefined {
    return this.documents.get(uri)?.stamp;
  }

  // -------------------------------------------------------------- diagnostics

  getDiagnostics(uri: Uri): readonly Diagnostic[] {
    return this.published.get(uri)?.diagnostics ?? [];
  }

  onDidChangeDiagnostics(listener: DiagnosticsChangedListener): void {
    this.listeners.push(listener);
  }

  /**
   * Requests diagnostics for `uri`. Reuses the cached result when its
   * version vector still matches; otherwise starts a background
   * computation, cancelling any superseded pending request for the file.
   */
  requestDiagnostics(uri: Uri): void {
    const doc = this.documents.get(uri);
    if (!doc) throw new Error(`Cannot diagnose a file that is not open: ${uri}`);

    const cached = this.cache.get(uri);
    if (cached) {
      if (vectorMatches(cached.vector, this)) {
        if (this.published.get(uri) !== cached) {
          this.published.set(uri, cached);
          this.emit({ uri, diagnostics: cached.diagnostics });
        }
        return;
      }
      this.cache.delete(uri);
    }

    for (const request of [...this.pending]) {
      if (request.primary === uri) this.cancelRequest(request);
    }

    const request: PendingRequest = {
      id: this.nextRequestId++,
      primary: uri,
      vector: new Map([[uri, doc.stamp]]),
      source: new CancellationTokenSource(),
      settled: false,
    };
    this.pending.add(request);

    const context: ComputeContext = {
      read: (depUri: Uri): string | undefined => {
        const dep = this.documents.get(depUri);
        request.vector.set(depUri, dep ? dep.stamp : null);
        return dep?.content;
      },
      token: request.source.token,
    };

    let result: Promise<Diagnostic[]>;
    try {
      result = Promise.resolve(this.computer(context));
    } catch {
      this.settleRequest(request);
      return;
    }
    result.then(
      (diagnostics) => this.completeRequest(request, diagnostics),
      () => this.settleRequest(request),
    );
  }

  pendingRequestCount(): number {
    return this.pending.size;
  }

  // ----------------------------------------------------------------- internal

  private completeRequest(
    request: PendingRequest,
    diagnostics: Diagnostic[],
  ): void {
    this.settleRequest(request);
    // A stale request is discarded here even if its cancellation failed and
    // the computer still produced a result: the version vector is the
    // single source of truth for validity.
    if (request.source.token.cancelled) return;
    if (!vectorMatches(request.vector, this)) return;
    const entry: DiagnosticEntry = {
      diagnostics,
      vector: new Map(request.vector),
    };
    this.published.set(request.primary, entry);
    this.cache.set(request.primary, entry);
    this.emit({ uri: request.primary, diagnostics });
  }

  private settleRequest(request: PendingRequest): void {
    if (request.settled) return;
    request.settled = true;
    this.pending.delete(request);
  }

  private cancelRequest(request: PendingRequest): void {
    request.source.cancel();
    this.settleRequest(request);
  }

  /**
   * Any state change to `changedUri` cancels pending requests that depend
   * on it and invalidates displayed diagnostics whose vector includes it.
   * The reuse cache is deliberately left untouched here: it is validated
   * lazily against the version vector at reuse time, which is what allows
   * results to survive a close/reopen cycle with identical content.
   */
  private onStateChanged(changedUri: Uri): void {
    for (const request of [...this.pending]) {
      if (request.primary === changedUri || request.vector.has(changedUri)) {
        this.cancelRequest(request);
      }
    }
    for (const [uri, entry] of [...this.published]) {
      if (entry.vector.has(changedUri) && !vectorMatches(entry.vector, this)) {
        this.published.delete(uri);
        this.emit({ uri, diagnostics: [] });
      }
    }
  }

  private emit(event: DiagnosticsChangedEvent): void {
    for (const listener of this.listeners) listener(event);
  }
}
