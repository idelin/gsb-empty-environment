import {
  closeSync,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  readFileSync,
  renameSync,
  rmSync,
  truncateSync,
  writeFileSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { tmpdir } from "node:os";
import { MigrationAcceptanceEngine, type EngineSnapshot } from "./engine.ts";
import type {
  ClosedDeviceManifest,
  EvaluateInput,
  Lease,
  MigrationEvent,
  MigrationPolicy,
  ReleaseDecision,
  SignatureVerifier,
} from "./types.ts";

export type PersistenceBoundary =
  | "before-append"
  | "after-append"
  | "after-journal-append"
  | "before-checkpoint"
  | "before-snapshot"
  | "after-snapshot-write"
  | "before-journal-truncate"
  | "after-journal-truncate"
  | "after-checkpoint";

export class SimulatedCrashError extends Error {
  boundary: PersistenceBoundary;

  constructor(boundary: PersistenceBoundary) {
    super(`simulated crash at persistence boundary: ${boundary}`);
    this.name = "SimulatedCrashError";
    this.boundary = boundary;
  }
}

export interface DurableStoreOptions {
  dir?: string;
  directory?: string;
  path?: string;
  policy?: MigrationPolicy;
  verifier?: SignatureVerifier;
  requireSignatures?: boolean;
  closedDevices?: ClosedDeviceManifest;
  leases?: Lease[];
  events?: MigrationEvent[];
  checkpointEvery?: number;
  crashAt?: PersistenceBoundary | PersistenceBoundary[];
  crashBoundary?: PersistenceBoundary | PersistenceBoundary[];
  failAt?: PersistenceBoundary | PersistenceBoundary[];
  autoLoad?: boolean;
}

export interface LoadResult {
  events: MigrationEvent[];
  leases: Lease[];
  recoveredPartialJournalLine: boolean;
  snapshotFound: boolean;
  journalLines: number;
}

function normalizeBoundaries(
  value: PersistenceBoundary | PersistenceBoundary[] | undefined,
): Set<PersistenceBoundary> {
  if (!value) return new Set();
  const aliases: Record<string, PersistenceBoundary> = {
    "before-journal": "before-append",
    "journal-appended": "after-journal-append",
    "after-journal": "after-journal-append",
    "snapshot-written": "after-snapshot-write",
    "journal-truncated": "after-journal-truncate",
    checkpoint: "after-checkpoint",
  };
  const values = Array.isArray(value) ? value : [value];
  return new Set(
    values.map((item) => aliases[String(item)] ?? (item as PersistenceBoundary)),
  );
}

function isSnapshot(value: unknown): value is EngineSnapshot {
  return (
    typeof value === "object" &&
    value !== null &&
    ((value as EngineSnapshot).format === "migration-acceptance/v1" ||
      (value as EngineSnapshot).format === undefined) &&
    Array.isArray((value as EngineSnapshot).events)
  );
}

/**
 * Append-only event journal plus atomically replaced snapshot.
 *
 * Durability boundaries intentionally mirror a real key agent:
 *   - before-append: the event was never durable
 *   - after-journal-append: the event is durable, snapshot is old
 *   - after-snapshot-write: both the snapshot and journal contain the event
 *   - after-journal-truncate: only the snapshot contains the event
 *
 * Reopening after any of those boundaries must produce the same event set,
 * modulo the event that was not yet durable.
 */
export class DurableMigrationStore {
  dir: string;
  journalPath: string;
  snapshotPath: string;
  checkpointEvery: number;
  engine: MigrationAcceptanceEngine;
  private crashes: Set<PersistenceBoundary>;
  private fired: Set<PersistenceBoundary>;
  private appendedSinceCheckpoint: number;

  constructor(options: DurableStoreOptions | string = {}) {
    if (typeof options === "string") options = { dir: options };
    if (
      options.policy === undefined &&
      ("digest" in options ||
        "ownerThreshold" in options ||
        "baselineTcb" in options ||
        "policyDigest" in options ||
        "recovery" in options)
    ) {
      options = {
        ...options,
        policy: options as unknown as MigrationPolicy,
      };
    }
    this.dir =
      options.dir ??
      options.directory ??
      options.path ??
      join(process.env.TMPDIR ?? tmpdir(), "migration-acceptance");
    this.journalPath = join(this.dir, "events.jsonl");
    this.snapshotPath = join(this.dir, "snapshot.json");
    this.checkpointEvery = Math.max(1, options.checkpointEvery ?? 1000);
    this.crashes = normalizeBoundaries(
      options.crashAt ?? options.crashBoundary ?? options.failAt,
    );
    this.fired = new Set();
    this.appendedSinceCheckpoint = 0;
    this.engine = new MigrationAcceptanceEngine({
      policy: options.policy,
      verifier: options.verifier,
      requireSignatures: options.requireSignatures,
      closedDevices: options.closedDevices,
      leases: options.leases,
    });
    mkdirSync(this.dir, { recursive: true });
    if (options.autoLoad ?? true) this.load();
    if (options.events && options.events.length > 0) {
      this.appendMany(options.events);
    }
  }

  setCrashBoundaries(
    boundaries: PersistenceBoundary | PersistenceBoundary[] | undefined,
  ): void {
    this.crashes = normalizeBoundaries(boundaries);
    this.fired.clear();
  }

  crashAt(boundary: PersistenceBoundary | PersistenceBoundary[] | undefined): void {
    this.setCrashBoundaries(boundary);
  }

  private boundary(boundary: PersistenceBoundary): void {
    if (!this.crashes.has(boundary) || this.fired.has(boundary)) return;
    this.fired.add(boundary);
    throw new SimulatedCrashError(boundary);
  }

  private fsyncFile(path: string): void {
    const fd = openSync(path, "r");
    try {
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
  }

  private fsyncDirectory(): void {
    try {
      const fd = openSync(this.dir, "r");
      try {
        fsyncSync(fd);
      } finally {
        closeSync(fd);
      }
    } catch {
      // Directory fsync is not available on every platform.
    }
  }

  private writeSnapshot(snapshot: EngineSnapshot): void {
    const tempPath = join(this.dir, `.snapshot-${process.pid}-${Date.now()}.tmp`);
    writeFileSync(tempPath, JSON.stringify(snapshot), "utf8");
    this.fsyncFile(tempPath);
    renameSync(tempPath, this.snapshotPath);
    this.fsyncDirectory();
  }

  append(event: MigrationEvent): boolean {
    return this.appendMany([event]) > 0;
  }

  appendMany(events: MigrationEvent[]): number {
    let appended = 0;
    for (const event of events) {
      this.boundary("before-append");
      const result = this.engine.ingest(event);
      if (result.added.length === 0) continue;
      writeFileSync(this.journalPath, `${JSON.stringify(event)}\n`, {
        encoding: "utf8",
        flag: "a",
      });
      this.fsyncFile(this.journalPath);
      appended += 1;
      this.appendedSinceCheckpoint += 1;
      this.boundary("after-append");
      this.boundary("after-journal-append");
      if (this.appendedSinceCheckpoint >= this.checkpointEvery) {
        this.checkpoint();
      }
    }
    return appended;
  }

  add(event: MigrationEvent): boolean {
    return this.append(event);
  }

  appendEvent(event: MigrationEvent): boolean {
    return this.append(event);
  }

  appendEvents(events: MigrationEvent[]): number {
    return this.appendMany(events);
  }

  addAll(events: MigrationEvent[]): number {
    return this.appendMany(events);
  }

  checkpoint(): EngineSnapshot {
    this.boundary("before-checkpoint");
    this.boundary("before-snapshot");
    const snapshot = this.engine.snapshot();
    this.writeSnapshot(snapshot);
    this.boundary("after-snapshot-write");
    this.boundary("before-journal-truncate");
    if (existsSync(this.journalPath)) {
      truncateSync(this.journalPath, 0);
      this.fsyncFile(this.journalPath);
    } else {
      writeFileSync(this.journalPath, "", "utf8");
    }
    this.appendedSinceCheckpoint = 0;
    this.boundary("after-journal-truncate");
    this.boundary("after-checkpoint");
    return snapshot;
  }

  load(): LoadResult {
    let snapshot: EngineSnapshot | undefined;
    let snapshotFound = false;
    if (existsSync(this.snapshotPath)) {
      try {
        const parsed = JSON.parse(readFileSync(this.snapshotPath, "utf8"));
        if (isSnapshot(parsed)) {
          snapshot = parsed;
          snapshotFound = true;
        }
      } catch {
        snapshot = undefined;
      }
    }

    let journalText = "";
    if (existsSync(this.journalPath)) {
      journalText = readFileSync(this.journalPath, "utf8");
    }
    const lines = journalText.split("\n");
    let recoveredPartialJournalLine = false;
    const journalEvents: MigrationEvent[] = [];
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index].trim();
      if (!line) continue;
      try {
        journalEvents.push(JSON.parse(line) as MigrationEvent);
      } catch {
        if (index === lines.length - 1 || (index === lines.length - 2 && lines[index + 1] === "")) {
          recoveredPartialJournalLine = true;
        }
      }
    }

    this.engine = new MigrationAcceptanceEngine({
      policy: this.engine.policy,
      verifier: this.engine.verifier,
      requireSignatures: this.engine.requireSignatures,
      closedDevices: snapshot?.closedDevices ?? this.engine.closedDevices,
      leases: snapshot?.leases ?? this.engine.leases(),
    });
    if (snapshot) this.engine.load(snapshot);
    for (const event of journalEvents) this.engine.ingest(event);
    this.appendedSinceCheckpoint = journalEvents.length;

    return {
      events: this.engine.events(),
      leases: this.engine.leases(),
      recoveredPartialJournalLine,
      snapshotFound,
      journalLines: journalEvents.length,
    };
  }

  reload(): LoadResult {
    return this.load();
  }

  reopen(): LoadResult {
    return this.load();
  }

  getEvents(): MigrationEvent[] {
    return this.events();
  }

  events(): MigrationEvent[] {
    return this.engine.events();
  }

  getLease(
    workloadId: string | Pick<Lease, "workloadId" | "generation">,
    generation?: number,
  ): Lease | undefined {
    return this.engine.getLease(workloadId, generation);
  }

  leases(): Lease[] {
    return this.engine.leases();
  }

  setPolicy(policy: MigrationPolicy): void {
    this.engine.setPolicy(policy);
  }

  setVerifier(verifier: SignatureVerifier | undefined): void {
    this.engine.setVerifier(verifier);
  }

  setClosedDevices(devices: ClosedDeviceManifest): void {
    this.engine.setClosedDevices(devices);
  }

  evaluate(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.engine.evaluate(input, generationArg, optionsArg);
  }

  evaluateRelease(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.evaluate(input, generationArg, optionsArg);
  }

  decide(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.evaluate(input, generationArg, optionsArg);
  }

  evaluateGeneration(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.evaluate(input, generationArg, optionsArg);
  }

  evaluateMigration(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.evaluate(input, generationArg, optionsArg);
  }

  release(
    workloadId: string | EvaluateInput | MigrationEvent[],
    generation?: number | Omit<EvaluateInput, "events">,
    options?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    const explicitEvents = Array.isArray(workloadId)
      ? workloadId
      : typeof workloadId === "object" && workloadId.events !== undefined
        ? Array.isArray(workloadId.events)
          ? workloadId.events
          : Object.values(workloadId.events)
        : [];
    const optionEvents =
      options?.events === undefined
        ? []
        : Array.isArray(options.events)
          ? options.events
          : Object.values(options.events);
    const eventList = [
      ...(explicitEvents as unknown[]),
      ...(optionEvents as unknown[]),
    ].flatMap((event) => (Array.isArray(event) ? event : [event])) as MigrationEvent[];
    if (eventList.length > 0) this.appendMany(eventList);
    const decision = this.engine.release(workloadId, generation, options);
    if (decision.status === "release") this.checkpoint();
    return decision;
  }

  commitRelease(
    workloadId: string | EvaluateInput | MigrationEvent[],
    generation?: number | Omit<EvaluateInput, "events">,
    options?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.release(workloadId, generation, options);
  }

  snapshot(): EngineSnapshot {
    return this.engine.snapshot();
  }

  compareWithReference(input: EvaluateInput): {
    equal: boolean;
    actual: ReleaseDecision;
    reference: ReleaseDecision;
    differences: string[];
  } {
    return this.engine.compareWithReference(input);
  }

  reset(): void {
    this.engine.clear();
    this.appendedSinceCheckpoint = 0;
    this.fired.clear();
    for (const path of [this.journalPath, this.snapshotPath]) {
      try {
        rmSync(path, { force: true });
      } catch {
        // Ignore cleanup failures.
      }
    }
    mkdirSync(dirname(this.journalPath), { recursive: true });
  }
}

/**
 * Convenience helper for crash tests: run an operation, simulate one crash
 * boundary, reopen the store and return the recovered state.
 */
export function runCrashScenario<T>(
  dir: string,
  boundary: PersistenceBoundary,
  operation: (store: DurableMigrationStore) => T,
  options: DurableStoreOptions = {},
): {
  crashed: boolean;
  error?: SimulatedCrashError;
  result?: T;
  recovered: LoadResult;
  store: DurableMigrationStore;
} {
  const store = new DurableMigrationStore({
    ...options,
    dir,
    crashAt: boundary,
  });
  store.load();
  let result: T | undefined;
  let error: SimulatedCrashError | undefined;
  let crashed = false;
  try {
    result = operation(store);
  } catch (caught) {
    if (caught instanceof SimulatedCrashError) {
      crashed = true;
      error = caught;
    } else {
      throw caught;
    }
  }
  const reopened = new DurableMigrationStore({ ...options, dir });
  const recovered = reopened.load();
  return { crashed, error, result, recovered, store: reopened };
}

export const simulateCrash = runCrashScenario;
export const simulatePersistenceCrash = runCrashScenario;
