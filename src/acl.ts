import { extractBdSeq, isRebirthCommand } from "./payload.ts";
import {
  hasMqttWildcard,
  parseSparkplugTopic,
  parseTopicFilter,
  SPARKPLUG_NAMESPACE,
  type SparkplugTopic,
} from "./topic.ts";
import {
  ACL_REASONS,
  type AclContext,
  type AclDecision,
  type AclReasonCode,
  type InvalidPrincipal,
  type NormalizedPrincipal,
  type SparkplugAclOptions,
} from "./types.ts";

const DEFAULT_MAX_REBIRTHS_PER_MINUTE = 3;
const DEFAULT_REBIRTH_WINDOW_MS = 60_000;

const APPLICATION_ROLES = new Set([
  "application",
  "app",
  "host",
  "host-application",
  "main-application",
  "main-app",
  "primary-host",
  "primary-application",
]);

const EDGE_ROLES = new Set([
  "edge",
  "edge-node",
  "node",
]);

const EDGE_PUBLISH_TYPES = new Set(["NBIRTH", "NDATA", "NDEATH"]);
const EDGE_SUBSCRIBE_TYPES = new Set(["NBIRTH", "NDATA", "NDEATH"]);
const COMMAND_TYPES = new Set(["NCMD", "DCMD"]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function firstNonEmptyString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string" && value.trim().length > 0) {
      return value;
    }
  }
  return undefined;
}

function normalizeRole(value: string | undefined): string | undefined {
  if (value === undefined) return undefined;
  return value.trim().toLowerCase().replace(/_/g, "-");
}

/**
 * Normalize a broker-provided principal into the small set of roles the ACL
 * understands. Unknown or ambiguous roles are rejected rather than guessed.
 */
export function normalizePrincipal(input: unknown): NormalizedPrincipal | InvalidPrincipal {
  if (!isRecord(input)) {
    return { ok: false, reason: "principal must be an object" };
  }

  const role = normalizeRole(
    firstNonEmptyString(input.kind, input.type, input.role),
  );
  if (role === undefined) {
    return { ok: false, reason: "principal is missing kind/type/role" };
  }

  const clientId = firstNonEmptyString(
    input.clientId,
    input.clientid,
    input.username,
    input.nodeId,
    input.node_id,
  );
  if (clientId === undefined) {
    return { ok: false, reason: "principal is missing a clientId" };
  }

  if (APPLICATION_ROLES.has(role)) {
    return { ok: true, kind: "application", clientId };
  }

  if (EDGE_ROLES.has(role)) {
    const groupId = firstNonEmptyString(input.groupId, input.group, input.group_id);
    if (groupId === undefined) {
      return { ok: false, reason: "edge principal is missing a groupId" };
    }

    const declaredNodeId = firstNonEmptyString(input.nodeId, input.node_id);
    if (declaredNodeId !== undefined && declaredNodeId !== clientId) {
      return {
        ok: false,
        reason: "edge clientId must equal the Sparkplug node id",
      };
    }

    return { ok: true, kind: "edge-node", clientId, groupId };
  }

  return { ok: false, reason: `unsupported principal role: ${role}` };
}

function allow(): AclDecision {
  return {
    allowed: true,
    code: ACL_REASONS.ALLOWED,
    reason: "publish/subscription is permitted",
  };
}

function deny(code: AclReasonCode, reason: string, detail?: string): AclDecision {
  return detail === undefined
    ? { allowed: false, code, reason }
    : { allowed: false, code, reason, detail };
}

function birthKey(groupId: string, nodeId: string): string {
  return `${groupId}\u0000${nodeId}`;
}

function extractSequence(payload: unknown):
  | { ok: true; bdSeq: bigint }
  | { ok: false; decision: AclDecision } {
  const result = extractBdSeq(payload);
  if (result.ok) return result;

  if (result.reason === "BIRTH_SEQUENCE_MISSING") {
    return {
      ok: false,
      decision: deny(
        ACL_REASONS.BIRTH_SEQUENCE_MISSING,
        "payload does not contain the required bdSeq metric",
      ),
    };
  }

  if (result.reason === "UNPARSEABLE_PAYLOAD") {
    return {
      ok: false,
      decision: deny(
        ACL_REASONS.BIRTH_SEQUENCE_INVALID,
        "payload could not be decoded to read bdSeq",
      ),
    };
  }

  return {
    ok: false,
    decision: deny(
      ACL_REASONS.BIRTH_SEQUENCE_INVALID,
      "payload contains an invalid bdSeq metric",
    ),
  };
}

function isCommandPayload(payload: unknown): boolean {
  try {
    return isRebirthCommand(payload);
  } catch {
    return false;
  }
}

function topicShapeDecision(topic: SparkplugTopic): AclDecision | null {
  if (topic.messageType === "NBIRTH" || topic.messageType === "NDEATH") {
    if (topic.deviceId !== undefined) {
      return deny(
        ACL_REASONS.INVALID_TOPIC_SHAPE,
        `${topic.messageType} topics may only address an edge node`,
      );
    }
    return null;
  }

  if (topic.messageType === "NDATA") {
    return null;
  }

  if (topic.messageType === "NCMD") {
    if (topic.deviceId !== undefined) {
      return deny(
        ACL_REASONS.INVALID_TOPIC_SHAPE,
        "NCMD topics may only address an edge node",
      );
    }
    return null;
  }

  if (topic.messageType === "DCMD") {
    if (topic.deviceId === undefined) {
      return deny(
        ACL_REASONS.INVALID_TOPIC_SHAPE,
        "DCMD topics must include a device id",
      );
    }
    return null;
  }

  return null;
}

/**
 * Stateful Sparkplug B publish/subscribe authorization engine.
 *
 * NBIRTH authorization records the node's latest bdSeq. NDEATH authorization
 * only succeeds when that exact bdSeq is presented. Accepted Rebirth NCMD
 * publishes consume a per-node sliding-window quota.
 */
export class SparkplugAcl {
  private readonly maxRebirths: number;
  private readonly rebirthWindowMs: number;
  private readonly clock: (() => number) | undefined;
  private readonly latestBirths = new Map<string, bigint>();
  private readonly rebirthTimestamps = new Map<string, number[]>();

  constructor(options: SparkplugAclOptions = {}) {
    const configuredLimit =
      options.maxRebirthCommandsPerMinute ?? options.maxRebirthPerMinute;
    const limit =
      configuredLimit === undefined
        ? DEFAULT_MAX_REBIRTHS_PER_MINUTE
        : configuredLimit;
    const windowMs =
      options.rebirthWindowMs === undefined
        ? DEFAULT_REBIRTH_WINDOW_MS
        : options.rebirthWindowMs;

    if (!Number.isInteger(limit) || limit < 0) {
      throw new TypeError("maxRebirthCommandsPerMinute must be a non-negative integer");
    }
    if (!Number.isFinite(windowMs) || windowMs <= 0) {
      throw new TypeError("rebirthWindowMs must be a positive finite number");
    }
    if (typeof options.now !== "function" && options.now !== undefined) {
      throw new TypeError("now must be a function");
    }

    this.maxRebirths = limit;
    this.rebirthWindowMs = windowMs;
    this.clock = options.now;
  }

  /**
   * Return a detailed decision. Accepted NBIRTH/NDEATH/Rebirth messages mutate
   * internal state, so call this once per broker authorization request.
   */
  evaluatePublish(
    principal: unknown,
    topic: string,
    payload?: unknown,
    context: AclContext = {},
  ): AclDecision {
    const normalized = normalizePrincipal(principal);
    if (!normalized.ok) {
      return deny(ACL_REASONS.INVALID_PRINCIPAL, normalized.reason);
    }

    const parsedTopic = parseSparkplugTopic(topic);
    if (parsedTopic === null) {
      return deny(
        ACL_REASONS.INVALID_TOPIC,
        "topic is not a valid concrete Sparkplug B topic",
      );
    }

    if (normalized.kind === "application") {
      return this.evaluateApplicationPublish(
        normalized.clientId,
        parsedTopic,
        payload,
        context,
      );
    }

    return this.evaluateEdgePublish(normalized, parsedTopic, payload);
  }

  /** Boolean-friendly broker hook for publish authorization. */
  authorizePublish(
    principal: unknown,
    topic: string,
    payload?: unknown,
    context: AclContext = {},
  ): boolean {
    return this.evaluatePublish(principal, topic, payload, context).allowed;
  }

  /** Alias for `authorizePublish`. */
  canPublish(
    principal: unknown,
    topic: string,
    payload?: unknown,
    context: AclContext = {},
  ): boolean {
    return this.authorizePublish(principal, topic, payload, context);
  }

  /**
   * Return a detailed subscription decision. Edge nodes cannot subscribe to
   * command topics, and wildcard filters are denied for edge nodes so a filter
   * cannot be used to reach NCMD/DCMD topics indirectly.
   */
  evaluateSubscribe(
    principal: unknown,
    topicFilter: string,
    _context: AclContext = {},
  ): AclDecision {
    const normalized = normalizePrincipal(principal);
    if (!normalized.ok) {
      return deny(ACL_REASONS.INVALID_PRINCIPAL, normalized.reason);
    }

    const filter = parseTopicFilter(topicFilter);
    if (filter === null) {
      return deny(
        ACL_REASONS.INVALID_TOPIC_FILTER,
        "topic filter is not valid MQTT syntax",
      );
    }

    if (filter.levels[0] !== SPARKPLUG_NAMESPACE || filter.levels.length < 2) {
      return deny(
        ACL_REASONS.UNSUPPORTED_TOPIC,
        "subscription is outside the Sparkplug B namespace",
      );
    }

    if (normalized.kind === "application") return allow();
    return this.evaluateEdgeSubscribe(normalized, topicFilter, filter.concrete);
  }

  /** Boolean-friendly broker hook for subscription authorization. */
  authorizeSubscribe(
    principal: unknown,
    topicFilter: string,
    context: AclContext = {},
  ): boolean {
    return this.evaluateSubscribe(principal, topicFilter, context).allowed;
  }

  /** Alias for `authorizeSubscribe`. */
  canSubscribe(
    principal: unknown,
    topicFilter: string,
    context: AclContext = {},
  ): boolean {
    return this.authorizeSubscribe(principal, topicFilter, context);
  }

  /** Latest accepted NBIRTH bdSeq for a group/node, or undefined. */
  getLatestBirthSequence(groupId: string, nodeId: string): bigint | undefined {
    return this.latestBirths.get(birthKey(groupId, nodeId));
  }

  /** Clear birth state and Rebirth quotas for one node. */
  clearNode(groupId: string, nodeId: string): void {
    const key = birthKey(groupId, nodeId);
    this.latestBirths.delete(key);
    this.rebirthTimestamps.delete(key);
  }

  /** Clear all retained state. */
  reset(): void {
    this.latestBirths.clear();
    this.rebirthTimestamps.clear();
  }

  private currentTime(context: AclContext): number {
    const supplied = context.now;
    if (typeof supplied === "number" && Number.isFinite(supplied)) return supplied;
    const fromClock = this.clock === undefined ? Date.now() : this.clock();
    return Number.isFinite(fromClock) ? fromClock : 0;
  }

  private evaluateApplicationPublish(
    clientId: string,
    topic: SparkplugTopic,
    payload: unknown,
    context: AclContext,
  ): AclDecision {
    if (topic.messageType === "STATE") {
      return allow();
    }

    if (!COMMAND_TYPES.has(topic.messageType)) {
      return deny(
        ACL_REASONS.APPLICATION_PUBLISH_FORBIDDEN,
        "application credentials may only publish command or STATE topics",
        `${clientId} attempted ${topic.messageType}`,
      );
    }

    const shape = topicShapeDecision(topic);
    if (shape !== null) return shape;

    if (
      topic.messageType === "NCMD" &&
      isCommandPayload(payload) &&
      !this.consumeRebirthQuota(topic.groupId, topic.nodeId, this.currentTime(context))
    ) {
      return deny(
        ACL_REASONS.REBIRTH_RATE_LIMITED,
        "Rebirth command rate exceeded for this edge node",
        `limit=${this.maxRebirths} per ${this.rebirthWindowMs}ms`,
      );
    }

    return allow();
  }

  private evaluateEdgePublish(
    principal: Extract<NormalizedPrincipal, { kind: "edge-node" }>,
    topic: SparkplugTopic,
    payload: unknown,
  ): AclDecision {
    if (COMMAND_TYPES.has(topic.messageType)) {
      return deny(
        ACL_REASONS.COMMAND_PUBLISH_FORBIDDEN,
        "edge nodes may not publish Sparkplug command topics",
        `${principal.clientId} attempted ${topic.messageType}`,
      );
    }

    if (!EDGE_PUBLISH_TYPES.has(topic.messageType)) {
      return deny(
        ACL_REASONS.EDGE_TOPIC_FORBIDDEN,
        "edge nodes may only publish NBIRTH, NDATA, and NDEATH",
        `attempted ${topic.messageType}`,
      );
    }

    if (topic.groupId !== principal.groupId) {
      return deny(
        ACL_REASONS.EDGE_GROUP_MISMATCH,
        "edge node may only publish into its own group",
      );
    }

    if (topic.nodeId !== principal.clientId) {
      return deny(
        ACL_REASONS.EDGE_NODE_MISMATCH,
        "topic node id must equal the authenticated client id",
      );
    }

    const shape = topicShapeDecision(topic);
    if (shape !== null) return shape;

    const key = birthKey(topic.groupId, topic.nodeId);

    if (topic.messageType === "NBIRTH") {
      const sequence = extractSequence(payload);
      if (!sequence.ok) return sequence.decision;
      this.latestBirths.set(key, sequence.bdSeq);
      return allow();
    }

    if (topic.messageType === "NDEATH") {
      const sequence = extractSequence(payload);
      if (!sequence.ok) return sequence.decision;

      const latest = this.latestBirths.get(key);
      if (latest === undefined) {
        return deny(
          ACL_REASONS.DEATH_WITHOUT_BIRTH,
          "NDEATH cannot be accepted before an NBIRTH for this node",
        );
      }
      if (latest !== sequence.bdSeq) {
        return deny(
          ACL_REASONS.DEATH_SEQUENCE_MISMATCH,
          "NDEATH bdSeq does not match the node's latest NBIRTH bdSeq",
          `expected=${latest} received=${sequence.bdSeq}`,
        );
      }
      return allow();
    }

    return allow();
  }

  private evaluateEdgeSubscribe(
    principal: Extract<NormalizedPrincipal, { kind: "edge-node" }>,
    topicFilter: string,
    concrete: boolean,
  ): AclDecision {
    if (!concrete || hasMqttWildcard(topicFilter)) {
      return deny(
        ACL_REASONS.WILDCARD_SUBSCRIPTION_FORBIDDEN,
        "edge nodes may only subscribe to concrete, node-scoped Sparkplug topics",
      );
    }

    const topic = parseSparkplugTopic(topicFilter);
    if (topic === null) {
      return deny(
        ACL_REASONS.INVALID_TOPIC_FILTER,
        "edge subscription is not a valid Sparkplug B topic",
      );
    }

    if (COMMAND_TYPES.has(topic.messageType)) {
      return deny(
        ACL_REASONS.COMMAND_SUBSCRIPTION_FORBIDDEN,
        "edge nodes may not subscribe to Sparkplug command topics",
      );
    }

    if (!EDGE_SUBSCRIBE_TYPES.has(topic.messageType)) {
      return deny(
        ACL_REASONS.EDGE_TOPIC_FORBIDDEN,
        "edge nodes may only subscribe to NBIRTH, NDATA, and NDEATH",
      );
    }

    if (topic.groupId !== principal.groupId) {
      return deny(
        ACL_REASONS.EDGE_GROUP_MISMATCH,
        "edge node may only subscribe within its own group",
      );
    }

    if (topic.nodeId !== principal.clientId) {
      return deny(
        ACL_REASONS.EDGE_NODE_MISMATCH,
        "subscription node id must equal the authenticated client id",
      );
    }

    const shape = topicShapeDecision(topic);
    if (shape !== null) return shape;
    return allow();
  }

  private consumeRebirthQuota(
    groupId: string,
    nodeId: string,
    now: number,
  ): boolean {
    const key = birthKey(groupId, nodeId);
    const cutoff = now - this.rebirthWindowMs;
    const existing = this.rebirthTimestamps.get(key) ?? [];
    const recent = existing.filter((timestamp) => timestamp > cutoff);

    if (recent.length >= this.maxRebirths) {
      this.rebirthTimestamps.set(key, recent);
      return false;
    }

    recent.push(now);
    this.rebirthTimestamps.set(key, recent);
    return true;
  }
}

export function createSparkplugAcl(options: SparkplugAclOptions = {}): SparkplugAcl {
  return new SparkplugAcl(options);
}

export const createAcl = createSparkplugAcl;

export { SparkplugAcl as SparkplugAclEngine };
