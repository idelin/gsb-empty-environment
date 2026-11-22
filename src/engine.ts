import { recordDigest } from "./events.ts";
import { evaluateRelease } from "./evaluate.ts";
import type {
  ClosedDeviceInput,
  ClosedDeviceManifest,
  EvaluateInput,
  Lease,
  MigrationEvent,
  MigrationPolicy,
  ReleaseDecision,
  SignatureVerifier,
} from "./types.ts";

export interface EngineOptions {
  policy?: MigrationPolicy;
  verifier?: SignatureVerifier;
  requireSignatures?: boolean;
  closedDevices?: ClosedDeviceManifest;
  leases?: Lease[];
  events?: MigrationEvent[];
}

export interface EngineSnapshot {
  format: "migration-acceptance/v1";
  events: MigrationEvent[];
  leases: Lease[];
  closedDevices: ClosedDeviceInput[];
}

export interface IngestResult {
  added: string[];
  duplicates: string[];
  conflicts: Array<{ id: string; reason: string }>;
}

/**
 * Stateful convenience wrapper.  Decisions are always recomputed from the
 * complete event set, which is the reference semantics used to cross-check
 * incremental/journal consumers.
 */
export class MigrationAcceptanceEngine {
  policy: MigrationPolicy;
  verifier?: SignatureVerifier;
  requireSignatures: boolean;
  closedDevices: ClosedDeviceManifest;
  private eventMap: Map<string, MigrationEvent>;
  private digests: Map<string, string>;
  private leaseMap: Map<string, Lease>;

  constructor(options: EngineOptions = {}) {
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
    this.policy = options.policy ?? {};
    this.verifier = options.verifier;
    this.requireSignatures = options.requireSignatures ?? false;
    this.closedDevices = cloneManifest(options.closedDevices);
    this.eventMap = new Map();
    this.digests = new Map();
    this.leaseMap = new Map();
    if (options.events) this.ingest(options.events);
    for (const lease of options.leases ?? []) {
      this.leaseMap.set(leaseKey(lease.workloadId, lease.generation), lease);
    }
  }

  ingest(events: MigrationEvent | MigrationEvent[]): IngestResult {
    const list = Array.isArray(events) ? events : [events];
    const added: string[] = [];
    const duplicates: string[] = [];
    const conflicts: Array<{ id: string; reason: string }> = [];
    for (const event of list) {
      const id =
        typeof event.id === "string"
          ? event.id
          : typeof event.eventId === "string"
            ? event.eventId
            : `auto:${recordDigest(event).slice(0, 24)}`;
      const digest = recordDigest(event);
      if (this.eventMap.has(id)) {
        if (this.digests.get(id) !== digest) {
          conflicts.push({
            id,
            reason: "duplicate-event-id-with-different-payload",
          });
        } else {
          duplicates.push(id);
        }
        continue;
      }
      this.eventMap.set(id, event);
      this.digests.set(id, digest);
      added.push(id);
    }
    return { added, duplicates, conflicts };
  }

  add(event: MigrationEvent): boolean {
    return this.ingest(event).added.length > 0;
  }

  addAll(events: MigrationEvent[]): IngestResult {
    return this.ingest(events);
  }

  addEvent(event: MigrationEvent): boolean {
    return this.add(event);
  }

  append(event: MigrationEvent): boolean {
    return this.add(event);
  }

  appendEvent(event: MigrationEvent): boolean {
    return this.add(event);
  }

  appendEvents(events: MigrationEvent[]): IngestResult {
    return this.ingest(events);
  }

  events(): MigrationEvent[] {
    return [...this.eventMap.values()];
  }

  setClosedDevices(devices: ClosedDeviceManifest): void {
    this.closedDevices = cloneManifest(devices);
  }

  setPolicy(policy: MigrationPolicy): void {
    this.policy = policy;
  }

  setVerifier(verifier: SignatureVerifier | undefined): void {
    this.verifier = verifier;
  }

  evaluate(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    if (
      !Array.isArray(input) &&
      typeof input === "object" &&
      input !== null
    ) {
      input = normalizeInputKeys(input);
    }
    if (Array.isArray(input)) {
      const provided = input.flatMap((event) =>
        Array.isArray(event) ? event : [event],
      );
      const arrayOptions =
        typeof generationArg === "number"
          ? { generation: generationArg }
          : normalizeInputKeys(
              ((generationArg as Omit<EvaluateInput, "events"> | undefined) ??
                {}) as object,
            );
      return evaluateRelease({
        ...arrayOptions,
        events: [...this.events(), ...provided],
        policy: (arrayOptions as Partial<EvaluateInput>).policy ?? this.policy,
        verifier:
          (arrayOptions as Partial<EvaluateInput>).verifier ?? this.verifier,
        closedDevices:
          (arrayOptions as Partial<EvaluateInput>).closedDevices ??
          this.closedDevices,
        leases:
          (arrayOptions as Partial<EvaluateInput>).leases ??
          [...this.leaseMap.values()],
        requireSignatures:
          (arrayOptions as Partial<EvaluateInput>).requireSignatures ??
          this.requireSignatures,
      });
    }
    if (typeof input === "string") {
      return evaluateRelease({
        workloadId: input,
        generation: generationArg ?? 0,
        events: this.events(),
        policy: this.policy,
        verifier: this.verifier,
        requireSignatures: this.requireSignatures,
        closedDevices: this.closedDevices,
        leases: [...this.leaseMap.values()],
        ...(optionsArg ?? {}),
      });
    }
    const merged: EvaluateInput = {
      ...input,
      events:
        input.events === undefined
          ? this.events()
          : [
              ...this.events(),
              ...(Array.isArray(input.events)
                ? input.events
                : Object.values(input.events)),
            ],
      policy: input.policy ?? this.policy,
      verifier: input.verifier ?? this.verifier,
      requireSignatures: input.requireSignatures ?? this.requireSignatures,
      closedDevices: input.closedDevices ?? this.closedDevices,
      leases: input.leases ?? [...this.leaseMap.values()],
    };
    return evaluateRelease(merged);
  }

  evaluateRelease(
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

  decide(
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

  accept(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.evaluate(input, generationArg, optionsArg);
  }

  acceptMigration(
    input: string | EvaluateInput | MigrationEvent[],
    generationArg?: number | Omit<EvaluateInput, "events">,
    optionsArg?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.evaluate(input, generationArg, optionsArg);
  }

  /**
   * Evaluate and durably remember a successful generation lease.  Repeating
   * the call is idempotent for the same target key.
   */
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
    const releaseEvents = [
      ...(explicitEvents as unknown[]),
      ...(optionEvents as unknown[]),
    ].flatMap((event) =>
      Array.isArray(event) ? event : [event],
    ) as MigrationEvent[];
    if (releaseEvents.length > 0) this.ingest(releaseEvents);
    const decision =
      Array.isArray(workloadId)
        ? this.evaluate(
            workloadId,
            (generation as Omit<EvaluateInput, "events"> | undefined) ?? {},
          )
        : typeof workloadId === "string"
          ? this.evaluate(
              workloadId,
              typeof generation === "number" ? generation : 0,
              options,
            )
          : this.evaluate(workloadId);
    const leaseWorkloadId =
      Array.isArray(workloadId)
        ? decision.workloadId
        : typeof workloadId === "string"
        ? workloadId
        : (workloadId.workloadId ?? decision.workloadId);
    const leaseGeneration =
      Array.isArray(workloadId)
        ? decision.generation
        : typeof workloadId === "string"
        ? typeof generation === "number"
          ? generation
          : 0
        : (workloadId.generation ?? decision.generation);
    if (decision.status === "release" && decision.release) {
      const key = leaseKey(leaseWorkloadId, leaseGeneration);
      const existing = this.leaseMap.get(key);
      if (
        !existing ||
        existing.runtimeKey !== decision.release.runtimeKey ||
        existing.chipId !== decision.release.chipId ||
        existing.migrationId !== decision.release.migrationId
      ) {
        this.leaseMap.set(key, {
          workloadId: leaseWorkloadId,
          generation: leaseGeneration,
          runtimeKey: decision.release.runtimeKey,
          chipId: decision.release.chipId,
          migrationId: decision.release.migrationId,
          dependencies: decision.minimalDependencies,
          releasedAt: Date.now(),
        });
      }
    }
    return decision;
  }

  commitRelease(
    workloadId: string | EvaluateInput | MigrationEvent[],
    generation?: number | Omit<EvaluateInput, "events">,
    options?: Partial<EvaluateInput>,
  ): ReleaseDecision {
    return this.release(workloadId, generation, options);
  }

  compareWithReference(input: EvaluateInput): {
    equal: boolean;
    actual: ReleaseDecision;
    reference: ReleaseDecision;
    differences: string[];
  } {
    return compareWithReference(this, input);
  }

  getLease(
    workloadId: string | Pick<Lease, "workloadId" | "generation">,
    generation?: number,
  ): Lease | undefined {
    const key =
      typeof workloadId === "string"
        ? leaseKey(workloadId, generation ?? 0)
        : leaseKey(workloadId.workloadId, workloadId.generation);
    const lease = this.leaseMap.get(key);
    return lease ? { ...lease } : undefined;
  }

  leases(): Lease[] {
    return [...this.leaseMap.values()].map((lease) => ({ ...lease }));
  }

  snapshot(): EngineSnapshot {
    return {
      format: "migration-acceptance/v1",
      events: this.events().map(
        (event) => JSON.parse(JSON.stringify(event)) as MigrationEvent,
      ),
      leases: this.leases(),
      closedDevices: manifestToArray(this.closedDevices),
    };
  }

  load(snapshot: EngineSnapshot): void {
    this.eventMap.clear();
    this.digests.clear();
    this.leaseMap.clear();
    this.closedDevices = cloneManifest(snapshot.closedDevices);
    this.ingest(snapshot.events ?? []);
    for (const lease of snapshot.leases ?? []) {
      this.leaseMap.set(leaseKey(lease.workloadId, lease.generation), lease);
    }
  }

  clear(): void {
    this.eventMap.clear();
    this.digests.clear();
    this.leaseMap.clear();
  }
}

function leaseKey(workloadId: string, generation: number): string {
  return `${workloadId}\u0000${generation}`;
}

function normalizeInputKeys<T extends object>(value: T): T {
  const normalized: Record<string, unknown> = {
    ...(value as Record<string, unknown>),
  };
  for (const [key, entry] of Object.entries(normalized)) {
    const camelKey = key.replace(/[_-]([a-zA-Z0-9])/g, (_, char: string) =>
      char.toUpperCase(),
    );
    if (camelKey !== key && normalized[camelKey] === undefined) {
      normalized[camelKey] = entry;
    }
  }
  return normalized as T;
}

function cloneManifest(
  manifest: ClosedDeviceManifest | undefined,
): ClosedDeviceManifest {
  if (!manifest) return [];
  if (typeof manifest === "string") return manifest;
  if (Array.isArray(manifest)) return [...manifest];
  if (manifest instanceof Set) return new Set(manifest);
  if (manifest instanceof Map) return new Map(manifest);
  return { ...manifest };
}

function manifestToArray(
  manifest: ClosedDeviceManifest | undefined,
): ClosedDeviceInput[] {
  if (!manifest) return [];
  if (typeof manifest === "string") return [manifest];
  if (Array.isArray(manifest)) return [...manifest];
  if (manifest instanceof Set) return [...manifest];
  if (manifest instanceof Map) {
    return [...manifest.entries()]
      .filter(([, state]) => state !== false && state !== null)
      .map(([chipId]) => chipId);
  }
  return Object.entries(manifest)
    .filter(([, state]) => state !== false && state !== null)
    .map(([chipId]) => chipId);
}

export function referenceEvaluate(
  input: EvaluateInput | MigrationEvent[],
  options?: Omit<EvaluateInput, "events">,
): ReleaseDecision {
  return evaluateRelease(
    input as EvaluateInput,
    options as Omit<EvaluateInput, "events">,
  );
}

export const KeyAgent = MigrationAcceptanceEngine;
export const MigrationEvaluator = MigrationAcceptanceEngine;
export const AcceptanceEngine = MigrationAcceptanceEngine;
export const KeyReleaseAgent = MigrationAcceptanceEngine;
export const KeyReleaseEvaluator = MigrationAcceptanceEngine;
export const MigrationAcceptancePackage = MigrationAcceptanceEngine;
export const MigrationAcceptance = MigrationAcceptanceEngine;
export const KeyReleaseEngine = MigrationAcceptanceEngine;

export function createAcceptanceEngine(
  options: EngineOptions = {},
): MigrationAcceptanceEngine {
  return new MigrationAcceptanceEngine(options);
}

export const createMigrationAcceptance = createAcceptanceEngine;

export function recomputeFromEventSet(
  input: EvaluateInput | MigrationEvent[],
  options?: Omit<EvaluateInput, "events">,
): ReleaseDecision {
  if (Array.isArray(input)) return referenceEvaluate(input, options);
  return evaluateRelease({
    ...input,
    events: Array.isArray(input.events)
      ? [...input.events]
      : { ...(input.events ?? {}) },
  });
}

export const recompute = recomputeFromEventSet;

/** Compare a stateful engine decision with a pure event-set recomputation. */
export function compareWithReference(
  engine: MigrationAcceptanceEngine,
  input: EvaluateInput,
): {
  equal: boolean;
  actual: ReleaseDecision;
  reference: ReleaseDecision;
  differences: string[];
} {
  const actual = engine.evaluate(input);
  const reference = referenceEvaluate({
    ...input,
    events:
      input.events === undefined
        ? engine.events()
        : [
            ...engine.events(),
            ...(Array.isArray(input.events)
              ? input.events
              : Object.values(input.events)),
          ],
    policy: input.policy ?? engine.policy,
    verifier: input.verifier ?? engine.verifier,
    closedDevices: input.closedDevices ?? engine.closedDevices,
    leases: input.leases ?? engine.leases(),
  });
  const differences = diffDecisions(actual, reference);
  return { equal: differences.length === 0, actual, reference, differences };
}

function diffDecisions(a: ReleaseDecision, b: ReleaseDecision): string[] {
  const differences: string[] = [];
  if (a.status !== b.status) differences.push(`status:${a.status}!=${b.status}`);
  if (a.targetKey !== b.targetKey) {
    differences.push(`targetKey:${a.targetKey}!=${b.targetKey}`);
  }
  if (a.targetChipId !== b.targetChipId) {
    differences.push(`targetChipId:${a.targetChipId}!=${b.targetChipId}`);
  }
  if (a.dependencies.join(",") !== b.dependencies.join(",")) {
    differences.push(
      `dependencies:${a.dependencies.join(",")}!=${b.dependencies.join(",")}`,
    );
  }
  if (a.missing.join(",") !== b.missing.join(",")) {
    differences.push(`missing:${a.missing.join(",")}!=${b.missing.join(",")}`);
  }
  return differences;
}
