import type {
  DocumentContext,
  InstanceGeneration,
  SecurityOrigin,
  StoragePartition,
} from "./types.ts";

let documentSequence = 0;

/**
 * A {@link DocumentContext} implementation that owns its own lifecycle.
 * Navigation and close flip `active` to false and abort the document's
 * lifecycle signal, which cancels any pending reads made through it.
 */
export class SandboxDocument implements DocumentContext {
  readonly id: string;
  readonly origin: SecurityOrigin;
  readonly partition: StoragePartition;
  readonly generation: InstanceGeneration;
  private activeFlag = true;
  private lifecycle: AbortController;

  constructor(
    origin: SecurityOrigin,
    partition: StoragePartition,
    generation: InstanceGeneration,
    id?: string,
  ) {
    this.id = id ?? `doc-${++documentSequence}`;
    this.origin = { ...origin };
    this.partition = partition;
    this.generation = generation;
    this.lifecycle = new AbortController();
  }

  get active(): boolean {
    return this.activeFlag;
  }

  /** Aborted on navigation or close; useful as an open-read signal. */
  get lifecycleSignal(): AbortSignal {
    return this.lifecycle.signal;
  }

  /** Mark the document inactive and abort reads waiting on its signal. */
  deactivate(): void {
    if (!this.activeFlag) {
      return;
    }
    this.activeFlag = false;
    this.lifecycle.abort();
  }
}

export interface NavigationResult {
  readonly previous: SandboxDocument;
  readonly current: SandboxDocument;
  /** True when the new document remains inside the same tenant binding. */
  readonly sameOriginPartition: boolean;
}

/**
 * Minimal browsing-context model. Every navigation retires the previous
 * document (deactivating it and aborting its pending reads) and installs a
 * fresh document, optionally in a new origin or storage partition.
 */
export class BrowsingContext {
  private currentDocument: SandboxDocument;

  constructor(initial: SandboxDocument) {
    this.currentDocument = initial;
  }

  get document(): SandboxDocument {
    return this.currentDocument;
  }

  navigate(next: {
    origin?: SecurityOrigin;
    partition?: StoragePartition;
    generation?: InstanceGeneration;
  }): NavigationResult {
    const previous = this.currentDocument;
    const sameOriginPartition =
      originsEqual(previous.origin, next.origin ?? previous.origin) &&
      previous.partition.id === (next.partition ?? previous.partition).id;

    const current = new SandboxDocument(
      next.origin ?? previous.origin,
      next.partition ?? previous.partition,
      next.generation ?? previous.generation,
    );
    previous.deactivate();
    this.currentDocument = current;
    return { previous, current, sameOriginPartition };
  }

  close(): void {
    this.currentDocument.deactivate();
  }
}

export function originsEqual(a: SecurityOrigin, b: SecurityOrigin): boolean {
  return a.scheme === b.scheme && a.host === b.host && a.port === b.port;
}
