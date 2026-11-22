import type {
  BlockReason,
  MigrationApproval,
  MigrationRelease,
  MigrationTakeover,
  SignedFact,
  SessionState,
  IdentityId,
  MigrationId,
} from "./types.ts";

export type MigrationMerge =
  | { readonly ok: true; readonly completes: boolean }
  | { readonly ok: false; readonly reason: BlockReason; readonly detail?: string };

type AnyMigrationFact =
  | SignedFact<MigrationRelease>
  | SignedFact<MigrationTakeover>
  | SignedFact<MigrationApproval>;

/** The migration party carried by each of the three signed fact kinds. */
export function migratingPartyOf(fact: AnyMigrationFact): IdentityId {
  switch (fact.kind) {
    case "migration-release":
    case "migration-takeover":
      return fact.payload.party;
    case "migration-approval":
      return fact.payload.migratingParty;
  }
}

export function migrationIdOf(fact: AnyMigrationFact): MigrationId {
  return fact.payload.migrationId;
}

/**
 * Facts only merge when bound by the same migration id and every shared field
 * agrees. A fact belonging to a different transaction can never complete a
 * tracker, even if the other two facts are already present.
 */
export function mergeMigrationFact(
  state: SessionState,
  fact: AnyMigrationFact,
): MigrationMerge {
  const migrationId = migrationIdOf(fact);
  const tracker = state.migrations[migrationId];
  const party = migratingPartyOf(fact);

  if (tracker) {
    if (tracker.party !== party) {
      return { ok: false, reason: "malformed-fact", detail: "migration party mismatch" };
    }
  }

  // A party may only have one in-flight migration at a time.
  for (const [otherId, other] of Object.entries(state.migrations)) {
    if (otherId === migrationId || other.completed || other.party !== party) {
      continue;
    }
    return { ok: false, reason: "migration-already-pending", detail: otherId };
  }

  const release = fact.kind === "migration-release" ? fact : tracker?.release ?? null;
  const takeover = fact.kind === "migration-takeover" ? fact : tracker?.takeover ?? null;
  const approval = fact.kind === "migration-approval" ? fact : tracker?.approval ?? null;

  const consistency = checkConsistency(release, takeover, approval);
  if (!consistency.ok) {
    return consistency;
  }
  return { ok: true, completes: Boolean(release && takeover && approval) };
}

function checkConsistency(
  release: SignedFact<MigrationRelease> | null,
  takeover: SignedFact<MigrationTakeover> | null,
  approval: SignedFact<MigrationApproval> | null,
): MigrationMerge {
  const newGeneration =
    release?.payload.newGeneration ??
    takeover?.payload.newGeneration ??
    approval?.payload.newGeneration;
  const sessionId =
    release?.payload.sessionId ??
    takeover?.payload.sessionId ??
    approval?.payload.sessionId;
  const party =
    release?.payload.party ?? takeover?.payload.party ?? approval?.payload.migratingParty;

  if (release) {
    if (release.payload.party !== party) {
      return { ok: false, reason: "malformed-fact", detail: "release party mismatch" };
    }
    if (release.payload.sessionId !== sessionId) {
      return { ok: false, reason: "malformed-fact", detail: "release session mismatch" };
    }
    if (release.payload.newGeneration !== newGeneration) {
      return { ok: false, reason: "malformed-fact", detail: "release generation mismatch" };
    }
    if (release.payload.newGeneration <= release.payload.oldGeneration) {
      return { ok: false, reason: "stale-generation", detail: "new generation must advance" };
    }
  }
  if (takeover) {
    if (takeover.payload.party !== party) {
      return { ok: false, reason: "malformed-fact", detail: "takeover party mismatch" };
    }
    if (takeover.payload.sessionId !== sessionId) {
      return { ok: false, reason: "malformed-fact", detail: "takeover session mismatch" };
    }
    if (takeover.payload.newGeneration !== newGeneration) {
      return { ok: false, reason: "malformed-fact", detail: "takeover generation mismatch" };
    }
  }
  if (approval) {
    if (approval.payload.migratingParty !== party) {
      return { ok: false, reason: "malformed-fact", detail: "approval party mismatch" };
    }
    if (approval.payload.sessionId !== sessionId) {
      return { ok: false, reason: "malformed-fact", detail: "approval session mismatch" };
    }
    if (approval.payload.newGeneration !== newGeneration) {
      return { ok: false, reason: "malformed-fact", detail: "approval generation mismatch" };
    }
  }
  return { ok: true, completes: false };
}

/** Facts still missing for a tracker to complete, in a fixed order. */
export function missingMigrationFacts(
  tracker: {
    release: unknown;
    takeover: unknown;
    approval: unknown;
  },
  migrationId: MigrationId,
) {
  const missing: Array<{
    kind: "migration-release" | "migration-takeover" | "migration-approval";
    migrationId: MigrationId;
  }> = [];
  if (!tracker.release) {
    missing.push({ kind: "migration-release", migrationId });
  }
  if (!tracker.takeover) {
    missing.push({ kind: "migration-takeover", migrationId });
  }
  if (!tracker.approval) {
    missing.push({ kind: "migration-approval", migrationId });
  }
  return missing;
}
