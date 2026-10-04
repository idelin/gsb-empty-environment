export interface Fragment {
  messageId: string;
  totalLength: number;
  offset: number;
  data: Uint8Array;
  /**
   * Optional sender-side generation tag. When supplied, fragments whose tag
   * differs from the in-flight message's tag are rejected as stale, so late
   * fragments of a previous generation can never mix into a new message.
   */
  generation?: number;
}

export interface AssemblerOptions {
  /** Hard bound on bytes buffered across all in-flight messages. */
  maxBufferedBytes: number;
  /** After this much silence a message id may re-enter as a new generation. */
  generationTimeoutMs: number;
  /** Injectable clock (milliseconds). Defaults to Date.now. */
  now?: () => number;
}

export type AddResult =
  | { status: 'complete'; messageId: string; generation: number; data: Uint8Array }
  | { status: 'accepted'; messageId: string; generation: number }
  | { status: 'duplicate'; messageId: string; generation: number }
  | { status: 'conflict'; messageId: string; generation: number; reason: string }
  | { status: 'quarantined'; messageId: string }
  | { status: 'rejected'; messageId: string; reason: string };

interface MessageState {
  generation: number;
  externalGeneration: number | undefined;
  totalLength: number;
  buffer: Uint8Array;
  received: Uint8Array;
  receivedBytes: number;
  createdAt: number;
  quarantined: boolean;
}

export class FragmentAssembler {
  private readonly maxBufferedBytes: number;
  private readonly generationTimeoutMs: number;
  private readonly now: () => number;
  private readonly messages = new Map<string, MessageState>();
  private nextGeneration = 1;
  private bufferedBytes = 0;

  constructor(options: AssemblerOptions) {
    if (!Number.isFinite(options.maxBufferedBytes) || options.maxBufferedBytes < 0) {
      throw new Error('maxBufferedBytes must be a non-negative finite number');
    }
    if (!Number.isFinite(options.generationTimeoutMs) || options.generationTimeoutMs <= 0) {
      throw new Error('generationTimeoutMs must be a positive finite number');
    }
    this.maxBufferedBytes = options.maxBufferedBytes;
    this.generationTimeoutMs = options.generationTimeoutMs;
    this.now = options.now ?? (() => Date.now());
  }

  get usedBytes(): number {
    return this.bufferedBytes;
  }

  get inFlightCount(): number {
    return this.messages.size;
  }

  isQuarantined(messageId: string): boolean {
    const state = this.messages.get(messageId);
    return state !== undefined && state.quarantined;
  }

  addFragment(fragment: Fragment): AddResult {
    const { messageId, totalLength, offset, data } = fragment;

    if (!Number.isInteger(totalLength) || totalLength < 0) {
      return { status: 'rejected', messageId, reason: 'invalid totalLength' };
    }
    if (!Number.isInteger(offset) || offset < 0 || offset + data.length > totalLength) {
      return { status: 'rejected', messageId, reason: 'fragment out of bounds' };
    }
    if (totalLength === 0) {
      if (offset !== 0 || data.length !== 0) {
        return { status: 'rejected', messageId, reason: 'invalid empty-message fragment' };
      }
      return { status: 'complete', messageId, generation: 0, data: new Uint8Array(0) };
    }
    if (data.length === 0) {
      return { status: 'rejected', messageId, reason: 'empty data for non-empty message' };
    }

    const now = this.now();
    let state = this.messages.get(messageId);

    if (state !== undefined && now - state.createdAt >= this.generationTimeoutMs) {
      this.evict(messageId, state);
      state = undefined;
    }

    if (state !== undefined && state.quarantined) {
      return { status: 'quarantined', messageId };
    }

    if (state === undefined) {
      if (this.bufferedBytes + totalLength > this.maxBufferedBytes) {
        return { status: 'rejected', messageId, reason: 'memory limit exceeded' };
      }
      state = {
        generation: this.nextGeneration++,
        externalGeneration: fragment.generation,
        totalLength,
        buffer: new Uint8Array(totalLength),
        received: new Uint8Array(totalLength),
        receivedBytes: 0,
        createdAt: now,
        quarantined: false,
      };
      this.messages.set(messageId, state);
      this.bufferedBytes += totalLength;
    } else if (fragment.generation !== state.externalGeneration) {
      return { status: 'rejected', messageId, reason: 'stale generation' };
    } else if (totalLength !== state.totalLength) {
      return { status: 'rejected', messageId, reason: 'totalLength mismatch with in-flight generation' };
    }

    const end = offset + data.length;
    for (let index = offset; index < end; index++) {
      if (state.received[index] === 1 && state.buffer[index] !== data[index - offset]) {
        state.quarantined = true;
        this.bufferedBytes -= state.totalLength;
        state.buffer = new Uint8Array(0);
        state.received = new Uint8Array(0);
        return {
          status: 'conflict',
          messageId,
          generation: state.generation,
          reason: `byte mismatch at offset ${index}`,
        };
      }
    }

    let newBytes = 0;
    for (let index = offset; index < end; index++) {
      if (state.received[index] === 0) {
        state.received[index] = 1;
        state.buffer[index] = data[index - offset]!;
        newBytes++;
      }
    }
    state.receivedBytes += newBytes;

    if (state.receivedBytes === state.totalLength) {
      const assembled = state.buffer.slice();
      this.evict(messageId, state);
      return { status: 'complete', messageId, generation: state.generation, data: assembled };
    }

    if (newBytes === 0) {
      return { status: 'duplicate', messageId, generation: state.generation };
    }
    return { status: 'accepted', messageId, generation: state.generation };
  }

  /** Expire timed-out in-flight messages and quarantine tombstones. */
  sweep(now: number = this.now()): string[] {
    const expired: string[] = [];
    for (const [messageId, state] of this.messages) {
      if (now - state.createdAt >= this.generationTimeoutMs) {
        this.evict(messageId, state);
        expired.push(messageId);
      }
    }
    return expired;
  }

  private evict(messageId: string, state: MessageState): void {
    if (!state.quarantined) {
      this.bufferedBytes -= state.totalLength;
    }
    this.messages.delete(messageId);
  }
}
