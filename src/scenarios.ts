import { MigrationAcceptanceEngine, compareWithReference } from "./engine.ts";
import { evaluateRelease } from "./evaluate.ts";
import type {
  ClosedDeviceManifest,
  EvaluateInput,
  MigrationEvent,
  MigrationPolicy,
  ReleaseDecision,
  TcbVector,
} from "./types.ts";
import {
  DurableMigrationStore,
  runCrashScenario,
  type DurableStoreOptions,
  type PersistenceBoundary,
} from "./store.ts";
import { mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";

export const PERSISTENCE_BOUNDARIES: PersistenceBoundary[] = [
  "before-append",
  "after-journal-append",
  "before-checkpoint",
  "before-snapshot",
  "after-snapshot-write",
  "before-journal-truncate",
  "after-journal-truncate",
  "after-checkpoint",
];

export interface ScenarioOptions {
  migrationId?: string;
  workloadId?: string;
  generation?: number;
  oldChipId?: string;
  newChipId?: string;
  oldRuntimeKey?: string;
  newRuntimeKey?: string;
  oldBootCount?: number;
  newBootCount?: number;
  oldTcb?: TcbVector;
  newTcb?: TcbVector;
  policyDigest?: string;
  ownerDomains?: string[];
  recoveryDevices?: Array<{
    chipId: string;
    adminDomainId: string;
    certificateId?: string;
  }>;
  include?: {
    oldAttestation?: boolean;
    newAttestation?: boolean;
    deactivation?: boolean;
    revocation?: boolean;
    transfer?: boolean;
    owner?: boolean;
    recovery?: boolean;
  };
  causalParents?: Record<string, string[]>;
  policy?: MigrationPolicy;
  closedDevices?: ClosedDeviceManifest;
}

export interface MigrationScenario {
  options: Required<
    Pick<
      ScenarioOptions,
      | "migrationId"
      | "workloadId"
      | "generation"
      | "oldChipId"
      | "newChipId"
      | "oldRuntimeKey"
      | "newRuntimeKey"
      | "oldBootCount"
      | "newBootCount"
      | "policyDigest"
      | "ownerDomains"
    >
  >;
  events: MigrationEvent[];
  policy: MigrationPolicy;
  input: EvaluateInput;
  oldAttestationId: string;
  newAttestationId: string;
  deactivationId: string;
  revocationId: string;
  transferId: string;
  ownerIds: string[];
  recoveryIds: string[];
}

function defaults(options: ScenarioOptions) {
  return {
    migrationId: options.migrationId ?? "mig-1",
    workloadId: options.workloadId ?? "wl-secret",
    generation: options.generation ?? 7,
    oldChipId: options.oldChipId ?? "chip-old",
    newChipId: options.newChipId ?? "chip-new",
    oldRuntimeKey: options.oldRuntimeKey ?? "pk-old",
    newRuntimeKey: options.newRuntimeKey ?? "pk-new",
    oldBootCount: options.oldBootCount ?? 4,
    newBootCount: options.newBootCount ?? 1,
    policyDigest: options.policyDigest ?? "policy-v1",
    ownerDomains: options.ownerDomains ?? ["owner-a"],
  };
}

function withParents(
  event: MigrationEvent,
  options: ScenarioOptions,
): MigrationEvent {
  const parents = options.causalParents?.[String(event.id)];
  return parents ? { ...event, causalParents: parents } : event;
}

/**
 * Build a small, complete migration fixture.  Fields are ordinary signed
 * records; use signRecord/signRecordHmac to attach real signatures when needed.
 */
export function buildMigrationScenario(
  options: ScenarioOptions = {},
): MigrationScenario {
  const base = defaults(options);
  const include = {
    oldAttestation: true,
    newAttestation: true,
    deactivation: true,
    revocation: true,
    transfer: true,
    owner: true,
    recovery: false,
    ...(options.include ?? {}),
  };
  const oldTcb = options.oldTcb ?? { svn: 5, fmw: 10 };
  const newTcb = options.newTcb ?? { svn: 6, fmw: 11 };
  const events: MigrationEvent[] = [];

  const oldAttestationId = `${base.migrationId}:attest:old`;
  const newAttestationId = `${base.migrationId}:attest:new`;
  const deactivationId = `${base.migrationId}:deactivate`;
  const revocationId = `${base.migrationId}:revoke`;
  const transferId = `${base.migrationId}:transfer`;

  if (include.oldAttestation) {
    events.push(
      withParents(
        {
          id: oldAttestationId,
          kind: "attestation",
          action: "attest",
          signer: base.oldChipId,
          payload: {
            chipId: base.oldChipId,
            workloadId: base.workloadId,
            bootCount: base.oldBootCount,
            debug: false,
            runtimeKey: base.oldRuntimeKey,
            nonce: `${base.migrationId}:old-nonce`,
            tcb: oldTcb,
            policyDigest: base.policyDigest,
            generation: base.generation,
          },
        },
        options,
      ),
    );
  }
  if (include.newAttestation) {
    events.push(
      withParents(
        {
          id: newAttestationId,
          kind: "attestation",
          action: "attest",
          signer: base.newChipId,
          payload: {
            chipId: base.newChipId,
            workloadId: base.workloadId,
            bootCount: base.newBootCount,
            debug: false,
            runtimeKey: base.newRuntimeKey,
            nonce: `${base.migrationId}:new-nonce`,
            tcb: newTcb,
            policyDigest: base.policyDigest,
            generation: base.generation,
          },
        },
        options,
      ),
    );
  }
  if (include.deactivation) {
    events.push(
      withParents(
        {
          id: deactivationId,
          kind: "instance-deactivation",
          action: "deactivate-instance",
          signer: base.oldChipId,
          payload: {
            migrationId: base.migrationId,
            chipId: base.oldChipId,
            workloadId: base.workloadId,
            bootCount: base.oldBootCount,
            runtimeKey: base.oldRuntimeKey,
            generation: base.generation,
          },
        },
        options,
      ),
    );
  }
  if (include.revocation) {
    events.push(
      withParents(
        {
          id: revocationId,
          kind: "key-revocation",
          action: "revoke-key",
          signer: "owner-control",
          payload: {
            migrationId: base.migrationId,
            workloadId: base.workloadId,
            runtimeKey: base.oldRuntimeKey,
            generation: base.generation,
          },
        },
        options,
      ),
    );
  }
  if (include.transfer) {
    events.push(
      withParents(
        {
          id: transferId,
          kind: "transfer-acceptance",
          action: "accept-transfer",
          signer: base.newChipId,
          payload: {
            migrationId: base.migrationId,
            workloadId: base.workloadId,
            generation: base.generation,
            chipId: base.newChipId,
            targetChipId: base.newChipId,
            targetRuntimeKey: base.newRuntimeKey,
            runtimeKey: base.newRuntimeKey,
            oldChipId: base.oldChipId,
            oldRuntimeKey: base.oldRuntimeKey,
            attestationId: newAttestationId,
          },
        },
        options,
      ),
    );
  }

  const ownerIds: string[] = [];
  if (include.owner) {
    base.ownerDomains.forEach((domain, index) => {
      const id = `${base.migrationId}:owner:${index}`;
      ownerIds.push(id);
      events.push(
        withParents(
          {
            id,
            kind: "owner-confirmation",
            action: "confirm-migration",
            signer: domain,
            payload: {
              migrationId: base.migrationId,
              workloadId: base.workloadId,
              generation: base.generation,
              adminDomainId: domain,
              ownerId: domain,
              targetChipId: base.newChipId,
              targetRuntimeKey: base.newRuntimeKey,
            },
          },
          options,
        ),
      );
    });
  }

  const recoveryIds: string[] = [];
  if (include.recovery) {
    (options.recoveryDevices ?? []).forEach((device, index) => {
      const id = `${base.migrationId}:recovery:${index}`;
      recoveryIds.push(id);
      events.push(
        withParents(
          {
            id,
            kind: "recovery-confirmation",
            action: "confirm-recovery",
            signer: device.adminDomainId,
            payload: {
              migrationId: base.migrationId,
              workloadId: base.workloadId,
              generation: base.generation,
              chipId: device.chipId,
              adminDomainId: device.adminDomainId,
              certificateId:
                device.certificateId ?? `${device.chipId}:${index}:cert`,
              targetChipId: base.newChipId,
              targetRuntimeKey: base.newRuntimeKey,
            },
          },
          options,
        ),
      );
    });
  }

  const policy: MigrationPolicy = {
    digest: base.policyDigest,
    baselineTcb: oldTcb,
    ownerThreshold:
      options.policy?.ownerThreshold ??
      (base.ownerDomains.length > 0
        ? base.ownerDomains.length
        : include.owner
          ? 0
          : 1),
    ...(options.policy ?? {}),
  };
  const input: EvaluateInput = {
    workloadId: base.workloadId,
    generation: base.generation,
    events,
    policy,
    closedDevices: options.closedDevices ?? [base.oldChipId],
  };

  return {
    options: base,
    events,
    policy,
    input,
    oldAttestationId,
    newAttestationId,
    deactivationId,
    revocationId,
    transferId,
    ownerIds,
    recoveryIds,
  };
}

export function evaluateScenario(
  scenario: MigrationScenario,
  overrides: Partial<EvaluateInput> = {},
): ReleaseDecision {
  return evaluateRelease({ ...scenario.input, ...overrides });
}

export function verifyScenarioAgainstReference(
  scenario: MigrationScenario,
  overrides: Partial<EvaluateInput> = {},
): {
  equal: boolean;
  actual: ReleaseDecision;
  reference: ReleaseDecision;
  differences: string[];
} {
  const engine = new MigrationAcceptanceEngine({
    policy: scenario.policy,
    events: scenario.events,
  });
  return compareWithReference(engine, { ...scenario.input, ...overrides });
}

export function persistenceCrashScenario(
  dir: string,
  boundary: PersistenceBoundary,
  scenario: MigrationScenario,
  options: DurableStoreOptions = {},
): ReturnType<typeof runCrashScenario> {
  return runCrashScenario(
    dir,
    boundary,
    (store: DurableMigrationStore) => store.addAll(scenario.events),
    {
      policy: scenario.policy,
      closedDevices: scenario.input.closedDevices,
      ...options,
    },
  );
}

export interface BoundaryVerification {
  boundary: PersistenceBoundary;
  crashed: boolean;
  recoveredEventIds: string[];
  expectedEventIds: string[];
  matchesExpected: boolean;
  referenceStatus: string;
  recoveredStatus: string;
  statusMatches: boolean;
}

/**
 * Exercise every durability boundary for the same migration fixture.  The
 * recovered event set must be a valid durable prefix: nothing before the crash
 * boundary and everything that was already fsynced.
 */
export function verifyPersistenceBoundaries(
  rootDir: string,
  scenario: MigrationScenario,
  options: DurableStoreOptions = {},
): BoundaryVerification[] {
  mkdirSync(rootDir, { recursive: true });
  const reference = evaluateRelease({
    ...scenario.input,
    events: scenario.events,
  });
  const results: BoundaryVerification[] = [];
  for (const boundary of PERSISTENCE_BOUNDARIES) {
    const dir = join(rootDir, boundary);
    rmSync(dir, { recursive: true, force: true });
    const run = runCrashScenario(
      dir,
      boundary,
      (store) => {
        store.addAll(scenario.events);
        store.checkpoint();
      },
      {
        policy: scenario.policy,
        closedDevices: scenario.input.closedDevices,
        ...options,
      },
    );
    const recoveredEventIds = run.recovered.events
      .map((event) => String(event.id))
      .sort();
    const expectedCount =
      boundary === "before-append"
        ? 0
        : boundary === "after-journal-append"
          ? 1
          : scenario.events.length;
    const expectedEventIds = scenario.events
      .slice(0, expectedCount)
      .map((event) => String(event.id))
      .sort();
    const recoveredDecision = run.store.evaluate(scenario.input);
    results.push({
      boundary,
      crashed: run.crashed,
      recoveredEventIds,
      expectedEventIds,
      matchesExpected:
        JSON.stringify(recoveredEventIds) === JSON.stringify(expectedEventIds),
      referenceStatus: reference.status,
      recoveredStatus: recoveredDecision.status,
      statusMatches: recoveredDecision.status === reference.status,
    });
  }
  return results;
}
