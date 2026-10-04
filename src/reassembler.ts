/**
 * Bounded-memory message fragment reassembler.
 *
 * Fragments carry the total message length and their byte offset, and may be
 * retransmitted with arbitrary overlap. Overlapping bytes must be identical;
 * a mismatch quarantines the whole message until its state times out.
 *
 * Each message id has a generation. A newer generation is only admitted once
 * the previous state has completed or timed out, so late fragments from an
 * old generation can never mix into the new message.
 */

export interface Fragment {
  /** Message identifier. Fragments are grouped by (id, generation). */
  id: string;
  /**
   * Generation (epoch) of this message. Defaults to 0. A higher generation
   * with the same id is accepted only after the previous state completed or
   * timed out; lower generations are dropped as stale.
   */
  generation?: number;
  /** Total message length in bytes. Must be consistent across fragments. */
  totalLength: number;
  /** Byte offset of `data` within the message. */
  offset: number;
  /** Fragment payload. May overlap previously received ranges. */
  data: Uint8Array;
}

export type RejectReason =
  /** Malformed fragment (bad offset/length/generation). */
  | 'invalid'
  /** Overlapping bytes (or totalLength) disagreed; message now quarantined. */
  | 'conflict'
  /** Message is quarantined until its state times out. */
  | 'quarantined'
  /** Newer generation arrived before the previous state completed/timed out. */
  | 'generation-locked'
  /** Does not fit within the memory budget. */
  | 'memory';

export type AddResult =
  | { status: 'complete'; id: string; generation: number; data: Uint8Array }
  | { status: 'pending'; id: string; generation: number; received: number; totalLength: number }
  | { status: 'duplicate'; id: string; generation: number }
  | { status: 'stale'; id: string; generation: number }
  | { status: 'rejected'; id: string; generation: number; reason: RejectReason };

export interface ReassemblerOptions {
  /** Maximum total bytes buffered across all in-flight messages. */
  maxBytes: number;
  /**
   * Idle timeout in milliseconds. State untouched for longer than this is
   * discarded, after which the same id (any generation) may start afresh.
   */
  timeoutMs: number;
  /** Maximum number of tracked message states. Defaults to 1024. */
  maxMessages?: number;
  /** Clock in milliseconds. Injectable for tests. Defaults to Date.now. */
  now?: () => number;
}

export interface ReassemblerStats {
  messages: number;
  active: number;
  quarantined: number;
  completed: number;
  usedBytes: number;
  maxBytes: number;
}

type StateStatus = 'active' | 'quarantined' | 'completed';

interface MessageState {
  generation: number;
  totalLength: number;
  /** Null once the state no longer buffers bytes (quarantined/completed). */
  buffer: Uint8Array | null;
  /** Sorted, disjoint, merged [start, end) ranges of received bytes. */
  intervals: Array<[number, number]>;
  /** Number of distinct bytes covered by `intervals`. */
  received: number;
  lastActivity: number;
  status: StateStatus;
}

export class Reassembler {
  private readonly maxBytes: number;
  private readonly timeoutMs: number;
  private readonly maxMessages: number;
  private readonly clock: () => number;
  private readonly states = new Map<string, MessageState>();
  private usedBytes = 0;

  constructor(options: ReassemblerOptions) {
    if (!Number.isFinite(options.maxBytes) || options.maxBytes < 0) {
      throw new RangeError('maxBytes must be a non-negative number');
    }
    if (!Number.isFinite(options.timeoutMs) || options.timeoutMs < 0) {
      throw new RangeError('timeoutMs must be a non-negative number');
    }
    this.maxBytes = options.maxBytes;
    this.timeoutMs = options.timeoutMs;
    this.maxMessages = options.maxMessages ?? 1024;
    this.clock = options.now ?? Date.now;
  }

  addFragment(fragment: Fragment): AddResult {
    const { id, totalLength, offset, data } = fragment;
    const generation = fragment.generation ?? 0;
    const reject = (reason: RejectReason): AddResult => ({ status: 'rejected', id, generation, reason });

    if (!Number.isInteger(generation) || generation < 0) return reject('invalid');
    if (!Number.isInteger(totalLength) || totalLength < 0) return reject('invalid');
    if (!Number.isInteger(offset) || offset < 0) return reject('invalid');
    if (offset + data.length > totalLength) return reject('invalid');
    if (totalLength === 0 && (offset !== 0 || data.length !== 0)) return reject('invalid');

    const now = this.clock();
    let state = this.states.get(id);
    if (state && now - state.lastActivity > this.timeoutMs) {
      // Timed out: discard, so a new generation with the same id may enter.
      this.release(state);
      this.states.delete(id);
      state = undefined;
    }

    if (state) {
      if (generation < state.generation) {
        // Late fragment of an old generation: never mix into the new message.
        return { status: 'stale', id, generation };
      }
      if (generation > state.generation) {
        if (state.status !== 'completed') return reject('generation-locked');
        this.release(state);
        this.states.delete(id);
        state = undefined;
      }
    }

    if (state) {
      if (state.status === 'quarantined') return reject('quarantined');
      if (state.status === 'completed') return { status: 'duplicate', id, generation };
      if (totalLength !== state.totalLength) {
        this.quarantine(state, now);
        return reject('conflict');
      }
      if (data.length === 0) {
        state.lastActivity = now;
        return { status: 'pending', id, generation, received: state.received, totalLength };
      }
      if (!this.isConsistent(state, offset, data)) {
        this.quarantine(state, now);
        return reject('conflict');
      }
      this.write(state, offset, data);
      state.lastActivity = now;
      if (state.received === state.totalLength) return this.complete(id, state);
      return { status: 'pending', id, generation, received: state.received, totalLength };
    }

    // No live state: start a new message.
    if (totalLength === 0) {
      // Empty message completes immediately; keep a tombstone for dedupe.
      if (!this.evictForCapacity(0)) return reject('memory');
      this.states.set(id, {
        generation,
        totalLength: 0,
        buffer: null,
        intervals: [],
        received: 0,
        lastActivity: now,
        status: 'completed',
      });
      return { status: 'complete', id, generation, data: new Uint8Array(0) };
    }
    if (data.length === 0) {
      // Empty probe fragment: report progress without allocating state.
      return { status: 'pending', id, generation, received: 0, totalLength };
    }
    if (totalLength > this.maxBytes) return reject('memory');
    if (!this.evictForCapacity(totalLength)) return reject('memory');

    const fresh: MessageState = {
      generation,
      totalLength,
      buffer: new Uint8Array(totalLength),
      intervals: [],
      received: 0,
      lastActivity: now,
      status: 'active',
    };
    this.write(fresh, offset, data);
    this.usedBytes += totalLength;
    this.states.set(id, fresh);
    if (fresh.received === totalLength) return this.complete(id, fresh);
    return { status: 'pending', id, generation, received: fresh.received, totalLength };
  }

  /** Discard all expired states. Returns the number of states removed. */
  sweep(): number {
    const now = this.clock();
    let removed = 0;
    for (const [id, state] of this.states) {
      if (now - state.lastActivity > this.timeoutMs) {
        this.release(state);
        this.states.delete(id);
        removed++;
      }
    }
    return removed;
  }

  has(id: string): boolean {
    return this.states.has(id);
  }

  isQuarantined(id: string): boolean {
    return this.states.get(id)?.status === 'quarantined';
  }

  stats(): ReassemblerStats {
    let active = 0;
    let quarantined = 0;
    let completed = 0;
    for (const state of this.states.values()) {
      if (state.status === 'active') active++;
      else if (state.status === 'quarantined') quarantined++;
      else completed++;
    }
    return {
      messages: this.states.size,
      active,
      quarantined,
      completed,
      usedBytes: this.usedBytes,
      maxBytes: this.maxBytes,
    };
  }

  /** Check that `data` agrees with every already-received byte it overlaps. */
  private isConsistent(state: MessageState, offset: number, data: Uint8Array): boolean {
    const end = offset + data.length;
    const buffer = state.buffer!;
    for (const [a, b] of state.intervals) {
      if (a >= end) break;
      if (b <= offset) continue;
      const from = Math.max(a, offset);
      const to = Math.min(b, end);
      for (let i = from; i < to; i++) {
        if (buffer[i] !== data[i - offset]) return false;
      }
    }
    return true;
  }

  /** Copy bytes into the buffer and merge the covered range into intervals. */
  private write(state: MessageState, offset: number, data: Uint8Array): void {
    state.buffer!.set(data, offset);
    let start = offset;
    let end = offset + data.length;
    let added = data.length;
    const merged: Array<[number, number]> = [];
    let inserted = false;
    for (const [a, b] of state.intervals) {
      if (b < start) {
        merged.push([a, b]);
      } else if (a > end) {
        if (!inserted) {
          merged.push([start, end]);
          inserted = true;
        }
        merged.push([a, b]);
      } else {
        added -= Math.max(0, Math.min(b, end) - Math.max(a, start));
        start = Math.min(start, a);
        end = Math.max(end, b);
      }
    }
    if (!inserted) merged.push([start, end]);
    state.intervals = merged;
    state.received += added;
  }

  private complete(id: string, state: MessageState): AddResult {
    const data = state.buffer!;
    state.buffer = null;
    state.intervals = [];
    state.status = 'completed';
    state.lastActivity = this.clock();
    this.usedBytes -= state.totalLength;
    return { status: 'complete', id, generation: state.generation, data };
  }

  /** Conflicting bytes seen: isolate the whole message until it times out. */
  private quarantine(state: MessageState, now: number): void {
    if (state.buffer) {
      this.usedBytes -= state.totalLength;
      state.buffer = null;
    }
    state.intervals = [];
    state.received = 0;
    state.status = 'quarantined';
    state.lastActivity = now;
  }

  private release(state: MessageState): void {
    if (state.status === 'active' && state.buffer) {
      this.usedBytes -= state.totalLength;
      state.buffer = null;
    }
  }

  /**
   * Make room for `neededBytes` and one more state entry by evicting
   * least-recently-active states. Returns false if it still does not fit.
   */
  private evictForCapacity(neededBytes: number): boolean {
    if (this.usedBytes + neededBytes > this.maxBytes) {
      const victims = [...this.states.entries()]
        .filter(([, s]) => s.status === 'active')
        .sort((a, b) => a[1].lastActivity - b[1].lastActivity);
      for (const [victimId, victim] of victims) {
        if (this.usedBytes + neededBytes <= this.maxBytes) break;
        this.release(victim);
        this.states.delete(victimId);
      }
      if (this.usedBytes + neededBytes > this.maxBytes) return false;
    }
    while (this.states.size >= this.maxMessages) {
      let oldestId: string | undefined;
      let oldest = Infinity;
      for (const [entryId, s] of this.states) {
        if (s.lastActivity < oldest) {
          oldest = s.lastActivity;
          oldestId = entryId;
        }
      }
      if (oldestId === undefined) return false;
      this.release(this.states.get(oldestId)!);
      this.states.delete(oldestId);
    }
    return true;
  }
}
