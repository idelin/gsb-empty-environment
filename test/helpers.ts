import type {
  ActivationBinding,
  ClipboardEnvironment,
  DocumentBinding,
  FocusBinding,
  SystemClipboard,
} from "../index.ts";

export class TestClock {
  value: number;
  readonly now: () => number;

  constructor(start = 1_000) {
    this.value = start;
    this.now = () => this.value;
  }

  advance(milliseconds: number): void {
    this.value += milliseconds;
  }
}

/** Focus is host-global, so tabs share this object in multi-tab tests. */
export class SharedFocus {
  generation = 1;
  tabId: string | null;

  constructor(tabId: string | null) {
    this.tabId = tabId;
  }

  moveTo(tabId: string): void {
    this.tabId = tabId;
    this.generation += 1;
  }

  blur(): void {
    this.tabId = null;
    this.generation += 1;
  }
}

export class TabEnvironment implements ClipboardEnvironment {
  readonly tabId: string;
  private readonly focus: SharedFocus;
  private readonly clock: TestClock;
  private documentId: string;
  private documentGeneration: number;
  private activation: ActivationBinding | null = null;
  private activationSequence = 0;

  constructor(
    tabId: string,
    focus: SharedFocus,
    clock: TestClock,
    documentId = `${tabId}-document-1`,
    documentGeneration = 1,
  ) {
    this.tabId = tabId;
    this.focus = focus;
    this.clock = clock;
    this.documentId = documentId;
    this.documentGeneration = documentGeneration;
  }

  now(): number {
    return this.clock.now();
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
      generation: this.focus.generation,
      tabId: this.focus.tabId,
    };
  }

  getUserActivation(): ActivationBinding | null {
    return this.activation;
  }

  activate(ttlMs = 5_000): ActivationBinding {
    this.activationSequence += 1;
    this.activation = {
      activationId: `${this.tabId}-activation-${this.activationSequence}`,
      expiresAt: this.clock.now() + ttlMs,
    };
    return { ...this.activation };
  }

  clearActivation(): void {
    this.activation = null;
  }

  navigate(documentId = `${this.tabId}-document-${this.documentGeneration + 1}`): void {
    this.documentId = documentId;
    this.documentGeneration += 1;
  }
}

export class RecordingClipboard implements SystemClipboard {
  readonly writes: string[] = [];
  reads = 0;
  readValue = "clipboard text";
  throwOnWrite: Error | null = null;
  throwOnRead: Error | null = null;

  get callCount(): number {
    return this.writes.length + this.reads;
  }

  writeText(text: string): Promise<void> {
    if (this.throwOnWrite !== null) {
      return Promise.reject(this.throwOnWrite);
    }
    this.writes.push(text);
    return Promise.resolve();
  }

  readText(): Promise<string> {
    if (this.throwOnRead !== null) {
      return Promise.reject(this.throwOnRead);
    }
    this.reads += 1;
    return Promise.resolve(this.readValue);
  }
}
