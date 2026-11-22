import { readFile, truncate, open } from "node:fs/promises";

import { deepFreeze, hashDomain, sha256 } from "./canonical.ts";
import { JOURNAL_DOMAIN, signPayload, verifyPayload } from "./keys.ts";
import type { AuthEvent, HostKeyPair, JournalRecord, JournalStore } from "./types.ts";

const JOURNAL_HASH_DOMAIN = "ddd-cross-device/journal-hash/v1";

export function createJournalRecord(
  seq: number,
  event: AuthEvent,
  prevHash: string | null,
  signer: HostKeyPair,
): JournalRecord {
  const body = { v: 1 as const, seq, event, prevHash };
  const hash = hashDomain(JOURNAL_HASH_DOMAIN, body);
  const signature = signPayload(signer.privateKey, JOURNAL_DOMAIN, { hash });
  return deepFreeze({ ...body, hash, signature });
}

export function verifyJournalRecord(
  record: JournalRecord,
  prevHash: string | null,
  expectedSeq: number,
  hostPublicKey: string,
): boolean {
  try {
    if (record.v !== 1 || record.seq !== expectedSeq || record.prevHash !== prevHash) return false;
    const body = { v: record.v, seq: record.seq, event: record.event, prevHash: record.prevHash };
    if (hashDomain(JOURNAL_HASH_DOMAIN, body) !== record.hash) return false;
    return verifyPayload(hostPublicKey, JOURNAL_DOMAIN, { hash: record.hash }, record.signature);
  } catch {
    return false;
  }
}

export class MemoryJournalStore implements JournalStore {
  #records: JournalRecord[];

  constructor(records: JournalRecord[] = []) {
    this.#records = records.map((record) => deepFreeze(structuredClone(record)));
  }

  async load(): Promise<JournalRecord[]> {
    return this.#records.map((record) => structuredClone(record));
  }

  async append(record: JournalRecord): Promise<void> {
    this.#records.push(deepFreeze(structuredClone(record)));
  }

  snapshot(): JournalRecord[] {
    return this.#records.map((record) => structuredClone(record));
  }

  get length(): number {
    return this.#records.length;
  }
}

/**
 * Crash-injecting store used by applications that want to exercise recovery.
 * The optional hook runs after the record is durable but before append resolves.
 */
export class FaultInjectingJournalStore implements JournalStore {
  readonly inner: JournalStore;
  #appendCount = 0;
  readonly faultAt: number;
  readonly faultMode: "before_append" | "after_append";

  constructor(
    inner: JournalStore,
    faultAt: number,
    faultMode: "before_append" | "after_append" = "after_append",
  ) {
    this.inner = inner;
    this.faultAt = faultAt;
    this.faultMode = faultMode;
  }

  get appendCount(): number {
    return this.#appendCount;
  }

  async load(): Promise<JournalRecord[]> {
    return this.inner.load();
  }

  async append(record: JournalRecord): Promise<void> {
    this.#appendCount += 1;
    if (this.faultMode === "before_append" && this.#appendCount === this.faultAt) {
      throw new Error(`Injected crash before append ${this.#appendCount}`);
    }
    await this.inner.append(record);
    if (this.faultMode === "after_append" && this.#appendCount === this.faultAt) {
      throw new Error(`Injected crash after append ${this.#appendCount}`);
    }
  }
}

/**
 * Append-only, one-record-per-line JSON journal. A torn final line is truncated
 * during load so recovery always sees a complete prefix of durable records.
 */
export class FileJournalStore implements JournalStore {
  readonly path: string;

  constructor(path: string) {
    this.path = path;
  }

  async load(): Promise<JournalRecord[]> {
    let data: Buffer;
    try {
      data = await readFile(this.path);
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === "ENOENT") return [];
      throw error;
    }
    if (data.length === 0) return [];

    let completeLength = 0;
    for (let index = data.length - 1; index >= 0; index -= 1) {
      if (data[index] === 0x0a) {
        completeLength = index + 1;
        break;
      }
    }
    if (completeLength !== data.length) {
      await truncate(this.path, completeLength);
      data = data.subarray(0, completeLength);
    }
    const text = data.toString("utf8");
    const lines = text.split("\n");
    lines.pop();
    const records: JournalRecord[] = [];
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index]!;
      if (line.length === 0) throw new Error(`Invalid blank journal line at ${index}`);
      let parsed: unknown;
      try {
        parsed = JSON.parse(line);
      } catch {
        throw new Error(`Corrupt journal record at line ${index}`);
      }
      records.push(deepFreeze(parsed as JournalRecord));
    }
    return records;
  }

  async append(record: JournalRecord): Promise<void> {
    const line = JSON.stringify(record) + "\n";
    const handle = await open(this.path, "a");
    try {
      let offset = 0;
      const buffer = Buffer.from(line, "utf8");
      while (offset < buffer.length) {
        const result = await handle.write(buffer, offset, buffer.length - offset);
        if (result.bytesWritten <= 0) throw new Error("Journal write made no progress");
        offset += result.bytesWritten;
      }
      await handle.sync();
    } finally {
      await handle.close();
    }
  }
}

export class AsyncMutex {
  #tail: Promise<void> = Promise.resolve();

  run<T>(task: () => Promise<T>): Promise<T> {
    const previous = this.#tail;
    let release!: () => void;
    this.#tail = new Promise<void>((resolve) => {
      release = resolve;
    });
    return previous.then(task).finally(() => release());
  }
}

export function journalDigest(records: JournalRecord[]): string {
  return sha256(records.map((record) => record.hash).join("\n"));
}
