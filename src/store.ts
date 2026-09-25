/**
 * Asynchronous, versioned attribute store. Every attribute read carries the
 * version observed at read time so the engine can detect mid-request
 * attribute updates and refuse to mix versions.
 */
export interface ReadResult {
  readonly value: unknown; // undefined means the attribute is missing
  readonly version: number;
}

export interface AttrProvider {
  read(subject: string, attr: string, signal?: AbortSignal): Promise<ReadResult>;
  /** Current versions without side effects (no read counting, no faults). */
  versions(subject: string, attrs: readonly string[]): Promise<Map<string, number>>;
}

export class AbortError extends Error {
  constructor(message = 'attribute read aborted') {
    super(message);
    this.name = 'AbortError';
  }
}

type Cell = { value: unknown; version: number };
type ReadHook = (subject: string, attr: string) => void | Promise<void>;
type ChangeListener = (subject: string, attr: string) => void;
interface Fault {
  match: (subject: string, attr: string) => boolean;
  error: Error;
  used: boolean;
}

const keyOf = (subject: string, attr: string) => `${subject}::${attr}`;

export class InMemoryAttrStore implements AttrProvider {
  private readonly cells = new Map<string, Cell>();
  private readonly hooks: ReadHook[] = [];
  private readonly listeners: ChangeListener[] = [];
  private readonly faults: Fault[] = [];
  /** read counts per `subject::attr`, for tests and cache auditing. */
  readonly readCounts = new Map<string, number>();

  readCount(subject: string, attr: string): number {
    return this.readCounts.get(keyOf(subject, attr)) ?? 0;
  }

  set(subject: string, attr: string, value: unknown): void {
    const k = keyOf(subject, attr);
    const prev = this.cells.get(k);
    this.cells.set(k, { value, version: (prev?.version ?? 0) + 1 });
    for (const l of this.listeners) l(subject, attr);
  }

  remove(subject: string, attr: string): void {
    const k = keyOf(subject, attr);
    const prev = this.cells.get(k);
    this.cells.set(k, { value: undefined, version: (prev?.version ?? 0) + 1 });
    for (const l of this.listeners) l(subject, attr);
  }

  versionOf(subject: string, attr: string): number {
    return this.cells.get(keyOf(subject, attr))?.version ?? 0;
  }

  /** Hook invoked after each successful read (may be async, e.g. hot-update). */
  addReadHook(hook: ReadHook): void {
    this.hooks.push(hook);
  }

  onChange(listener: ChangeListener): void {
    this.listeners.push(listener);
  }

  /** The next matching read throws `error`. One-shot. */
  failNextRead(match: (subject: string, attr: string) => boolean, error: Error): void {
    this.faults.push({ match, error, used: false });
  }

  async read(subject: string, attr: string, signal?: AbortSignal): Promise<ReadResult> {
    if (signal?.aborted) throw new AbortError();
    const k = keyOf(subject, attr);
    this.readCounts.set(k, (this.readCounts.get(k) ?? 0) + 1);
    for (const f of this.faults) {
      if (!f.used && f.match(subject, attr)) {
        f.used = true;
        throw f.error;
      }
    }
    const cell = this.cells.get(k);
    const result: ReadResult = { value: cell?.value, version: cell?.version ?? 0 };
    for (const h of this.hooks) await h(subject, attr);
    if (signal?.aborted) throw new AbortError();
    return result;
  }

  async versions(subject: string, attrs: readonly string[]): Promise<Map<string, number>> {
    const out = new Map<string, number>();
    for (const a of attrs) out.set(a, this.versionOf(subject, a));
    return out;
  }
}
