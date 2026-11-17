import type {
  ActivationBinding,
  ClipboardEnvironment,
  DocumentBinding,
  FocusBinding,
} from "./types.ts";

export interface TrackedClipboardEnvironmentOptions {
  readonly tabId?: string;
  readonly documentId?: string;
  readonly documentGeneration?: number;
  readonly focusGeneration?: number;
  readonly focusedTabId?: string | null;
  readonly activation?: ActivationBinding | null;
  readonly startTime?: number;
  readonly now?: () => number;
}

/**
 * Small host-side model that exposes monotonic document and focus
 * generations. A browser host should call `navigate`, `focus`, `blur` and
 * `activate` from its real lifecycle events.
 *
 * `getUserActivation` returns the latest activation snapshot, including an
 * expired one, so callers can distinguish "never active" from "activation
 * expired".
 */
export class TrackedClipboardEnvironment implements ClipboardEnvironment {
  private tabId: string;
  private documentId: string;
  private documentGeneration: number;
  private focusGeneration: number;
  private focusedTabId: string | null;
  private activation: ActivationBinding | null;
  private activationSequence = 0;
  private clock: number;
  private readonly nowFromHost: (() => number) | null;

  constructor(options: TrackedClipboardEnvironmentOptions = {}) {
    this.tabId = options.tabId ?? "tab-1";
    this.documentId = options.documentId ?? "document-1";
    this.documentGeneration = options.documentGeneration ?? 1;
    this.focusGeneration = options.focusGeneration ?? 1;
    this.focusedTabId =
      options.focusedTabId === undefined ? this.tabId : options.focusedTabId;
    this.activation = options.activation ?? null;
    this.clock = options.startTime ?? Date.now();
    this.nowFromHost = options.now ?? null;
  }

  now(): number {
    return this.nowFromHost === null ? this.clock : this.nowFromHost();
  }

  getDocument(): DocumentBinding {
    return {
      tabId: this.tabId,
      documentId: this.documentId,
      generation: this.documentGeneration,
    };
  }

  getFocus(): FocusBinding {
    return {
      generation: this.focusGeneration,
      tabId: this.focusedTabId,
    };
  }

  getUserActivation(): ActivationBinding | null {
    return this.activation;
  }

  /** Simulates navigation in the current tab. */
  navigate(documentId?: string): void {
    this.documentId =
      documentId ?? `document-${this.documentGeneration + 1}`;
    this.documentGeneration += 1;
  }

  /** Simulates focus moving to `tabId`, even if it is already focused. */
  focus(tabId: string = this.tabId): void {
    this.focusedTabId = tabId;
    this.focusGeneration += 1;
  }

  /** Simulates the host losing focus without closing the document. */
  blur(): void {
    this.focusedTabId = null;
    this.focusGeneration += 1;
  }

  /** Switches the current tab context and focuses the new tab. */
  switchTab(tabId: string, documentId?: string): void {
    this.tabId = tabId;
    this.documentId = documentId ?? `${tabId}-document-1`;
    this.documentGeneration += 1;
    this.focus(tabId);
  }

  /** Creates a fresh transient user activation. */
  activate(ttlMs = 5_000, activationId?: string): ActivationBinding {
    this.activationSequence += 1;
    this.activation = {
      activationId:
        activationId ?? `activation-${this.activationSequence}`,
      expiresAt: this.now() + ttlMs,
    };
    return { ...this.activation };
  }

  clearActivation(): void {
    this.activation = null;
  }

  advanceTime(milliseconds: number): void {
    this.clock += milliseconds;
  }
}

export function createTrackedEnvironment(
  options: TrackedClipboardEnvironmentOptions = {},
): TrackedClipboardEnvironment {
  return new TrackedClipboardEnvironment(options);
}
