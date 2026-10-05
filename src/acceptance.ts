import { createHash } from "node:crypto";

export function sha256Hex(data: Buffer | string): string {
  return createHash("sha256").update(data).digest("hex");
}

export interface ChunkDescriptor {
  id: string;
  sha256: string;
  size: number;
}

export interface ArtifactManifest {
  runId: string;
  inputDigest: string;
  toolVersion: string;
  attempt: number;
  artifactDigest: string;
  chunks: ChunkDescriptor[];
}

export function manifestDigest(manifest: ArtifactManifest): string {
  return sha256Hex(
    JSON.stringify({
      runId: manifest.runId,
      inputDigest: manifest.inputDigest,
      toolVersion: manifest.toolVersion,
      attempt: manifest.attempt,
      artifactDigest: manifest.artifactDigest,
      chunks: manifest.chunks,
    }),
  );
}

export type Phase = "FETCHING" | "VERIFYING" | "CLEANING_UP" | "ACCEPTED" | "FAILED";

export type RejectionReason =
  | "INPUT_DIGEST_MISMATCH"
  | "TOOL_VERSION_MISMATCH"
  | "STALE_ATTEMPT"
  | "ATTEMPT_ALREADY_ACCEPTED"
  | "MANIFEST_CONFLICT";

export type ManifestResult =
  | { status: "TRACKING"; attempt: number; missing: string[] }
  | { status: "DUPLICATE"; attempt: number }
  | { status: "REJECTED"; reason: RejectionReason };

export type ChunkResult =
  | { status: "STORED"; attempt: number; chunkId: string; remaining: string[] }
  | { status: "CHECKSUM_MISMATCH"; attempt: number; chunkId: string }
  | { status: "STALE_ATTEMPT"; attempt: number }
  | { status: "UNEXPECTED_CHUNK"; attempt: number; chunkId: string }
  | { status: "NOT_FETCHING"; attempt: number; phase: Phase };

export interface AcceptedArtifact {
  attempt: number;
  manifest: ArtifactManifest;
  artifact: Buffer;
}

export interface Transition {
  attempt: number;
  from: Phase | "NONE";
  to: Phase;
  reason: string;
  at: number;
}

export interface StagingStore {
  put(attempt: number, chunkId: string, data: Buffer): void;
  get(attempt: number, chunkId: string): Buffer | undefined;
  purgeChunk(attempt: number, chunkId: string): boolean;
  purgeAttempt(attempt: number): string[];
  keys(): string[];
}

export class InMemoryStagingStore implements StagingStore {
  private readonly data = new Map<string, Buffer>();

  private key(attempt: number, chunkId: string): string {
    return `${attempt}/${chunkId}`;
  }

  put(attempt: number, chunkId: string, data: Buffer): void {
    this.data.set(this.key(attempt, chunkId), data);
  }

  get(attempt: number, chunkId: string): Buffer | undefined {
    return this.data.get(this.key(attempt, chunkId));
  }

  purgeChunk(attempt: number, chunkId: string): boolean {
    return this.data.delete(this.key(attempt, chunkId));
  }

  purgeAttempt(attempt: number): string[] {
    const purged: string[] = [];
    for (const key of this.data.keys()) {
      if (key.startsWith(`${attempt}/`)) {
        this.data.delete(key);
        purged.push(key);
      }
    }
    return purged;
  }

  keys(): string[] {
    return [...this.data.keys()].sort();
  }
}

export interface AcceptanceOptions {
  inputDigest: string;
  toolVersion: string;
  maxVerifyRetries?: number;
  store?: StagingStore;
  now?: () => number;
}

interface AttemptContext {
  manifest: ArtifactManifest;
  manifestDigest: string;
  phase: Phase;
  received: Set<string>;
  verifyRetries: number;
  chunkChecksumFailures: number;
}

export interface AcceptanceSnapshot {
  phase: Phase | "IDLE";
  currentAttempt: number | null;
  acceptedAttempt: number;
  missing: string[];
  verifyRetries: number;
  chunkChecksumFailures: number;
  stagedKeys: string[];
}

export class AcceptanceController {
  private readonly expectedInputDigest: string;
  private readonly expectedToolVersion: string;
  private readonly maxVerifyRetries: number;
  private readonly store: StagingStore;
  private readonly now: () => number;

  private current: AttemptContext | null = null;
  private acceptedAttempt = -1;
  private acceptedRecord: AcceptedArtifact | null = null;

  readonly transitions: Transition[] = [];

  constructor(options: AcceptanceOptions) {
    this.expectedInputDigest = options.inputDigest;
    this.expectedToolVersion = options.toolVersion;
    this.maxVerifyRetries = options.maxVerifyRetries ?? 2;
    this.store = options.store ?? new InMemoryStagingStore();
    this.now = options.now ?? Date.now;
  }

  submitManifest(manifest: ArtifactManifest): ManifestResult {
    if (manifest.inputDigest !== this.expectedInputDigest) {
      return { status: "REJECTED", reason: "INPUT_DIGEST_MISMATCH" };
    }
    if (manifest.toolVersion !== this.expectedToolVersion) {
      return { status: "REJECTED", reason: "TOOL_VERSION_MISMATCH" };
    }
    if (manifest.attempt < this.acceptedAttempt) {
      return { status: "REJECTED", reason: "STALE_ATTEMPT" };
    }
    if (manifest.attempt === this.acceptedAttempt && this.acceptedRecord !== null) {
      return { status: "REJECTED", reason: "ATTEMPT_ALREADY_ACCEPTED" };
    }

    const digest = manifestDigest(manifest);

    if (this.current !== null && manifest.attempt === this.current.manifest.attempt) {
      if (digest === this.current.manifestDigest) {
        return { status: "DUPLICATE", attempt: manifest.attempt };
      }
      return { status: "REJECTED", reason: "MANIFEST_CONFLICT" };
    }
    if (this.current !== null && manifest.attempt < this.current.manifest.attempt) {
      return { status: "REJECTED", reason: "STALE_ATTEMPT" };
    }

    if (this.current !== null) {
      const superseded = this.current;
      this.transition(superseded, "CLEANING_UP", `superseded by attempt ${manifest.attempt}`);
      this.store.purgeAttempt(superseded.manifest.attempt);
    }

    this.current = {
      manifest,
      manifestDigest: digest,
      phase: "FETCHING",
      received: new Set<string>(),
      verifyRetries: 0,
      chunkChecksumFailures: 0,
    };
    this.transitions.push({
      attempt: manifest.attempt,
      from: "NONE",
      to: "FETCHING",
      reason: "manifest accepted for tracking",
      at: this.now(),
    });
    return { status: "TRACKING", attempt: manifest.attempt, missing: this.missingChunks() };
  }

  deliverChunk(attempt: number, chunkId: string, data: Buffer): ChunkResult {
    const ctx = this.current;
    if (ctx === null || attempt !== ctx.manifest.attempt) {
      return { status: "STALE_ATTEMPT", attempt };
    }
    if (ctx.phase !== "FETCHING") {
      return { status: "NOT_FETCHING", attempt, phase: ctx.phase };
    }
    const descriptor = ctx.manifest.chunks.find((chunk) => chunk.id === chunkId);
    if (descriptor === undefined) {
      return { status: "UNEXPECTED_CHUNK", attempt, chunkId };
    }
    if (data.length !== descriptor.size || sha256Hex(data) !== descriptor.sha256) {
      this.store.purgeChunk(attempt, chunkId);
      ctx.received.delete(chunkId);
      ctx.chunkChecksumFailures += 1;
      return { status: "CHECKSUM_MISMATCH", attempt, chunkId };
    }
    this.store.put(attempt, chunkId, data);
    ctx.received.add(chunkId);
    if (this.missingChunks().length === 0) {
      this.verify(ctx);
    }
    return { status: "STORED", attempt, chunkId, remaining: this.missingChunks() };
  }

  fetchMissing(fetcher: (chunkId: string) => Buffer): ChunkResult[] {
    const results: ChunkResult[] = [];
    const ctx = this.current;
    if (ctx === null) {
      return results;
    }
    for (const chunkId of this.missingChunks()) {
      results.push(this.deliverChunk(ctx.manifest.attempt, chunkId, fetcher(chunkId)));
      if (ctx.phase !== "FETCHING") {
        break;
      }
    }
    return results;
  }

  missingChunks(): string[] {
    const ctx = this.current;
    if (ctx === null || ctx.phase === "ACCEPTED" || ctx.phase === "FAILED") {
      return [];
    }
    return ctx.manifest.chunks
      .map((chunk) => chunk.id)
      .filter((chunkId) => !ctx.received.has(chunkId));
  }

  accepted(): AcceptedArtifact | null {
    return this.acceptedRecord;
  }

  snapshot(): AcceptanceSnapshot {
    return {
      phase: this.current === null ? "IDLE" : this.current.phase,
      currentAttempt: this.current === null ? null : this.current.manifest.attempt,
      acceptedAttempt: this.acceptedAttempt,
      missing: this.missingChunks(),
      verifyRetries: this.current === null ? 0 : this.current.verifyRetries,
      chunkChecksumFailures: this.current === null ? 0 : this.current.chunkChecksumFailures,
      stagedKeys: this.store.keys(),
    };
  }

  private verify(ctx: AttemptContext): void {
    const attempt = ctx.manifest.attempt;
    this.transition(ctx, "VERIFYING", "all chunks staged");
    const parts = ctx.manifest.chunks.map((chunk) => {
      const data = this.store.get(attempt, chunk.id);
      if (data === undefined) {
        throw new Error(`chunk ${chunk.id} missing from staging during verify`);
      }
      return data;
    });
    const assembled = Buffer.concat(parts);
    if (sha256Hex(assembled) === ctx.manifest.artifactDigest) {
      this.transition(ctx, "ACCEPTED", "artifact digest verified");
      this.acceptedAttempt = attempt;
      this.acceptedRecord = { attempt, manifest: ctx.manifest, artifact: assembled };
      return;
    }
    this.transition(ctx, "CLEANING_UP", "artifact digest mismatch; purging staged chunks");
    this.store.purgeAttempt(attempt);
    ctx.received.clear();
    ctx.verifyRetries += 1;
    if (ctx.verifyRetries > this.maxVerifyRetries) {
      this.transition(ctx, "FAILED", `verify retries exhausted (${this.maxVerifyRetries})`);
    } else {
      this.transition(ctx, "FETCHING", `verify retry ${ctx.verifyRetries}/${this.maxVerifyRetries}`);
    }
  }

  private transition(ctx: AttemptContext, to: Phase, reason: string): void {
    this.transitions.push({
      attempt: ctx.manifest.attempt,
      from: ctx.phase,
      to,
      reason,
      at: this.now(),
    });
    ctx.phase = to;
  }
}
