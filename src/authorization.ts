import type {
  NormalizedGrant,
  NormalizedMessage,
  NormalizedProblem,
} from "./model.ts";
import type { StableId } from "./types.ts";

export interface AuthorizationContext {
  model: NormalizedProblem;
  reset: ReadonlySet<StableId>;
  authorityMemo: Map<string, boolean>;
  receiveMemo: Map<string, boolean>;
}

export function createAuthorizationContext(
  model: NormalizedProblem,
  reset: Iterable<StableId> = [],
): AuthorizationContext {
  return {
    model,
    reset: new Set(reset),
    authorityMemo: new Map(),
    receiveMemo: new Map(),
  };
}

function isReset(context: AuthorizationContext, instanceId: StableId): boolean {
  return context.reset.has(instanceId);
}

/**
 * A grant record represents one authenticated generation. It is live at a
 * particular point in the log only when it had been issued, it has not been
 * revoked by then, and neither endpoint was reset by the migration.
 */
export function isGrantActiveAt(
  context: AuthorizationContext,
  grant: NormalizedGrant,
  at: number,
): boolean {
  if (!grant.active) return false;
  if (grant.seq > at) return false;
  if (isReset(context, grant.grantee)) return false;
  if (
    grant.grantorKind === "instance" &&
    grant.grantor !== null &&
    isReset(context, grant.grantor)
  ) {
    return false;
  }
  for (const revoke of context.model.revocations) {
    if (!revoke.targetGrantIds.has(grant.id)) continue;
    if (revoke.seq === null || revoke.seq <= at) return false;
  }
  return true;
}

function carriesAudience(
  grant: NormalizedGrant,
  audience: StableId,
  scope: StableId | null,
): boolean {
  if (scope !== null && grant.scope !== null && scope !== grant.scope) return false;
  if (grant.audience.length === 0) return true;
  if (grant.audience.includes(audience)) return true;
  return scope !== null && grant.audience.includes(scope);
}

function canUseCapability(
  context: AuthorizationContext,
  instanceId: StableId,
  audience: StableId,
  at: number,
  scope: StableId | null,
  visiting: Set<string>,
): boolean {
  const cacheKey = `${instanceId}\u0000${audience}\u0000${scope ?? ""}\u0000${at}`;
  const cached = context.authorityMemo.get(cacheKey);
  if (cached !== undefined) return cached;

  const instance = context.model.instances.get(instanceId);
  if (instance === undefined || isReset(context, instanceId)) {
    context.authorityMemo.set(cacheKey, false);
    return false;
  }
  if (instance.principal === audience || instanceId === audience) {
    context.authorityMemo.set(cacheKey, true);
    return true;
  }

  const visitKey = cacheKey;
  if (visiting.has(visitKey)) return false;
  visiting.add(visitKey);

  let allowed = false;
  for (const grant of context.model.grants) {
    if (grant.grantee !== instanceId) continue;
    if (!carriesAudience(grant, audience, scope)) continue;
    if (!isGrantActiveAt(context, grant, at)) continue;

    if (grant.grantor === null) {
      allowed = true;
      break;
    }
    if (grant.grantorKind === "principal") {
      if (grant.grantor === audience) {
        allowed = true;
        break;
      }
      continue;
    }
    if (canUseCapability(context, grant.grantor, audience, grant.seq, scope, visiting)) {
      allowed = true;
      break;
    }
  }

  visiting.delete(visitKey);
  context.authorityMemo.set(cacheKey, allowed);
  return allowed;
}

/**
 * Checks whether an instance is an authorized recipient for a message in one
 * compatible history. Public messages impose no recipient restriction. For a
 * private message, authorization is either direct (the audience names the
 * instance or its principal) or derived from a live delegated capability
 * chain at the message's log position.
 */
export function isInstanceAuthorized(
  context: AuthorizationContext,
  instanceId: StableId,
  message: NormalizedMessage,
): boolean {
  if (message.visibility === "public") return true;
  if (isReset(context, instanceId)) return false;
  if (message.authorizedInstances.includes(instanceId)) return true;

  const memoKey = `${instanceId}\u0000${message.id}`;
  const cached = context.receiveMemo.get(memoKey);
  if (cached !== undefined) return cached;

  const instance = context.model.instances.get(instanceId);
  if (instance === undefined) {
    context.receiveMemo.set(memoKey, false);
    return false;
  }

  let allowed = false;
  for (const audience of message.audience) {
    if (audience === instance.principal || audience === instance.id) {
      allowed = true;
      break;
    }
    if (
      canUseCapability(
        context,
        instanceId,
        audience,
        message.seq,
        message.scope,
        new Set<string>(),
      )
    ) {
      allowed = true;
      break;
    }
  }

  context.receiveMemo.set(memoKey, allowed);
  return allowed;
}

export function privateMessagesForFrame(
  model: NormalizedProblem,
  frameId: StableId,
): NormalizedMessage[] {
  return model.messages.filter(
    (message) => message.frameId === frameId && message.visibility === "private",
  );
}

export function publicMessagesForFrame(
  model: NormalizedProblem,
  frameId: StableId,
): NormalizedMessage[] {
  return model.messages.filter(
    (message) => message.frameId === frameId && message.visibility === "public",
  );
}

export function protectedMessagesForFrame(
  model: NormalizedProblem,
  frameId: StableId,
): NormalizedMessage[] {
  return model.messages.filter(
    (message) => message.frameId === frameId && message.protected,
  );
}
