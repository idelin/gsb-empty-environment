/**
 * Public types for the Sparkplug B ACL package.
 *
 * The broker is expected to authenticate the client first and then pass the
 * resulting principal to this package. The ACL never infers a role from a
 * topic or payload.
 */

export type PrincipalKind = "application" | "edge-node";

/** A trusted Sparkplug host application (for example, a SCADA primary host). */
export interface ApplicationPrincipal {
  kind: "application";
  clientId: string;
}

/**
 * An authenticated edge node. The Sparkplug edge node id is the MQTT client
 * identifier, so `clientId` is the authoritative node id.
 */
export interface EdgeNodePrincipal {
  kind: "edge-node";
  clientId: string;
  groupId: string;
}

export type AclPrincipal = ApplicationPrincipal | EdgeNodePrincipal;

/**
 * Loose input shape accepted at the API boundary. This makes integration with
 * brokers that expose `role`, `type`, `clientid`, or `group_id` straightforward.
 */
export interface PrincipalLike {
  kind?: string;
  type?: string;
  role?: string;
  clientId?: string;
  clientid?: string;
  username?: string;
  groupId?: string;
  group?: string;
  group_id?: string;
  nodeId?: string;
  node_id?: string;
}

export interface SparkplugAclOptions {
  /** Defaults to 3. */
  maxRebirthCommandsPerMinute?: number;
  /** Alias for `maxRebirthCommandsPerMinute`. Defaults to 3. */
  maxRebirthPerMinute?: number;
  /** Sliding window length. Defaults to 60_000 ms. */
  rebirthWindowMs?: number;
  /** Injectable clock, useful for deterministic tests. */
  now?: () => number;
}

export interface AclContext {
  /** Overrides the configured clock for one authorization call. */
  now?: number;
}

export interface AclDecision {
  allowed: boolean;
  code: AclReasonCode;
  reason: string;
  /** Present for denials that concern a specific message property. */
  detail?: string;
}

export const ACL_REASONS = {
  ALLOWED: "ALLOWED",
  INVALID_PRINCIPAL: "INVALID_PRINCIPAL",
  INVALID_TOPIC: "INVALID_TOPIC",
  INVALID_TOPIC_FILTER: "INVALID_TOPIC_FILTER",
  UNSUPPORTED_TOPIC: "UNSUPPORTED_TOPIC",
  COMMAND_PUBLISH_FORBIDDEN: "COMMAND_PUBLISH_FORBIDDEN",
  APPLICATION_PUBLISH_FORBIDDEN: "APPLICATION_PUBLISH_FORBIDDEN",
  EDGE_GROUP_MISMATCH: "EDGE_GROUP_MISMATCH",
  EDGE_NODE_MISMATCH: "EDGE_NODE_MISMATCH",
  EDGE_TOPIC_FORBIDDEN: "EDGE_TOPIC_FORBIDDEN",
  INVALID_TOPIC_SHAPE: "INVALID_TOPIC_SHAPE",
  BIRTH_SEQUENCE_MISSING: "BIRTH_SEQUENCE_MISSING",
  BIRTH_SEQUENCE_INVALID: "BIRTH_SEQUENCE_INVALID",
  DEATH_WITHOUT_BIRTH: "DEATH_WITHOUT_BIRTH",
  DEATH_SEQUENCE_MISMATCH: "DEATH_SEQUENCE_MISMATCH",
  REBIRTH_RATE_LIMITED: "REBIRTH_RATE_LIMITED",
  COMMAND_SUBSCRIPTION_FORBIDDEN: "COMMAND_SUBSCRIPTION_FORBIDDEN",
  WILDCARD_SUBSCRIPTION_FORBIDDEN: "WILDCARD_SUBSCRIPTION_FORBIDDEN",
} as const;

export type AclReasonCode = (typeof ACL_REASONS)[keyof typeof ACL_REASONS];

export interface NormalizedApplicationPrincipal {
  ok: true;
  kind: "application";
  clientId: string;
}

export interface NormalizedEdgePrincipal {
  ok: true;
  kind: "edge-node";
  clientId: string;
  groupId: string;
}

export interface InvalidPrincipal {
  ok: false;
  reason: string;
}

export type NormalizedPrincipal =
  | NormalizedApplicationPrincipal
  | NormalizedEdgePrincipal;

