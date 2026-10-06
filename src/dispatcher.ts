/**
 * Keyboard shortcut dispatcher with multi-key sequences.
 *
 * The dispatcher is driven exclusively by explicit clock events: it never
 * reads wall-clock time and never starts real timers. Every event carries a
 * logical timestamp supplied by the caller, which makes the full behavior
 * deterministic and replayable in tests.
 *
 * Core rules:
 * - A short sequence that is also a prefix of a longer sequence waits for a
 *   logical deadline (`time + config.timeoutMs`) before its command fires.
 * - Timeouts fire when a timestamped event (key or tick) arrives with
 *   `deadline <= time`. A key landing exactly on the deadline therefore sees
 *   the short command fire first, then the key is matched from scratch.
 * - On mismatch (the buffered keys plus the new key match nothing), the
 *   pending short command (if any) fires first, then the current key is
 *   re-matched as the start of a fresh sequence.
 * - A focus change cancels the pending sequence without firing anything.
 * - Contexts form a stack; the topmost context that has any candidate for
 *   the buffered keys shadows all lower contexts (e.g. an overlay shadows
 *   global sequences).
 * - Context-stack changes and atomic config hot-reloads re-adjudicate the
 *   already-typed keys, but re-adjudication never emits commands, so a
 *   command can never be triggered twice by re-evaluation.
 * - Pause drops key/tick events; resume preserves the pending sequence, its
 *   original deadline, and its generation.
 */

export interface Binding {
  /** Ordered keys of the sequence, e.g. ["g", "d"]. */
  sequence: string[];
  /** Command identifier emitted when the sequence completes. */
  command: string;
}

export interface ShortcutConfig {
  /** Logical time units to wait when a short sequence prefixes a longer one. */
  timeoutMs: number;
  /** Bindings grouped by context name. */
  contexts: Record<string, Binding[]>;
}

interface Resolution {
  matched: boolean;
  /** Command of an exact match for the current buffer, if any. */
  exact: string | null;
  /** Whether any longer sequence has the buffer as a prefix. */
  hasLonger: boolean;
}

interface Pending {
  /** Exact-match command awaiting its deadline; null for a pure prefix. */
  command: string | null;
  /** Absolute logical time at which the pending command fires. */
  deadline: number;
  /** Config generation at which this pending state was adjudicated. */
  generation: number;
}

export interface DispatcherState {
  paused: boolean;
  generation: number;
  contextStack: string[];
  buffer: string[];
  pending: { command: string | null; deadline: number; generation: number } | null;
}

export type CommandSink = (command: string, time: number) => void;

export class ShortcutDispatcher {
  private config: ShortcutConfig;
  private contextStack: string[];
  private readonly emit: CommandSink;

  /** Keys typed towards the in-progress sequence. Empty iff no pending. */
  private buffer: string[] = [];
  private pending: Pending | null = null;
  /** Bumped on every atomic config hot-reload. */
  private generation = 0;
  private paused = false;

  /**
   * @param config Initial shortcut configuration.
   * @param contextStack Context names, bottom first; the last element is the
   *   topmost (most specific) context and shadows all lower ones.
   * @param emit Sink invoked synchronously whenever a command fires.
   */
  constructor(config: ShortcutConfig, contextStack: string[], emit: CommandSink) {
    this.config = config;
    this.contextStack = [...contextStack];
    this.emit = emit;
  }

  /** A key was pressed at the given logical time. */
  keyDown(key: string, time: number): void {
    if (this.paused) return;
    this.settleTimeout(time);
    this.buffer.push(key);
    if (this.resolve(this.buffer).matched) {
      this.applyResolution(time);
      return;
    }
    // Mismatch: fire the pending short command first, then re-match the
    // current key as the start of a fresh sequence.
    const pendingCommand = this.pending?.command ?? null;
    this.clearSequence();
    if (pendingCommand !== null) this.emit(pendingCommand, time);
    this.buffer.push(key);
    this.applyResolution(time);
  }

  /** Pure clock event: fires the pending command if its deadline has passed. */
  tick(time: number): void {
    if (this.paused) return;
    this.settleTimeout(time);
  }

  /** Focus changed: cancel the pending sequence without firing anything. */
  focusChanged(_time: number): void {
    this.clearSequence();
  }

  /**
   * Replace the context stack (e.g. an overlay opened or closed) and
   * re-adjudicate the already-typed keys. Never emits commands.
   */
  setContextStack(stack: string[], _time: number): void {
    this.contextStack = [...stack];
    this.readjudicate();
  }

  /**
   * Atomically hot-reload the whole configuration, bump the generation, and
   * re-adjudicate the already-typed keys. Never emits commands.
   */
  updateConfig(config: ShortcutConfig, _time: number): void {
    this.config = config;
    this.generation += 1;
    this.readjudicate();
  }

  /** Suspend dispatching: key and clock events are dropped while paused. */
  pause(): void {
    this.paused = true;
  }

  /**
   * Resume dispatching. The pending sequence, its original deadline, and its
   * generation are preserved exactly as they were when paused.
   */
  resume(): void {
    this.paused = false;
  }

  /** Snapshot of the internal state, for inspection and tests. */
  getState(): DispatcherState {
    return {
      paused: this.paused,
      generation: this.generation,
      contextStack: [...this.contextStack],
      buffer: [...this.buffer],
      pending: this.pending === null ? null : { ...this.pending },
    };
  }

  /** Fire the pending command if its deadline is at or before `time`. */
  private settleTimeout(time: number): void {
    if (this.pending !== null && this.pending.deadline <= time) {
      const command = this.pending.command;
      this.clearSequence();
      if (command !== null) this.emit(command, time);
    }
  }

  /**
   * Update pending state from the resolution of the current buffer.
   * Assumes the buffer is non-empty.
   */
  private applyResolution(time: number): void {
    const res = this.resolve(this.buffer);
    if (!res.matched) {
      this.clearSequence();
      return;
    }
    if (res.exact !== null && !res.hasLonger) {
      // Unambiguous exact match: fire immediately.
      this.emit(res.exact, time);
      this.clearSequence();
      return;
    }
    // Ambiguous (exact + longer) or pure prefix: wait for the deadline.
    this.pending = {
      command: res.exact,
      deadline: time + this.config.timeoutMs,
      generation: this.generation,
    };
  }

  /**
   * Re-adjudicate the already-typed keys after a context-stack or config
   * change. The original deadline is kept and nothing is ever emitted here,
   * so re-evaluation can never trigger (or re-trigger) a command.
   */
  private readjudicate(): void {
    if (this.pending === null) return;
    const res = this.resolve(this.buffer);
    if (!res.matched) {
      // The buffered keys no longer match anything: drop them silently.
      this.clearSequence();
      return;
    }
    this.pending.command = res.exact;
    this.pending.generation = this.generation;
  }

  private clearSequence(): void {
    this.buffer = [];
    this.pending = null;
  }

  /**
   * Resolve a key buffer against the context stack. Contexts are consulted
   * from the top of the stack downwards; the first context with any
   * candidate (exact or prefix) shadows all lower contexts.
   */
  private resolve(buffer: string[]): Resolution {
    const none: Resolution = { matched: false, exact: null, hasLonger: false };
    if (buffer.length === 0) return none;
    for (let i = this.contextStack.length - 1; i >= 0; i--) {
      const bindings = this.config.contexts[this.contextStack[i]];
      if (bindings === undefined) continue;
      let exact: string | null = null;
      let hasLonger = false;
      for (const binding of bindings) {
        const seq = binding.sequence;
        if (seq.length < buffer.length) continue;
        let prefix = true;
        for (let j = 0; j < buffer.length; j++) {
          if (seq[j] !== buffer[j]) {
            prefix = false;
            break;
          }
        }
        if (!prefix) continue;
        if (seq.length === buffer.length) exact = binding.command;
        else hasLonger = true;
      }
      if (exact !== null || hasLonger) return { matched: true, exact, hasLonger };
    }
    return none;
  }
}
