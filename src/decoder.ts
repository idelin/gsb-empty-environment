import { canonicalJson, concatBytes } from "./canonical.ts";
import { ErrorCode, PipelineError } from "./errors.ts";
import type { TransformStep } from "./manifest.ts";

export interface DecoderFeedResult {
  /** Bytes accepted from the supplied chunk. Zero means retry after reading. */
  readonly consumed: number;
  /** Bytes appended to the decoder's output queue by this call. */
  readonly produced: number;
  readonly pendingOutput: number;
  readonly pendingInput: number;
  readonly finished: boolean;
}

export interface ByteDecoder {
  readonly finished: boolean;
  readonly done: boolean;
  readonly bufferedOutput: number;
  readonly pendingInput: number;
  feed(input: Uint8Array): DecoderFeedResult;
  read(maxBytes?: number): Uint8Array;
  finish(): void;
  cancel(reason?: unknown): void;
}

class OutputQueue {
  #chunks: Uint8Array[] = [];
  #length = 0;

  get length(): number {
    return this.#length;
  }

  push(chunk: Uint8Array): void {
    if (chunk.byteLength === 0) {
      return;
    }
    this.#chunks.push(chunk);
    this.#length += chunk.byteLength;
  }

  read(maxBytes = Number.POSITIVE_INFINITY): Uint8Array {
    if (this.#length === 0 || maxBytes <= 0) {
      return new Uint8Array(0);
    }
    const wanted = Math.min(this.#length, Math.max(0, Math.floor(maxBytes)));
    if (wanted === this.#length) {
      const output =
        this.#chunks.length === 1
          ? this.#chunks[0]
          : concatBytes(this.#chunks);
      this.#chunks = [];
      this.#length = 0;
      return output;
    }
    const pieces: Uint8Array[] = [];
    let remaining = wanted;
    while (remaining > 0 && this.#chunks.length > 0) {
      const chunk = this.#chunks[0];
      if (chunk.byteLength <= remaining) {
        pieces.push(chunk);
        this.#chunks.shift();
        remaining -= chunk.byteLength;
        this.#length -= chunk.byteLength;
      } else {
        pieces.push(chunk.slice(0, remaining));
        this.#chunks[0] = chunk.slice(remaining);
        this.#length -= remaining;
        remaining = 0;
      }
    }
    return concatBytes(pieces);
  }

  clear(): void {
    this.#chunks = [];
    this.#length = 0;
  }
}

export interface DeterministicDecoderOptions {
  /**
   * Stop accepting new input once this many output bytes are queued.  The
   * caller must read pending output before retrying the same chunk.
   */
  readonly maxOutputBuffer?: number;
  readonly maxInputBuffer?: number;
  /** Require a 0x00 end marker before finish(). */
  readonly requireEndMarker?: boolean;
}

/**
 * Small deterministic block codec:
 *
 *   0x01 u16be length  literal bytes
 *   0x02 u16be length  repeated byte
 *   0x00               end marker (optional unless requireEndMarker is set)
 *
 * A zero-length literal/repeat is legal.  An incomplete unit consumes no
 * output-producing work but may be retained internally; if the output queue is
 * full feed() returns consumed=0.  That is the zero-consumption/pending-output
 * case the pipeline loop is required to handle.
 */
export class DeterministicDecoder implements ByteDecoder {
  readonly maxOutputBuffer: number;
  readonly maxInputBuffer: number;
  readonly requireEndMarker: boolean;

  #input = new Uint8Array(0);
  #output = new OutputQueue();
  #seenEnd = false;
  #finished = false;
  #cancelled = false;

  constructor(options: DeterministicDecoderOptions = {}) {
    this.maxOutputBuffer = options.maxOutputBuffer ?? Number.POSITIVE_INFINITY;
    this.maxInputBuffer = options.maxInputBuffer ?? 4 * 1024 * 1024;
    this.requireEndMarker = options.requireEndMarker ?? false;
    if (
      !(this.maxOutputBuffer > 0) ||
      !(this.maxInputBuffer > 0)
    ) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "decoder buffer limits must be positive",
        {
          maxOutputBuffer: this.maxOutputBuffer,
          maxInputBuffer: this.maxInputBuffer,
        },
      );
    }
  }

  get finished(): boolean {
    return this.#finished;
  }

  get done(): boolean {
    return this.#finished && this.#output.length === 0;
  }

  get bufferedOutput(): number {
    return this.#output.length;
  }

  get pendingInput(): number {
    return this.#input.byteLength;
  }

  #process(force = false): number {
    let produced = 0;
    while (
      !this.#cancelled &&
      !this.#seenEnd &&
      (force || this.#output.length < this.maxOutputBuffer)
    ) {
      if (this.#input.byteLength === 0) {
        break;
      }
      const control = this.#input[0];
      if (control === 0x00) {
        this.#input = this.#input.subarray(1);
        this.#seenEnd = true;
        break;
      }
      if (control === 0x01) {
        if (this.#input.byteLength < 3) {
          break;
        }
        const length =
          (this.#input[1] << 8) | (this.#input[2] & 0xff);
        const unitLength = 3 + length;
        if (this.#input.byteLength < unitLength) {
          break;
        }
        if (length > 0) {
          this.#output.push(this.#input.slice(3, unitLength));
          produced += length;
        }
        this.#input = this.#input.subarray(unitLength);
        continue;
      }
      if (control === 0x02) {
        if (this.#input.byteLength < 4) {
          break;
        }
        const length =
          (this.#input[1] << 8) | (this.#input[2] & 0xff);
        const value = this.#input[3];
        if (length > 0) {
          this.#output.push(new Uint8Array(length).fill(value));
          produced += length;
        }
        this.#input = this.#input.subarray(4);
        continue;
      }
      throw new PipelineError(
        ErrorCode.INVALID_REPRESENTATION,
        `unknown deterministic decoder control byte 0x${control.toString(16)}`,
        { control },
      );
    }
    return produced;
  }

  feed(input: Uint8Array): DecoderFeedResult {
    if (this.#cancelled) {
      return {
        consumed: 0,
        produced: 0,
        pendingOutput: this.#output.length,
        pendingInput: this.#input.byteLength,
        finished: this.#finished,
      };
    }
    if (!(input instanceof Uint8Array)) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "decoder feed requires a Uint8Array",
        { valueType: typeof input },
      );
    }
    if (this.#finished) {
      if (input.byteLength === 0) {
        return {
          consumed: 0,
          produced: 0,
          pendingOutput: this.#output.length,
          pendingInput: this.#input.byteLength,
          finished: true,
        };
      }
      throw new PipelineError(
        ErrorCode.TRAILING_BYTES,
        "decoder received bytes after it finished",
        { bytes: input.byteLength },
      );
    }
    if (this.#seenEnd) {
      if (input.byteLength > 0) {
        throw new PipelineError(
          ErrorCode.TRAILING_BYTES,
          "decoder received bytes after the end marker",
          { bytes: input.byteLength },
        );
      }
      return {
        consumed: 0,
        produced: 0,
        pendingOutput: this.#output.length,
        pendingInput: this.#input.byteLength,
        finished: false,
      };
    }
    if (this.#output.length >= this.maxOutputBuffer) {
      return {
        consumed: 0,
        produced: 0,
        pendingOutput: this.#output.length,
        pendingInput: this.#input.byteLength,
        finished: false,
      };
    }
    this.#process();
    if (this.#input.byteLength + input.byteLength > this.maxInputBuffer) {
      throw new PipelineError(
        ErrorCode.DECODER_BACKPRESSURE,
        "decoder input buffer limit exceeded",
        {
          pendingInput: this.#input.byteLength,
          incoming: input.byteLength,
          maxInputBuffer: this.maxInputBuffer,
        },
      );
    }

    const before = this.#output.length;
    this.#input = concatBytes([this.#input, input]);
    this.#process();
    return {
      consumed: input.byteLength,
      produced: this.#output.length - before,
      pendingOutput: this.#output.length,
      pendingInput: this.#input.byteLength,
      finished: this.#seenEnd,
    };
  }

  read(maxBytes = Number.POSITIVE_INFINITY): Uint8Array {
    return this.#output.read(maxBytes);
  }

  finish(): void {
    if (this.#cancelled || this.#finished) {
      return;
    }
    this.#process(true);
    if (this.#input.byteLength > 0) {
      throw new PipelineError(
        ErrorCode.INCOMPLETE_DECODE,
        "decoder finished with a partial block",
        { pendingInput: this.#input.byteLength },
      );
    }
    if (this.requireEndMarker && !this.#seenEnd) {
      throw new PipelineError(
        ErrorCode.INCOMPLETE_DECODE,
        "decoder is missing its end marker",
        {},
      );
    }
    this.#finished = true;
  }

  cancel(_reason?: unknown): void {
    this.#cancelled = true;
    this.#finished = true;
    this.#input = new Uint8Array(0);
    this.#output.clear();
  }
}

export class IdentityDecoder implements ByteDecoder {
  readonly maxOutputBuffer: number;
  #output = new OutputQueue();
  #finished = false;
  #cancelled = false;

  constructor(options: { maxOutputBuffer?: number } = {}) {
    this.maxOutputBuffer = options.maxOutputBuffer ?? Number.POSITIVE_INFINITY;
  }

  get finished(): boolean {
    return this.#finished;
  }

  get done(): boolean {
    return this.#finished && this.#output.length === 0;
  }

  get bufferedOutput(): number {
    return this.#output.length;
  }

  get pendingInput(): number {
    return 0;
  }

  feed(input: Uint8Array): DecoderFeedResult {
    if (this.#cancelled) {
      return {
        consumed: 0,
        produced: 0,
        pendingOutput: this.#output.length,
        pendingInput: 0,
        finished: this.#finished,
      };
    }
    if (this.#finished && input.byteLength > 0) {
      throw new PipelineError(
        ErrorCode.TRAILING_BYTES,
        "identity decoder received bytes after it finished",
        { bytes: input.byteLength },
      );
    }
    if (this.#output.length >= this.maxOutputBuffer && input.byteLength > 0) {
      return {
        consumed: 0,
        produced: 0,
        pendingOutput: this.#output.length,
        pendingInput: 0,
        finished: this.#finished,
      };
    }
    this.#output.push(input.slice());
    return {
      consumed: input.byteLength,
      produced: input.byteLength,
      pendingOutput: this.#output.length,
      pendingInput: 0,
      finished: this.#finished,
    };
  }

  read(maxBytes = Number.POSITIVE_INFINITY): Uint8Array {
    return this.#output.read(maxBytes);
  }

  finish(): void {
    this.#finished = true;
  }

  cancel(_reason?: unknown): void {
    this.#cancelled = true;
    this.#finished = true;
    this.#output.clear();
  }
}

/**
 * Feeds the output of one decoder into the next.  This makes transform-chain
 * identity explicit and keeps the streaming contract uniform.
 */
export class ChainedDecoder implements ByteDecoder {
  readonly stages: readonly ByteDecoder[];
  #downstreamPending: Uint8Array[];

  constructor(stages: readonly ByteDecoder[]) {
    if (stages.length === 0) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "a decoder chain needs at least one stage",
      );
    }
    this.stages = stages;
    this.#downstreamPending = Array.from(
      { length: Math.max(0, stages.length - 1) },
      () => new Uint8Array(0),
    );
  }

  get finished(): boolean {
    return this.stages[this.stages.length - 1].finished;
  }

  get done(): boolean {
    return this.stages[this.stages.length - 1].done;
  }

  get bufferedOutput(): number {
    return this.stages[this.stages.length - 1].bufferedOutput;
  }

  get pendingInput(): number {
    return this.stages.reduce((total, stage) => total + stage.pendingInput, 0);
  }

  #pumpBoundary(index: number): void {
    const stage = this.stages[index];
    const next = this.stages[index + 1];
    let pending = this.#downstreamPending[index];
    while (pending.byteLength > 0) {
      const result = next.feed(pending);
      if (result.consumed === 0) {
        this.#downstreamPending[index] = pending;
        return;
      }
      pending = pending.subarray(result.consumed);
    }
    this.#downstreamPending[index] = new Uint8Array(0);
    while (stage.bufferedOutput > 0) {
      const chunk = stage.read();
      if (chunk.byteLength === 0) {
        break;
      }
      let offset = 0;
      while (offset < chunk.byteLength) {
        const result = next.feed(chunk.subarray(offset));
        if (result.consumed === 0) {
          this.#downstreamPending[index] = chunk.slice(offset);
          return;
        }
        offset += result.consumed;
      }
    }
  }

  #pumpDownstream(): void {
    for (let index = 0; index < this.stages.length - 1; index += 1) {
      this.#pumpBoundary(index);
    }
  }

  feed(input: Uint8Array): DecoderFeedResult {
    const before = this.bufferedOutput;
    const result = this.stages[0].feed(input);
    this.#pumpDownstream();
    return {
      ...result,
      produced: this.bufferedOutput - before,
      pendingOutput: this.bufferedOutput,
      pendingInput: this.pendingInput,
    };
  }

  read(maxBytes = Number.POSITIVE_INFINITY): Uint8Array {
    const output = this.stages[this.stages.length - 1].read(maxBytes);
    this.#pumpDownstream();
    return output;
  }

  finish(): void {
    for (let index = 0; index < this.stages.length; index += 1) {
      const stage = this.stages[index];
      if (index < this.stages.length - 1) {
        this.#pumpBoundary(index);
        if (this.#downstreamPending[index].byteLength > 0) {
          throw new PipelineError(
            ErrorCode.DECODER_BACKPRESSURE,
            "decoder chain cannot finish while downstream output is pending",
            { stage: index, pending: this.#downstreamPending[index].byteLength },
          );
        }
      }
      stage.finish();
      this.#pumpDownstream();
    }
  }

  cancel(reason?: unknown): void {
    for (const stage of this.stages) {
      stage.cancel(reason);
    }
  }
}

function decoderId(step: TransformStep): string {
  return step.id.trim().toLowerCase();
}

export type DecoderChainInput =
  | readonly TransformStep[]
  | TransformStep
  | string;

function asTransformSteps(chain: DecoderChainInput): readonly TransformStep[] {
  if (Array.isArray(chain)) {
    return chain.map((step) =>
      typeof step === "string" ? { id: step, version: 1 } : step,
    );
  }
  if (typeof chain === "string") {
    return [{ id: chain, version: 1 }];
  }
  return [chain];
}

export function createDecoder(
  chainInput: DecoderChainInput,
  options: DeterministicDecoderOptions = {},
): ByteDecoder {
  const chain = asTransformSteps(chainInput);
  if (chain.length === 0) {
    return new IdentityDecoder({ maxOutputBuffer: options.maxOutputBuffer });
  }
  const stages = chain.map((step) => {
    const id = decoderId(step);
    if (
      id === "identity" ||
      id === "none" ||
      id === "raw" ||
      id === "passthrough"
    ) {
      return new IdentityDecoder({ maxOutputBuffer: options.maxOutputBuffer });
    }
    if (
      id === "rle" ||
      id === "small" ||
      id === "deterministic" ||
      id === "toy" ||
      id === "gsb.rle" ||
      id === "gsb.small"
    ) {
      return new DeterministicDecoder(options);
    }
    throw new PipelineError(
      ErrorCode.UNSUPPORTED_ALGORITHM,
      `unsupported decoder transform: ${step.id}`,
      { transform: step },
    );
  });
  return stages.length === 1 ? stages[0] : new ChainedDecoder(stages);
}

export function decoderChainId(chain: DecoderChainInput): string {
  return canonicalJson(asTransformSteps(chain));
}

export interface EncodeOptions {
  readonly chunkSize?: number;
  readonly compressRuns?: boolean;
  readonly endMarker?: boolean;
}

/**
 * Encoder for the deterministic test decoder.  Runs of at least four equal
 * bytes become repeat blocks; everything else becomes literal blocks.
 */
export function encodeDeterministic(
  data: Uint8Array,
  options: EncodeOptions = {},
): Uint8Array {
  const chunkSize = options.chunkSize ?? 65535;
  const compressRuns = options.compressRuns ?? true;
  const endMarker = options.endMarker ?? true;
  if (!Number.isSafeInteger(chunkSize) || chunkSize <= 0 || chunkSize > 65535) {
    throw new PipelineError(
      ErrorCode.INVALID_ARGUMENT,
      "chunkSize must be between 1 and 65535",
      { chunkSize },
    );
  }
  const output: Uint8Array[] = [];
  let cursor = 0;
  let literalStart = 0;

  const flushLiteral = (end: number): void => {
    let start = literalStart;
    while (start < end) {
      const length = Math.min(chunkSize, end - start);
      const header = new Uint8Array(3);
      header[0] = 0x01;
      header[1] = (length >>> 8) & 0xff;
      header[2] = length & 0xff;
      output.push(header, data.slice(start, start + length));
      start += length;
    }
    literalStart = end;
  };

  while (cursor < data.byteLength) {
    if (compressRuns) {
      let runEnd = cursor + 1;
      while (
        runEnd < data.byteLength &&
        data[runEnd] === data[cursor] &&
        runEnd - cursor < 65535
      ) {
        runEnd += 1;
      }
      const runLength = runEnd - cursor;
      if (runLength >= 4) {
        flushLiteral(cursor);
        const header = new Uint8Array(4);
        header[0] = 0x02;
        header[1] = (runLength >>> 8) & 0xff;
        header[2] = runLength & 0xff;
        header[3] = data[cursor];
        output.push(header);
        cursor = runEnd;
        literalStart = cursor;
        continue;
      }
    }
    cursor += 1;
  }
  flushLiteral(data.byteLength);
  if (endMarker) {
    output.push(new Uint8Array([0x00]));
  }
  return output.length === 0 ? new Uint8Array(0) : concatBytes(output);
}

export { DeterministicDecoder as SmallDecoder, DeterministicDecoder as RleDecoder };

export interface DriveDecoderOptions {
  readonly signal?: AbortSignal;
  readonly onOutput?: (chunk: Uint8Array) => void | Promise<void>;
  readonly maxChunkBytes?: number;
  readonly maxIterations?: number;
  readonly collect?: boolean;
}

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) {
    throw new PipelineError(
      ErrorCode.CANCELLED,
      "decoder drive was cancelled",
      { reason: signal.reason },
    );
  }
}

/**
 * Drives a decoder to completion while applying sink backpressure.  The loop
 * always drains pending output before retrying a zero-consumption feed, which
 * is the deadlock case called out by the spec.
 */
export async function driveDecoder(
  decoder: ByteDecoder,
  inputs: Iterable<Uint8Array> | AsyncIterable<Uint8Array>,
  options: DriveDecoderOptions = {},
): Promise<Uint8Array> {
  const maxChunkBytes = options.maxChunkBytes ?? 64 * 1024;
  const maxIterations = options.maxIterations ?? 10_000_000;
  const collect = options.collect ?? options.onOutput === undefined;
  const collected: Uint8Array[] = [];
  let iterations = 0;

  const drain = async (): Promise<number> => {
    let drained = 0;
    while (true) {
      throwIfAborted(options.signal);
      const chunk = decoder.read(maxChunkBytes);
      if (chunk.byteLength === 0) {
        break;
      }
      drained += chunk.byteLength;
      if (collect) {
        collected.push(chunk);
      }
      if (options.onOutput !== undefined) {
        await options.onOutput(chunk);
      }
    }
    return drained;
  };

  const consume = async (input: Uint8Array): Promise<void> => {
    let offset = 0;
    while (offset < input.byteLength) {
      throwIfAborted(options.signal);
      iterations += 1;
      if (iterations > maxIterations) {
        throw new PipelineError(
          ErrorCode.DECODER_BACKPRESSURE,
          "decoder made no progress before the iteration limit",
          { iterations, offset, remaining: input.byteLength - offset },
        );
      }
      const result = decoder.feed(input.subarray(offset));
      offset += result.consumed;
      const drained = await drain();
      if (result.consumed === 0) {
        if (drained > 0) {
          continue;
        }
        if (decoder.finished) {
          break;
        }
        throw new PipelineError(
          ErrorCode.DECODER_BACKPRESSURE,
          "decoder consumed zero bytes without pending output",
          {
            offset,
            remaining: input.byteLength - offset,
            pendingInput: decoder.pendingInput,
          },
        );
      }
    }
  };

  for await (const input of inputs) {
    throwIfAborted(options.signal);
    if (!(input instanceof Uint8Array)) {
      throw new PipelineError(
        ErrorCode.INVALID_ARGUMENT,
        "decoder input must be a Uint8Array",
        { valueType: typeof input },
      );
    }
    await consume(input);
  }

  throwIfAborted(options.signal);
  decoder.finish();
  await drain();
  return collect ? concatBytes(collected) : new Uint8Array(0);
}

export async function decodeAll(
  encoded: Uint8Array,
  decoderOrChain: ByteDecoder | DecoderChainInput,
  options: DriveDecoderOptions = {},
): Promise<Uint8Array> {
  const decoder =
    Array.isArray(decoderOrChain) ||
    typeof decoderOrChain === "string" ||
    (!("feed" in (decoderOrChain as object)))
    ? createDecoder(decoderOrChain)
    : (decoderOrChain as ByteDecoder);
  return driveDecoder(decoder, [encoded], options);
}

export function decodeAllSync(
  encoded: Uint8Array,
  decoderOrChain: ByteDecoder | DecoderChainInput,
): Uint8Array {
  const decoder =
    Array.isArray(decoderOrChain) ||
    typeof decoderOrChain === "string" ||
    !("feed" in (decoderOrChain as object))
      ? createDecoder(decoderOrChain)
      : (decoderOrChain as ByteDecoder);
  const chunks: Uint8Array[] = [];
  let offset = 0;
  let iterations = 0;
  while (offset < encoded.byteLength) {
    iterations += 1;
    if (iterations > 10_000_000) {
      throw new PipelineError(
        ErrorCode.DECODER_BACKPRESSURE,
        "decoder made no progress",
        { offset },
      );
    }
    const result = decoder.feed(encoded.subarray(offset));
    offset += result.consumed;
    let drained = 0;
    while (decoder.bufferedOutput > 0) {
      const chunk = decoder.read();
      if (chunk.byteLength === 0) {
        break;
      }
      chunks.push(chunk);
      drained += chunk.byteLength;
    }
    if (result.consumed === 0) {
      if (drained > 0) {
        continue;
      }
      if (decoder.finished) {
        break;
      }
      throw new PipelineError(
        ErrorCode.DECODER_BACKPRESSURE,
        "decoder consumed zero bytes without pending output",
        { offset, pendingInput: decoder.pendingInput },
      );
    }
  }
  decoder.finish();
  while (decoder.bufferedOutput > 0) {
    const chunk = decoder.read();
    if (chunk.byteLength === 0) {
      break;
    }
    chunks.push(chunk);
  }
  return concatBytes(chunks);
}
