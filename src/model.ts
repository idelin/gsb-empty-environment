import {
  MigrationInputError,
  type ChannelSpec,
  type FrameSpec,
  type GrantSpec,
  type InstanceSpec,
  type MessageSpec,
  type MigrationProblem,
  type MutexGroupSpec,
  type RevokeSpec,
  type SignedEvent,
  type StableId,
} from "./types.ts";

export interface NormalizedInstance {
  id: StableId;
  principal: StableId;
  connection: StableId | null;
  generation: number;
  interruptible: boolean;
  stableOrder: number | null;
}

export interface NormalizedFrame {
  id: StableId;
  channelIds: StableId[];
  candidateConstraints: StableId[][];
  domain: StableId[];
  required: boolean;
  explicitChannels: boolean;
}

export interface NormalizedChannel {
  id: StableId;
  frameId: StableId;
  candidateConstraints: StableId[][];
  optional: boolean;
  required: boolean;
  stop: boolean;
}

export interface NormalizedMessage {
  id: StableId;
  frameId: StableId;
  channelIds: StableId[];
  visibility: "public" | "private";
  audience: StableId[];
  authorizedInstances: StableId[];
  sender: StableId | null;
  scope: StableId | null;
  seq: number;
  confirmed: boolean;
  protected: boolean;
  optional: boolean;
}

export interface NormalizedGrant {
  id: StableId;
  grantor: StableId | null;
  grantorKind: "principal" | "instance";
  grantee: StableId;
  audience: StableId[];
  scope: StableId | null;
  generation: number;
  seq: number;
  active: boolean;
}

export interface NormalizedRevoke {
  id: StableId;
  grantId: StableId | null;
  grantee: StableId | null;
  grantor: StableId | null;
  audience: StableId[];
  scope: StableId | null;
  generation: number | null;
  seq: number | null;
  all: boolean;
  targetGrantIds: Set<StableId>;
}

export interface NormalizedGroup {
  id: StableId;
  frameIds: StableId[];
  channelIds: StableId[];
  atomic: boolean;
  required: boolean;
}

export interface NormalizedProblem {
  instances: Map<StableId, NormalizedInstance>;
  instanceOrder: StableId[];
  frames: Map<StableId, NormalizedFrame>;
  frameOrder: StableId[];
  channels: Map<StableId, NormalizedChannel>;
  channelOrder: StableId[];
  messages: NormalizedMessage[];
  grants: NormalizedGrant[];
  revocations: NormalizedRevoke[];
  groups: NormalizedGroup[];
  protectedPublicMessages: Set<StableId>;
}

interface RawFrame {
  id: StableId;
  candidates: StableId[] | null;
  channels: StableId[];
  optional: boolean | null;
  required: boolean | null;
}

interface RawChannel {
  id: StableId;
  frameId: StableId | null;
  candidates: StableId[] | null;
  optional: boolean | null;
  required: boolean | null;
  stop: boolean;
}

interface RawMessage {
  id: StableId;
  channelIds: StableId[];
  frameId: StableId | null;
  visibility: "public" | "private" | null;
  audience: StableId[];
  authorizedInstances: StableId[];
  sender: StableId | null;
  scope: StableId | null;
  seq: number | null;
  candidates: StableId[] | null;
  confirmed: boolean;
  protected: boolean | null;
  optional: boolean | null;
}

const NATURAL_CHUNK = /(\d+|\D+)/g;

function naturalChunks(value: string): string[] {
  return value.match(NATURAL_CHUNK) ?? [value];
}

/**
 * Stable, locale-independent ordering. Numeric chunks compare numerically, so
 * i2 sorts before i10. This is the ordering used for all tie breaks.
 */
export function compareStableIds(a: StableId, b: StableId): number {
  const left = naturalChunks(a);
  const right = naturalChunks(b);
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    const l = left[index] as string;
    const r = right[index] as string;
    if (l === r) continue;
    const ln = /^\d+$/.test(l);
    const rn = /^\d+$/.test(r);
    if (ln && rn) {
      const strippedL = l.replace(/^0+(?=\d)/, "");
      const strippedR = r.replace(/^0+(?=\d)/, "");
      if (strippedL.length !== strippedR.length) return strippedL.length - strippedR.length;
      if (strippedL !== strippedR) return strippedL < strippedR ? -1 : 1;
      return l < r ? -1 : 1;
    }
    if (ln !== rn) return ln ? -1 : 1;
    return l < r ? -1 : 1;
  }
  return left.length - right.length;
}

function uniqueSorted(values: Iterable<StableId>): StableId[] {
  return [...new Set(values)].sort(compareStableIds);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asId(value: unknown): StableId | null {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  if (isRecord(value)) {
    const candidate =
      value.id ??
      value.messageId ??
      value.message ??
      value.channelId ??
      value.channel ??
      value.instanceId ??
      value.instance;
    if (typeof candidate === "string" || typeof candidate === "number") return String(candidate);
  }
  return null;
}

function asIdArray(value: unknown): StableId[] | null {
  if (value === undefined || value === null) return null;
  if (Array.isArray(value)) {
    const result: StableId[] = [];
    for (const item of value) {
      const id = asId(item);
      if (id !== null) result.push(id);
    }
    return uniqueSorted(result);
  }
  if (isRecord(value)) {
    const nested =
      value.candidates ??
      value.receivers ??
      value.instances ??
      value.receiverCandidates ??
      value.possibleReceivers;
    if (nested !== undefined) return asIdArray(nested);
  }
  const id = asId(value);
  return id === null ? null : [id];
}

function firstIdArray(...values: unknown[]): StableId[] | null {
  for (const value of values) {
    const ids = asIdArray(value);
    if (ids !== null) return ids;
  }
  return null;
}

function asFiniteNumber(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const numeric = Number(value);
    if (Number.isFinite(numeric)) return numeric;
  }
  return null;
}

function asBoolean(value: unknown): boolean | null {
  if (typeof value === "boolean") return value;
  if (value === "true") return true;
  if (value === "false") return false;
  return null;
}

function intersect(left: StableId[] | null, right: StableId[] | null): StableId[] | null {
  if (left === null) return right;
  if (right === null) return left;
  const allowed = new Set(right);
  return uniqueSorted(left.filter((id) => allowed.has(id)));
}

function candidateConstraintsFromFrame(spec: FrameSpec): StableId[] | null {
  return firstIdArray(
    spec.candidates,
    spec.receiptCandidates,
    spec.possibleReceivers,
    spec.receiverCandidates,
  );
}

function candidateConstraintsFromChannel(spec: ChannelSpec): StableId[] | null {
  return firstIdArray(
    spec.candidates,
    spec.receiptCandidates,
    spec.possibleReceivers,
    spec.receiverCandidates,
  );
}

function messageChannelIds(spec: MessageSpec): StableId[] {
  const values: StableId[] = [];
  for (const candidate of [
    spec.channel,
    spec.channelId,
    spec.queue,
    spec.queueId,
  ]) {
    const id = asId(candidate);
    if (id !== null) values.push(id);
  }
  const many = asIdArray(spec.channels);
  if (many !== null) values.push(...many);
  return uniqueSorted(values);
}

function messageCandidates(spec: MessageSpec): StableId[] | null {
  return firstIdArray(
    spec.candidates,
    spec.receiverCandidates,
    spec.possibleReceivers,
    spec.ack,
    spec.receivers,
  );
}

function messageConfirmed(spec: MessageSpec): boolean {
  if (spec.confirmed === true || spec.acknowledged === true) return true;
  return messageCandidates(spec) !== null;
}

function messageVisibility(spec: MessageSpec): "public" | "private" | null {
  const visibility = typeof spec.visibility === "string" ? spec.visibility.toLowerCase() : null;
  if (visibility === "private" || visibility === "secret" || visibility === "confidential") {
    return "private";
  }
  if (visibility === "public") return "public";
  if (spec.private === true) return "private";
  if (spec.private === false) return "public";
  if (spec.public === true) return "public";
  const audience = firstIdArray(
    spec.audience,
    spec.recipients,
    spec.privateTo,
    spec.to,
    spec.recipient,
    spec.authorizedPrincipals,
    spec.allowedPrincipals,
  );
  if (audience !== null && audience.length > 0) return "private";
  if (typeof spec.kind === "string") {
    const kind = spec.kind.toLowerCase();
    if (kind === "private" || kind === "reply" || kind === "direct") return "private";
    if (kind === "public" || kind === "broadcast") return "public";
  }
  return null;
}

function normalizeVisibility(value: unknown, fallback: "public" | "private"): "public" | "private" {
  if (typeof value !== "string") return fallback;
  const normalized = value.toLowerCase();
  if (normalized === "private" || normalized === "secret" || normalized === "confidential") {
    return "private";
  }
  if (normalized === "public" || normalized === "broadcast") return "public";
  return fallback;
}

function eventType(event: Record<string, unknown>): string {
  const value = event.type ?? event.op ?? event.action ?? event.kind;
  return typeof value === "string" ? value.toLowerCase() : "";
}

function eventPayload(event: SignedEvent): Record<string, unknown> {
  if (isRecord(event.payload)) {
    return { ...event, ...event.payload };
  }
  return event;
}

function ensureRawFrame(frames: Map<StableId, RawFrame>, id: StableId): RawFrame {
  const existing = frames.get(id);
  if (existing !== undefined) return existing;
  const created: RawFrame = {
    id,
    candidates: null,
    channels: [],
    optional: null,
    required: null,
  };
  frames.set(id, created);
  return created;
}

function ensureRawChannel(channels: Map<StableId, RawChannel>, id: StableId): RawChannel {
  const existing = channels.get(id);
  if (existing !== undefined) return existing;
  const created: RawChannel = {
    id,
    frameId: null,
    candidates: null,
    optional: null,
    required: null,
    stop: false,
  };
  channels.set(id, created);
  return created;
}

function ensureRawMessage(messages: Map<StableId, RawMessage>, id: StableId): RawMessage {
  const existing = messages.get(id);
  if (existing !== undefined) return existing;
  const created: RawMessage = {
    id,
    channelIds: [],
    frameId: null,
    visibility: null,
    audience: [],
    authorizedInstances: [],
    sender: null,
    scope: null,
    seq: null,
    candidates: null,
    confirmed: false,
    protected: null,
    optional: null,
  };
  messages.set(id, created);
  return created;
}

function addDistinct(target: StableId[], values: Iterable<StableId>): void {
  const seen = new Set(target);
  for (const value of values) {
    if (!seen.has(value)) {
      seen.add(value);
      target.push(value);
    }
  }
}

function inferInstance(
  instances: Map<StableId, NormalizedInstance>,
  id: StableId,
  connection: StableId | null = null,
): void {
  if (instances.has(id)) return;
  instances.set(id, {
    id,
    principal: id,
    connection,
    generation: 1,
    interruptible: true,
    stableOrder: null,
  });
}

function sameGrantAudience(grant: NormalizedGrant, audience: StableId[], scope: StableId | null): boolean {
  if (audience.length === 0 && scope === null) return true;
  if (scope !== null && grant.scope !== null && scope !== grant.scope) return false;
  if (audience.length === 0) return true;
  if (grant.audience.length === 0) return true;
  const granted = new Set(grant.audience);
  return audience.some((item) => granted.has(item));
}

function applyRevocations(grants: NormalizedGrant[], revocations: NormalizedRevoke[]): void {
  for (const revoke of revocations) {
    const matching = grants.filter((grant) => {
      if (revoke.seq !== null && grant.seq > revoke.seq) return false;
      if (revoke.grantId !== null && grant.id !== revoke.grantId) return false;
      if (revoke.grantee !== null && grant.grantee !== revoke.grantee) return false;
      if (revoke.grantor !== null) {
        const grantorMatches =
          grant.grantor === revoke.grantor ||
          (grant.grantorKind === "principal" && grant.grantor === revoke.grantor);
        if (!grantorMatches) return false;
      }
      if (revoke.scope !== null && grant.scope !== null && revoke.scope !== grant.scope) {
        return false;
      }
      if (revoke.audience.length > 0 && grant.audience.length > 0) {
        const granted = new Set(grant.audience);
        if (!revoke.audience.some((item) => granted.has(item))) return false;
      }
      if (revoke.generation !== null && grant.generation !== revoke.generation) return false;
      return true;
    });

    let targeted = matching;
    if (revoke.generation === null && revoke.grantId === null && !revoke.all && matching.length > 0) {
      const generation = Math.max(...matching.map((grant) => grant.generation));
      targeted = matching.filter((grant) => grant.generation === generation);
    }
    for (const grant of targeted) revoke.targetGrantIds.add(grant.id);
  }
}

function protectedMessageIds(problem: Partial<MigrationProblem>): {
  explicit: Set<StableId> | null;
  preserveAll: boolean;
} {
  const source =
    problem.protectedPublicMessages ??
    problem.preservePublicMessages ??
    problem.confirmedPublicMessages;
  if (source === undefined) {
    return { explicit: null, preserveAll: problem.preserveAllPublicMessages === true };
  }
  const explicit = new Set<StableId>();
  for (const item of source) {
    const id = asId(item);
    if (id !== null) explicit.add(id);
  }
  return { explicit, preserveAll: problem.preserveAllPublicMessages === true };
}

export function normalizeMigrationProblem(
  input: MigrationProblem | readonly SignedEvent[],
): NormalizedProblem {
  const problem: Partial<MigrationProblem> = Array.isArray(input)
    ? { events: input }
    : (input as MigrationProblem);

  const instances = new Map<StableId, NormalizedInstance>();
  const explicitInstances = problem.instances;
  if (Array.isArray(explicitInstances)) {
    for (const entry of explicitInstances as readonly (InstanceSpec | StableId | number)[]) {
      if (typeof entry === "string" || typeof entry === "number") {
        const id = String(entry);
        instances.set(id, {
          id,
          principal: id,
          connection: null,
          generation: 1,
          interruptible: true,
          stableOrder: null,
        });
        continue;
      }
      if (!isRecord(entry)) continue;
      const id = asId(entry);
      if (id === null) continue;
      instances.set(id, {
        id,
        principal: asId(entry.principal) ?? id,
        connection:
          asId(entry.connection) ??
          asId(entry.connectionId) ??
          asId(entry.transport),
        generation: asFiniteNumber(entry.generation) ?? 1,
        interruptible:
          asBoolean(entry.interruptible) ??
          asBoolean(entry.canReauthenticate) ??
          true,
        stableOrder: asFiniteNumber(entry.stableOrder),
      });
    }
  } else if (explicitInstances !== undefined) {
    const byId = explicitInstances as Readonly<Record<string, Omit<InstanceSpec, "id">>>;
    for (const [id, entry] of Object.entries(byId)) {
      instances.set(id, {
        id,
        principal: asId(entry.principal) ?? id,
        connection: asId(entry.connection) ?? asId(entry.connectionId) ?? asId(entry.transport),
        generation: asFiniteNumber(entry.generation) ?? 1,
        interruptible:
          asBoolean(entry.interruptible) ??
          asBoolean(entry.canReauthenticate) ??
          true,
        stableOrder: asFiniteNumber(entry.stableOrder),
      });
    }
  }

  for (const connection of problem.connections ?? []) {
    const connectionId = asId(connection.id);
    if (connectionId === null) continue;
    for (const member of connection.instances ?? []) {
      const id = asId(member);
      if (id === null) continue;
      inferInstance(instances, id, connectionId);
      const instance = instances.get(id);
      if (instance !== undefined && instance.connection === null) instance.connection = connectionId;
    }
  }

  const rawFrames = new Map<StableId, RawFrame>();
  const rawChannels = new Map<StableId, RawChannel>();
  const rawMessages = new Map<StableId, RawMessage>();
  const grants: NormalizedGrant[] = [];
  const rawRevokes: RevokeSpec[] = [];
  const rawGroups: MutexGroupSpec[] = [];

  for (const spec of problem.frames ?? []) {
    const id = asId(spec.id) ?? asId(spec);
    if (id === null) continue;
    const frame = ensureRawFrame(rawFrames, id);
    frame.candidates = intersect(frame.candidates, candidateConstraintsFromFrame(spec));
    frame.optional = asBoolean(spec.optional) ?? frame.optional;
    frame.required = asBoolean(spec.required) ?? frame.required;
    for (const channel of [...(spec.channels ?? []), ...(spec.queues ?? [])]) {
      const channelId = asId(channel);
      if (channelId === null) continue;
      addDistinct(frame.channels, [channelId]);
      const rawChannel = ensureRawChannel(rawChannels, channelId);
      rawChannel.frameId = id;
    }
  }

  for (const spec of [...(problem.channels ?? []), ...(problem.queues ?? [])]) {
    const id = asId(spec.id) ?? asId(spec);
    if (id === null) continue;
    const channel = ensureRawChannel(rawChannels, id);
    const frameId = asId(spec.frame) ?? asId(spec.frameId);
    if (frameId !== null) {
      channel.frameId = frameId;
      ensureRawFrame(rawFrames, frameId);
    }
    channel.candidates = intersect(channel.candidates, candidateConstraintsFromChannel(spec));
    channel.optional = asBoolean(spec.optional) ?? channel.optional;
    channel.required = asBoolean(spec.required) ?? channel.required;
    channel.stop = channel.stop || spec.stop === true;
  }

  for (let index = 0; index < (problem.messages ?? []).length; index += 1) {
    const spec = (problem.messages ?? [])[index] as MessageSpec;
    const id = asId(spec.id) ?? `@message:${index}`;
    const message = ensureRawMessage(rawMessages, id);
    addDistinct(message.channelIds, messageChannelIds(spec));
    const frameId = asId(spec.frame) ?? asId(spec.frameId);
    if (frameId !== null) message.frameId = frameId;
    message.visibility = messageVisibility(spec) ?? message.visibility;
    addDistinct(
      message.audience,
      firstIdArray(
        spec.audience,
        spec.recipients,
        spec.privateTo,
        spec.to,
        spec.recipient,
        spec.authorizedPrincipals,
        spec.allowedPrincipals,
      ) ?? [],
    );
    addDistinct(message.authorizedInstances, asIdArray(spec.authorizedInstances) ?? []);
    const sender = asId(spec.sender);
    if (sender !== null) message.sender = sender;
    const scope = asId(spec.scope);
    if (scope !== null) message.scope = scope;
    message.seq = asFiniteNumber(spec.seq ?? spec.at) ?? message.seq;
    message.candidates = intersect(message.candidates, messageCandidates(spec));
    message.confirmed = message.confirmed || messageConfirmed(spec);
    const protectedFlag = asBoolean(spec.protected);
    if (protectedFlag !== null) message.protected = protectedFlag;
    const optional = asBoolean(spec.optional);
    if (optional !== null) message.optional = optional;
    if (asBoolean(spec.required) === true) message.optional = false;
  }

  for (const spec of [...(problem.grants ?? []), ...(problem.delegations ?? [])]) {
    const grantee = firstIdArray(spec.grantee, spec.toInstance, spec.to, spec.instance, spec.receiver);
    const target = grantee?.[0] ?? null;
    if (target === null) {
      throw new MigrationInputError("A grant is missing its grantee instance");
    }
    inferInstance(instances, target);
    const grantor = asId(spec.grantor) ?? asId(spec.from) ?? asId(spec.by) ?? asId(spec.sender);
    const explicitKind = (spec.grantorType ?? spec.fromKind ?? "").toLowerCase();
    const grantorKind =
      explicitKind === "instance" || explicitKind === "principal"
        ? (explicitKind as "principal" | "instance")
        : grantor !== null && instances.has(grantor)
          ? "instance"
          : "principal";
    let audience = firstIdArray(spec.audience, spec.principal, spec.capability) ?? [];
    if (audience.length === 0 && typeof spec.scope === "string") audience = [spec.scope];
    if (audience.length === 0 && grantor !== null && grantorKind === "principal") {
      audience = [grantor];
    }
    grants.push({
      id: asId(spec.id) ?? `@grant:${grants.length}`,
      grantor,
      grantorKind,
      grantee: target,
      audience: uniqueSorted(audience),
      scope: asId(spec.scope),
      generation: asFiniteNumber(spec.generation ?? spec.gen ?? spec.version) ?? 1,
      seq: asFiniteNumber(spec.seq ?? spec.at) ?? Number.NEGATIVE_INFINITY,
      active: spec.active !== false,
    });
  }

  for (const spec of [...(problem.revocations ?? []), ...(problem.revokes ?? [])]) {
    rawRevokes.push(spec);
  }

  for (const group of [...(problem.mutexGroups ?? []), ...(problem.groups ?? [])]) {
    if (typeof group === "string" || typeof group === "number") {
      rawGroups.push({ id: String(group), members: [] });
    } else {
      rawGroups.push(group);
    }
  }

  const events = [...(problem.events ?? []), ...(problem.log ?? [])];
  for (let index = 0; index < events.length; index += 1) {
    const original = events[index] as SignedEvent;
    const event = eventPayload(original);
    const type = eventType(event);
    const seq = asFiniteNumber(event.seq ?? event.at);

    if (
      type === "instance" ||
      type === "session" ||
      type === "authenticate" ||
      type === "auth"
    ) {
      const id = asId(event.instanceId) ?? asId(event.instance) ?? asId(event.id);
      if (id === null) continue;
      instances.set(id, {
        id,
        principal: asId(event.principal) ?? asId(event.subject) ?? id,
        connection:
          asId(event.connection) ??
          asId(event.connectionId) ??
          asId(event.transport),
        generation: asFiniteNumber(event.generation) ?? 1,
        interruptible:
          asBoolean(event.interruptible) ??
          asBoolean(event.canReauthenticate) ??
          true,
        stableOrder: asFiniteNumber(event.stableOrder),
      });
      continue;
    }

    if (
      type === "send" ||
      type === "enqueue" ||
      type === "message" ||
      type === "reply" ||
      type === "deliver"
    ) {
      const rawMessage = isRecord(event.message) ? event.message : null;
      const id =
        asId(rawMessage) ??
        asId(event.messageId) ??
        asId(event.message) ??
        asId(event.id) ??
        `@event-message:${index}`;
      const message = ensureRawMessage(rawMessages, id);
      if (rawMessage !== null) {
        addDistinct(
          message.channelIds,
          messageChannelIds(rawMessage as unknown as MessageSpec),
        );
        const frameId = asId(rawMessage.frame) ?? asId(rawMessage.frameId);
        if (frameId !== null) message.frameId = frameId;
        message.visibility =
          messageVisibility(rawMessage as unknown as MessageSpec) ?? message.visibility;
        addDistinct(
          message.audience,
          firstIdArray(
            rawMessage.audience,
            rawMessage.recipients,
            rawMessage.privateTo,
            rawMessage.to,
            rawMessage.recipient,
            rawMessage.authorizedPrincipals,
            rawMessage.allowedPrincipals,
          ) ?? [],
        );
        addDistinct(
          message.authorizedInstances,
          asIdArray(rawMessage.authorizedInstances) ?? [],
        );
        const sender = asId(rawMessage.sender);
        if (sender !== null) message.sender = sender;
        message.seq = asFiniteNumber(rawMessage.seq ?? rawMessage.at) ?? message.seq;
        message.candidates = intersect(
          message.candidates,
          messageCandidates(rawMessage as unknown as MessageSpec),
        );
        message.confirmed =
          message.confirmed || messageConfirmed(rawMessage as unknown as MessageSpec);
        const protectedFlag = asBoolean(rawMessage.protected);
        if (protectedFlag !== null) message.protected = protectedFlag;
      }
      const channelIds = firstIdArray(
        event.channel,
        event.channelId,
        event.queue,
        event.queueId,
      );
      if (channelIds !== null) addDistinct(message.channelIds, channelIds);
      const frameId = asId(event.frame) ?? asId(event.frameId);
      if (frameId !== null) message.frameId = frameId;
      const visibility =
        event.visibility ??
        (event.private === true ? "private" : event.public === true ? "public" : undefined);
      if (visibility !== undefined) {
        message.visibility = normalizeVisibility(visibility, message.visibility ?? "public");
      }
      const audience = firstIdArray(
        event.audience,
        event.recipients,
        event.privateTo,
        event.recipient,
        event.to,
        event.authorizedPrincipals,
      );
      if (audience !== null) addDistinct(message.audience, audience);
      const sender = asId(event.sender) ?? asId(event.from);
      if (sender !== null) message.sender = sender;
      const scope = asId(event.scope);
      if (scope !== null) message.scope = scope;
      if (seq !== null) message.seq = seq;
      const candidates = firstIdArray(
        event.candidates,
        event.receiverCandidates,
        event.possibleReceivers,
      );
      if (candidates !== null) message.candidates = intersect(message.candidates, candidates);
      if (asBoolean(event.confirmed) === true || asBoolean(event.acknowledged) === true) {
        message.confirmed = true;
      }
      const protectedFlag = asBoolean(event.protected);
      if (protectedFlag !== null) message.protected = protectedFlag;
      continue;
    }

    if (type === "ack" || type === "acknowledge" || type === "confirm" || type === "receipt") {
      const id =
        asId(event.messageId) ??
        asId(event.message) ??
        asId(event.ackFor) ??
        asId(event.id);
      if (id === null) continue;
      const message = ensureRawMessage(rawMessages, id);
      const candidates = firstIdArray(
        event.candidates,
        event.receivers,
        event.instances,
        event.receiverCandidates,
        event.possibleReceivers,
      );
      if (candidates !== null) message.candidates = intersect(message.candidates, candidates);
      message.confirmed = true;
      const frameId = asId(event.frame) ?? asId(event.frameId);
      if (frameId !== null) message.frameId = frameId;
      continue;
    }

    if (
      type === "delegate" ||
      type === "grant" ||
      type === "authorize" ||
      type === "permit"
    ) {
      const grantee =
        asId(event.grantee) ??
        asId(event.toInstance) ??
        asId(event.to) ??
        asId(event.instance) ??
        asId(event.receiver);
      if (grantee === null) continue;
      inferInstance(instances, grantee);
      const grantor =
        asId(event.grantor) ??
        asId(event.from) ??
        asId(event.by) ??
        asId(event.sender);
      const grantorKind =
        grantor !== null && instances.has(grantor) ? "instance" : "principal";
      let audience = firstIdArray(
        event.audience,
        event.principal,
        event.capability,
        event.scope,
      ) ?? [];
      if (audience.length === 0 && grantor !== null && grantorKind === "principal") {
        audience = [grantor];
      }
      grants.push({
        id:
          asId(event.grantId) ??
          asId(event.permit) ??
          asId(event.grant) ??
          asId(event.id) ??
          `@event-grant:${grants.length}`,
        grantor,
        grantorKind,
        grantee,
        audience: uniqueSorted(audience),
        scope: asId(event.scope),
        generation: asFiniteNumber(event.generation ?? event.gen ?? event.version) ?? 1,
        seq: seq ?? Number.NEGATIVE_INFINITY,
        active: event.active !== false,
      });
      continue;
    }

    if (type === "revoke" || type === "revocation" || type === "withdraw") {
      const target = asId(event.target);
      const targetIsGrant = target !== null && grants.some((grant) => grant.id === target);
      rawRevokes.push({
        id: asId(event.id) ?? undefined,
        grantId:
          asId(event.grantId) ??
          asId(event.permit) ??
          asId(event.permitId) ??
          asId(event.delegationId) ??
          (targetIsGrant ? target ?? undefined : undefined),
        grantee:
          asId(event.grantee) ??
          asId(event.toInstance) ??
          asId(event.to) ??
          asId(event.instance) ??
          asId(event.receiver) ??
          (target !== null && !targetIsGrant ? target : undefined),
        grantor: asId(event.grantor) ?? asId(event.from),
        audience: firstIdArray(event.audience, event.principal, event.capability),
        scope: asId(event.scope),
        generation: asFiniteNumber(event.generation ?? event.gen ?? event.version),
        seq,
        all: event.all === true,
      });
      continue;
    }

    if (type === "frame" || type === "batch" || type === "receipt-set" || type === "receiptset") {
      const id = asId(event.frame) ?? asId(event.frameId) ?? asId(event.id);
      if (id === null) continue;
      const frame = ensureRawFrame(rawFrames, id);
      frame.candidates = intersect(
        frame.candidates,
        firstIdArray(
          event.candidates,
          event.receivers,
          event.instances,
          event.receiverCandidates,
          event.possibleReceivers,
        ),
      );
      continue;
    }

    if (type === "channel" || type === "queue") {
      const id = asId(event.channel) ?? asId(event.channelId) ?? asId(event.queue) ?? asId(event.id);
      if (id === null) continue;
      const channel = ensureRawChannel(rawChannels, id);
      const frameId = asId(event.frame) ?? asId(event.frameId);
      if (frameId !== null) {
        channel.frameId = frameId;
        ensureRawFrame(rawFrames, frameId);
      }
      channel.candidates = intersect(
        channel.candidates,
        firstIdArray(
          event.candidates,
          event.receivers,
          event.instances,
          event.receiverCandidates,
          event.possibleReceivers,
        ),
      );
      continue;
    }

    if (type === "mutex" || type === "mutex-group" || type === "group") {
      const id = asId(event.group) ?? asId(event.groupId) ?? asId(event.id);
      if (id === null) continue;
      rawGroups.push({
        id,
        members: firstIdArray(event.members, event.channels, event.frames) ?? [],
        atomic: event.atomic !== false,
        required: asBoolean(event.required) ?? undefined,
        optional: asBoolean(event.optional) ?? undefined,
      });
    }
  }

  for (const spec of rawRevokes) {
    const target = asId(spec.target);
    const targetIsGrant = target !== null && grants.some((grant) => grant.id === target);
    if (target !== null && !targetIsGrant && spec.grantee === undefined) {
      spec.grantee = target;
    }
    if (target !== null && targetIsGrant && spec.grantId === undefined) {
      spec.grantId = target;
    }
  }

  const revocations: NormalizedRevoke[] = rawRevokes.map((spec, index) => ({
    id: asId(spec.id) ?? `@revoke:${index}`,
    grantId:
      asId(spec.grantId) ??
      asId(spec.grant) ??
      asId(spec.permit) ??
      asId(spec.permitId) ??
      asId(spec.delegation) ??
      asId(spec.delegationId),
    grantee:
      asId(spec.grantee) ??
      asId(spec.toInstance) ??
      asId(spec.to) ??
      asId(spec.instance) ??
      asId(spec.receiver),
    grantor: asId(spec.grantor) ?? asId(spec.from),
    audience: firstIdArray(spec.audience, spec.scope) ?? [],
    scope: asId(spec.scope),
    generation: asFiniteNumber(spec.generation ?? spec.gen ?? spec.version),
    seq: asFiniteNumber(spec.seq ?? spec.at),
    all: spec.all === true,
    targetGrantIds: new Set<StableId>(),
  }));
  applyRevocations(grants, revocations);

  for (const channel of rawChannels.values()) {
    if (channel.frameId !== null) {
      const frame = ensureRawFrame(rawFrames, channel.frameId);
      addDistinct(frame.channels, [channel.id]);
    }
  }

  for (const message of rawMessages.values()) {
    for (const channelId of message.channelIds) {
      const channel = ensureRawChannel(rawChannels, channelId);
      if (message.frameId !== null) {
        channel.frameId = message.frameId;
        ensureRawFrame(rawFrames, message.frameId);
      }
    }
  }

  for (const message of rawMessages.values()) {
    if (message.frameId === null && message.channelIds.length > 0) {
      const firstChannel = rawChannels.get(message.channelIds[0] as StableId);
      if (firstChannel?.frameId != null) message.frameId = firstChannel.frameId;
    }
    if (message.frameId === null) {
      message.frameId =
        message.channelIds.length === 1
          ? (message.channelIds[0] as StableId)
          : `@frame:${message.id}`;
    }
  }

  for (const channel of rawChannels.values()) {
    if (channel.frameId === null) {
      const message = [...rawMessages.values()].find((entry) =>
        entry.channelIds.includes(channel.id),
      );
      channel.frameId =
        message?.frameId ??
        channel.id;
    }
    const frame = ensureRawFrame(rawFrames, channel.frameId);
    addDistinct(frame.channels, [channel.id]);
  }

  for (const message of rawMessages.values()) {
    const frame = ensureRawFrame(rawFrames, message.frameId as StableId);
    for (const channelId of message.channelIds) {
      addDistinct(frame.channels, [channelId]);
      ensureRawChannel(rawChannels, channelId).frameId = message.frameId;
    }
  }

  const allCandidateIds: StableId[] = [];
  for (const frame of rawFrames.values()) {
    if (frame.candidates !== null) allCandidateIds.push(...frame.candidates);
  }
  for (const channel of rawChannels.values()) {
    if (channel.candidates !== null) allCandidateIds.push(...channel.candidates);
  }
  for (const message of rawMessages.values()) {
    if (message.candidates !== null) allCandidateIds.push(...message.candidates);
  }
  for (const id of allCandidateIds) inferInstance(instances, id);

  const instanceOrder = [...instances.keys()].sort(compareStableIds);

  const protectedInfo = protectedMessageIds(problem);
  const requiredFrames = new Set<StableId>();
  for (const id of problem.requiredFrames ?? []) {
    const value = asId(id);
    if (value !== null) requiredFrames.add(value);
  }
  const requiredChannels = new Set<StableId>();
  for (const id of problem.requiredChannels ?? []) {
    const value = asId(id);
    if (value !== null) requiredChannels.add(value);
  }

  const channels = new Map<StableId, NormalizedChannel>();
  for (const raw of rawChannels.values()) {
    const parentFrame = raw.frameId === null ? undefined : rawFrames.get(raw.frameId);
    const optional =
      raw.optional ??
      (raw.required === false || parentFrame?.optional === true ? true : false);
    const inheritedRequired =
      raw.required ??
      (parentFrame?.required === false ? false : null);
    const required = raw.stop
      ? false
      : (inheritedRequired ?? (requiredChannels.has(raw.id) || !optional));
    channels.set(raw.id, {
      id: raw.id,
      frameId: raw.frameId as StableId,
      candidateConstraints: raw.candidates === null ? [] : [raw.candidates],
      optional,
      required: raw.stop ? false : required,
      stop: raw.stop,
    });
  }

  const frameChannelMap = new Map<StableId, StableId[]>();
  for (const channel of channels.values()) {
    const list = frameChannelMap.get(channel.frameId) ?? [];
    list.push(channel.id);
    frameChannelMap.set(channel.frameId, list);
  }

  const frames = new Map<StableId, NormalizedFrame>();
  for (const raw of rawFrames.values()) {
    const channelIds = uniqueSorted(
      frameChannelMap.get(raw.id) ?? raw.channels,
    );
    const constraints: StableId[][] = [];
    if (raw.candidates !== null) constraints.push(raw.candidates);
    for (const channelId of channelIds) {
      const channel = channels.get(channelId);
      if (channel !== undefined) constraints.push(...channel.candidateConstraints);
    }
    for (const message of rawMessages.values()) {
      if (message.frameId !== raw.id || message.candidates === null) continue;
      constraints.push(message.candidates);
    }
    let domain = instanceOrder;
    for (const constraint of constraints) {
      const allowed = new Set(constraint);
      domain = domain.filter((id) => allowed.has(id));
    }
    const channelSpecs = channelIds.map((id) => channels.get(id)).filter(
      (entry): entry is NormalizedChannel => entry !== undefined,
    );
    const frameOptional =
      raw.optional === true ||
      (channelSpecs.length > 0 && channelSpecs.every((channel) => channel.optional));
    const frameRequired =
      raw.required === true ||
      requiredFrames.has(raw.id) ||
      (raw.required === null && !frameOptional);
    frames.set(raw.id, {
      id: raw.id,
      channelIds,
      candidateConstraints: constraints,
      domain: uniqueSorted(domain),
      required: frameRequired,
      explicitChannels: raw.channels.length > 0 || channelIds.length > 0,
    });
  }

  const messages: NormalizedMessage[] = [];
  for (const raw of rawMessages.values()) {
    const frameId = raw.frameId as StableId;
    const frame = frames.get(frameId);
    if (frame === undefined) {
      throw new MigrationInputError(`Message ${raw.id} refers to unknown frame ${frameId}`);
    }
    const visibility = raw.visibility ?? "public";
    const audience = raw.audience.length > 0
      ? raw.audience
      : raw.sender !== null && visibility === "private"
        ? [raw.sender]
        : [];
    const listedProtected = protectedInfo.explicit?.has(raw.id) ?? false;
    const defaultConfirmed =
      protectedInfo.explicit === null && raw.confirmed && visibility === "public";
    const protectedMessage =
      visibility === "public" &&
      (raw.protected === true ||
        protectedInfo.preserveAll ||
        listedProtected ||
        (raw.protected !== false && defaultConfirmed));
    messages.push({
      id: raw.id,
      frameId,
      channelIds: raw.channelIds.length > 0 ? raw.channelIds : frame.channelIds,
      visibility,
      audience: uniqueSorted(audience),
      authorizedInstances: uniqueSorted(raw.authorizedInstances),
      sender: raw.sender,
      scope: raw.scope,
      seq: raw.seq ?? Number.POSITIVE_INFINITY,
      confirmed: raw.confirmed,
      protected: protectedMessage,
      optional: raw.optional === true,
    });
  }
  messages.sort((a, b) => compareStableIds(a.id, b.id));

  const protectedPublicMessages = new Set<StableId>();
  for (const message of messages) {
    if (message.protected) protectedPublicMessages.add(message.id);
  }

  const groups: NormalizedGroup[] = [];
  for (const raw of rawGroups) {
    const explicitFrames = new Set<StableId>();
    for (const frameId of [...(raw.frames ?? []), ...(raw.members ?? [])]) {
      const id = asId(frameId);
      if (id !== null && rawFrames.has(id)) explicitFrames.add(id);
    }
    const explicitChannels = new Set<StableId>();
    for (const channelId of [...(raw.channels ?? []), ...(raw.members ?? [])]) {
      const id = asId(channelId);
      if (id !== null && !rawFrames.has(id)) {
        explicitChannels.add(id);
        const channel = ensureRawChannel(rawChannels, id);
        if (channel.frameId === null) channel.frameId = id;
      }
    }
    const frameIds = new Set<StableId>(explicitFrames);
    for (const channelId of explicitChannels) {
      const channel = rawChannels.get(channelId);
      if (channel?.frameId != null) frameIds.add(channel.frameId);
      if (channel?.frameId != null) ensureRawFrame(rawFrames, channel.frameId);
    }
    const allChannelIds = new Set<StableId>(explicitChannels);
    for (const frameId of frameIds) {
      const frame = rawFrames.get(frameId);
      for (const channelId of frame?.channels ?? []) allChannelIds.add(channelId);
    }
    const groupRequired = raw.required === true;
    groups.push({
      id: raw.id,
      frameIds: uniqueSorted(frameIds),
      channelIds: uniqueSorted(allChannelIds),
      atomic: raw.atomic !== false,
      required: groupRequired,
    });
  }

  // Groups can name channels that were created only by a group member. Add
  // those channels and synthetic frames before returning.
  for (const group of groups) {
    for (const channelId of group.channelIds) {
      if (channels.has(channelId)) continue;
      const rawChannel = ensureRawChannel(rawChannels, channelId);
      if (rawChannel.frameId === null) rawChannel.frameId = channelId;
      const frameId = rawChannel.frameId ?? channelId;
      channels.set(channelId, {
        id: channelId,
        frameId,
        candidateConstraints: rawChannel.candidates === null ? [] : [rawChannel.candidates],
        optional: rawChannel.optional ?? true,
        required: false,
        stop: rawChannel.stop,
      });
      if (!frames.has(frameId)) {
        frames.set(frameId, {
          id: frameId,
          channelIds: [channelId],
          candidateConstraints: [],
          domain: instanceOrder,
          required: false,
          explicitChannels: true,
        });
      } else {
        const frame = frames.get(frameId) as NormalizedFrame;
        addDistinct(frame.channelIds, [channelId]);
      }
    }
  }

  const channelOrder = [...channels.keys()].sort(compareStableIds);
  const frameOrder = [...frames.keys()].sort(compareStableIds);

  return {
    instances,
    instanceOrder,
    frames,
    frameOrder,
    channels,
    channelOrder,
    messages,
    grants,
    revocations,
    groups,
    protectedPublicMessages,
  };
}

export function messagesForFrame(
  model: NormalizedProblem,
  frameId: StableId,
): NormalizedMessage[] {
  return model.messages.filter((message) => message.frameId === frameId);
}

export function channelsForFrame(
  model: NormalizedProblem,
  frameId: StableId,
): NormalizedChannel[] {
  const frame = model.frames.get(frameId);
  if (frame === undefined) return [];
  return frame.channelIds
    .map((id) => model.channels.get(id))
    .filter((channel): channel is NormalizedChannel => channel !== undefined);
}

export function groupMustContinue(
  model: NormalizedProblem,
  group: NormalizedGroup,
): boolean {
  if (group.required) return true;
  for (const frameId of group.frameIds) {
    const frame = model.frames.get(frameId);
    if (frame?.required === true) return true;
    if (messagesForFrame(model, frameId).some((message) => message.protected)) return true;
  }
  for (const channelId of group.channelIds) {
    const channel = model.channels.get(channelId);
    if (channel?.required === true) return true;
  }
  return false;
}

export function frameMustContinue(
  model: NormalizedProblem,
  frameId: StableId,
): boolean {
  const frame = model.frames.get(frameId);
  if (frame === undefined) return false;
  if (frame.required) return true;
  return messagesForFrame(model, frameId).some((message) => message.protected);
}
