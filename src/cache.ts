import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import { digest, shortDigest } from "./digest.ts";

/**
 * File cache for validation outcomes. Every entry is bound to the digests of
 * BOTH the schema and the sample set it was computed from. A changed sample
 * (or schema) produces a different key, so stale "pass" results are never
 * reused; the stored digests are also re-verified on read to catch tampering
 * or hash-collision-level accidents.
 */

export interface CacheEnvelope<T> {
  schemaDigest: string;
  samplesDigest: string;
  createdAt: string;
  payload: T;
}

export class ResultCache<T> {
  private dir: string;

  constructor(dir: string) {
    this.dir = dir;
    mkdirSync(dir, { recursive: true });
  }

  private keyFor(schema: unknown, samples: unknown): { file: string; schemaDigest: string; samplesDigest: string } {
    const schemaDigest = digest(schema);
    const samplesDigest = digest(samples);
    return {
      file: join(this.dir, `${shortDigest(schema)}-${shortDigest(samples)}.json`),
      schemaDigest,
      samplesDigest,
    };
  }

  get(schema: unknown, samples: unknown): T | null {
    const { file, schemaDigest, samplesDigest } = this.keyFor(schema, samples);
    if (!existsSync(file)) return null;
    let envelope: CacheEnvelope<T>;
    try {
      envelope = JSON.parse(readFileSync(file, "utf8")) as CacheEnvelope<T>;
    } catch {
      return null;
    }
    if (envelope.schemaDigest !== schemaDigest || envelope.samplesDigest !== samplesDigest) {
      return null;
    }
    return envelope.payload;
  }

  set(schema: unknown, samples: unknown, payload: T): void {
    const { file, schemaDigest, samplesDigest } = this.keyFor(schema, samples);
    const envelope: CacheEnvelope<T> = {
      schemaDigest,
      samplesDigest,
      createdAt: new Date().toISOString(),
      payload,
    };
    writeFileSync(file, JSON.stringify(envelope, null, 2));
  }
}
