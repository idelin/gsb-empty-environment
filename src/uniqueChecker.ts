/**
 * Streaming composite-key uniqueness checker.
 *
 * Records are consumed one at a time from any (async) iterable, so duplicates
 * are detected across arbitrary chunk boundaries: the key state lives in the
 * checker, not in the chunks.
 *
 * Memory bound: while the number of distinct keys stays below
 * `maxInMemoryKeys`, everything happens in memory. Once it is exceeded, all
 * key occurrences (key, recordIndex) are spilled to `spillPartitions`
 * hash-partitioned JSONL files (equal keys always land in the same
 * partition), and the remaining stream is appended to those files. A final
 * pass loads one partition at a time to reconstruct first/duplicate
 * positions. One partition must fit in memory; raise `spillPartitions` for
 * very skewed key distributions.
 */

import { createReadStream, createWriteStream, type WriteStream } from "node:fs";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import * as readline from "node:readline";

import { decodeCompositeKey, encodeCompositeKey } from "./keyEncoding.ts";
import { compileValidator, type Schema, type ValidationError, type Validator } from "./schema.ts";

export interface UniqueCheckerOptions {
  /**
   * Key fields. Either a plain top-level name ("id") or a JSON Pointer
   * ("/user/id", with ~0/~1 escaping) for nested fields.
   */
  fields: string[];
  /** Whether two null key components are considered equal. Default: true. */
  nullsEqual?: boolean;
  /** Maximum distinct keys held in memory before spilling. Default: Infinity. */
  maxInMemoryKeys?: number;
  /** Directory for spill files. Default: a fresh directory under os.tmpdir(). */
  spillDir?: string;
  /** Number of hash partitions used when spilling. Default: 256. */
  spillPartitions?: number;
  /** Keep spill files after the run (for inspection). Default: false. */
  keepSpillFiles?: boolean;
  /** Optional schema (supported subset only) each record is validated against. */
  schema?: Schema;
}

export interface DuplicateEntry {
  /** Decoded composite key values, in `fields` order. */
  key: unknown[];
  /** Stream position (0-based) of the first occurrence. */
  firstIndex: number;
  /** Stream position (0-based) of this duplicate occurrence. */
  duplicateIndex: number;
}

export interface UniqueReport {
  totalRecords: number;
  uniqueKeyCount: number;
  duplicateOccurrences: number;
  /** Sorted by duplicateIndex. */
  duplicates: DuplicateEntry[];
  spilled: boolean;
  /** Only set when keepSpillFiles is true and spilling happened. */
  spillDirectory?: string;
}

export class RecordValidationError extends Error {
  readonly recordIndex: number;
  readonly errors: ValidationError[];
  constructor(recordIndex: number, errors: ValidationError[]) {
    super(
      `record #${recordIndex} failed validation: ` +
        errors.map((e) => `${e.instancePath || "/"} violates ${e.rulePath} (${e.message})`).join("; "),
    );
    this.name = "RecordValidationError";
    this.recordIndex = recordIndex;
    this.errors = errors;
  }
}

interface InternalDuplicate {
  encoded: string;
  firstIndex: number;
  duplicateIndex: number;
}

function parseFieldPath(field: string): string[] {
  if (field.startsWith("/")) {
    return field
      .slice(1)
      .split("/")
      .map((segment) => segment.replace(/~1/g, "/").replace(/~0/g, "~"));
  }
  return [field];
}

function getPath(value: unknown, segments: string[]): unknown {
  let current = value;
  for (const segment of segments) {
    if (current === null || typeof current !== "object") return undefined;
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

function fnv1a(text: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

async function* toAsyncIterable<T>(source: Iterable<T> | AsyncIterable<T>): AsyncGenerator<T> {
  if (Symbol.asyncIterator in (source as AsyncIterable<T>)) {
    yield* source as AsyncIterable<T>;
  } else {
    yield* source as Iterable<T>;
  }
}

class SpillManager {
  readonly directory: string;
  readonly partitions: number;
  readonly #ownsDirectory: boolean;
  readonly #writers: WriteStream[] = [];

  private constructor(directory: string, partitions: number, ownsDirectory: boolean) {
    this.directory = directory;
    this.partitions = partitions;
    this.#ownsDirectory = ownsDirectory;
  }

  static async create(partitions: number, directory?: string): Promise<SpillManager> {
    let dir = directory;
    let owns = false;
    if (dir === undefined) {
      dir = await mkdtemp(join(tmpdir(), "composite-unique-spill-"));
      owns = true;
    } else {
      await mkdir(dir, { recursive: true });
    }
    const manager = new SpillManager(dir, partitions, owns);
    for (let p = 0; p < partitions; p++) {
      manager.#writers.push(createWriteStream(manager.#partitionPath(p), { flags: "w" }));
    }
    return manager;
  }

  #partitionPath(partition: number): string {
    return join(this.directory, `partition-${partition}.jsonl`);
  }

  async write(encodedKey: string, index: number): Promise<void> {
    const partition = fnv1a(encodedKey) % this.partitions;
    const writer = this.#writers[partition];
    if (!writer.write(JSON.stringify([encodedKey, index]) + "\n")) {
      await new Promise<void>((resolve, reject) => {
        writer.once("drain", resolve);
        writer.once("error", reject);
      });
    }
  }

  async close(): Promise<void> {
    await Promise.all(
      this.#writers.map(
        (writer) =>
          new Promise<void>((resolve, reject) => {
            writer.end((err: Error | null | undefined) => (err ? reject(err) : resolve()));
          }),
      ),
    );
  }

  async *readPartition(partition: number): AsyncGenerator<[string, number]> {
    const lines = readline.createInterface({
      input: createReadStream(this.#partitionPath(partition)),
      crlfDelay: Infinity,
    });
    for await (const line of lines) {
      if (line.length === 0) continue;
      const [encodedKey, index] = JSON.parse(line) as [string, number];
      yield [encodedKey, index];
    }
  }

  async cleanup(): Promise<void> {
    if (this.#ownsDirectory) {
      await rm(this.directory, { recursive: true, force: true });
    } else {
      await Promise.all(
        this.#writers.map((_, p) => rm(this.#partitionPath(p), { force: true })),
      );
    }
  }
}

export class CompositeUniqueChecker {
  readonly #paths: string[][];
  readonly #nullsEqual: boolean;
  readonly #maxInMemoryKeys: number;
  readonly #spillDir?: string;
  readonly #spillPartitions: number;
  readonly #keepSpillFiles: boolean;
  readonly #validator?: Validator;

  constructor(options: UniqueCheckerOptions) {
    if (!Array.isArray(options.fields) || options.fields.length === 0) {
      throw new Error("options.fields must be a non-empty array of field names / JSON pointers");
    }
    this.#paths = options.fields.map(parseFieldPath);
    this.#nullsEqual = options.nullsEqual ?? true;
    this.#maxInMemoryKeys = options.maxInMemoryKeys ?? Infinity;
    this.#spillDir = options.spillDir;
    this.#spillPartitions = options.spillPartitions ?? 256;
    this.#keepSpillFiles = options.keepSpillFiles ?? false;
    if (options.schema !== undefined) this.#validator = compileValidator(options.schema);
  }

  async check(records: Iterable<unknown> | AsyncIterable<unknown>): Promise<UniqueReport> {
    const firstSeen = new Map<string, number>();
    const duplicates: InternalDuplicate[] = [];
    let spill: SpillManager | null = null;
    let total = 0;

    for await (const record of toAsyncIterable(records)) {
      const index = total++;
      if (this.#validator !== undefined) {
        const errors = this.#validator(record);
        if (errors.length > 0) throw new RecordValidationError(index, errors);
      }
      const values = this.#paths.map((path) => getPath(record, path));
      const encoded = encodeCompositeKey(values, {
        nullsEqual: this.#nullsEqual,
        recordIndex: index,
      });

      if (spill !== null) {
        await spill.write(encoded, index);
        continue;
      }
      const first = firstSeen.get(encoded);
      if (first !== undefined) {
        duplicates.push({ encoded, firstIndex: first, duplicateIndex: index });
        continue;
      }
      firstSeen.set(encoded, index);
      if (firstSeen.size > this.#maxInMemoryKeys) {
        spill = await SpillManager.create(this.#spillPartitions, this.#spillDir);
        for (const [key, firstIndex] of firstSeen) await spill.write(key, firstIndex);
        for (const dup of duplicates) await spill.write(dup.encoded, dup.duplicateIndex);
        firstSeen.clear();
        duplicates.length = 0;
      }
    }

    let uniqueKeyCount: number;
    if (spill === null) {
      uniqueKeyCount = firstSeen.size;
    } else {
      await spill.close();
      uniqueKeyCount = 0;
      for (let p = 0; p < spill.partitions; p++) {
        const partitionFirst = new Map<string, number>();
        for await (const [encoded, index] of spill.readPartition(p)) {
          const first = partitionFirst.get(encoded);
          if (first === undefined) partitionFirst.set(encoded, index);
          else duplicates.push({ encoded, firstIndex: first, duplicateIndex: index });
        }
        uniqueKeyCount += partitionFirst.size;
      }
      if (!this.#keepSpillFiles) await spill.cleanup();
    }

    duplicates.sort((a, b) => a.duplicateIndex - b.duplicateIndex);
    return {
      totalRecords: total,
      uniqueKeyCount,
      duplicateOccurrences: duplicates.length,
      duplicates: duplicates.map((dup) => ({
        key: decodeCompositeKey(dup.encoded),
        firstIndex: dup.firstIndex,
        duplicateIndex: dup.duplicateIndex,
      })),
      spilled: spill !== null,
      spillDirectory: spill !== null && this.#keepSpillFiles ? spill.directory : undefined,
    };
  }
}

/** Flattens chunks into a record stream; duplicates across chunks are still detected. */
export async function* fromChunks<T>(chunks: Iterable<T[]> | AsyncIterable<T[]>): AsyncGenerator<T> {
  for await (const chunk of toAsyncIterable(chunks)) {
    yield* chunk;
  }
}
