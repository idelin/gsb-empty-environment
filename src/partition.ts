/**
 * A partition is the complete security context that may receive a broadcast.
 * It is intentionally small and serialisable so that it can travel with a
 * message across a process boundary.
 */
export interface PartitionIdentity {
  readonly topLevelRegistrableDomain: string;
  readonly initiatorOrigin: string;
  readonly crossSiteAncestor: boolean;
  readonly configVersion: string;
}

/**
 * Input accepted by makePartition. The aliases are useful at integration
 * boundaries where host APIs use different names for the same values.
 */
export interface PartitionInput {
  readonly topLevelRegistrableDomain?: string;
  readonly registrableDomain?: string;
  readonly topLevelDomain?: string;
  readonly topLevelSite?: string;
  readonly initiatorOrigin?: string;
  readonly initiatingOrigin?: string;
  readonly sourceOrigin?: string;
  readonly crossSiteAncestor?: boolean;
  readonly hasCrossSiteAncestor?: boolean;
  readonly crossSiteAncestorBit?: boolean;
  readonly configVersion?: string | number;
  readonly configurationVersion?: string | number;
}

export interface SubscriptionScope {
  /** If present, only messages sent by one of these origins are visible. */
  readonly visibleOrigins?: readonly string[];
  /** If present, only messages whose send snapshot is in this set are visible. */
  readonly visiblePartitions?: readonly PartitionIdentity[];
}

export interface SubscriptionScopeInput {
  readonly visibleOrigins?: readonly string[];
  readonly allowedSenderOrigins?: readonly string[];
  readonly visiblePartitions?: readonly PartitionInput[];
  readonly allowedPartitions?: readonly PartitionInput[];
}

function firstDefined<T>(...values: readonly (T | undefined)[]): T | undefined {
  for (const value of values) {
    if (value !== undefined) return value;
  }
  return undefined;
}

/** Normalises a registrable domain without consulting a public-suffix list. */
export function normalizeRegistrableDomain(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length === 0) {
    throw new TypeError("topLevelRegistrableDomain must not be empty");
  }

  let host = trimmed;
  if (trimmed.includes("://")) {
    host = new URL(trimmed).hostname;
  } else {
    host = trimmed.split("/")[0] ?? trimmed;
  }

  host = host.replace(/^\.+|\.+$/g, "").toLowerCase();
  if (host.length === 0) {
    throw new TypeError("topLevelRegistrableDomain must contain a host");
  }
  return host;
}

/** Normalises an origin to the serialisation used by the URL standard. */
export function normalizeOrigin(value: string): string {
  const trimmed = value.trim();
  if (trimmed === "null") return "null";
  try {
    return new URL(trimmed).origin;
  } catch {
    throw new TypeError(`initiatorOrigin is not an absolute URL origin: ${value}`);
  }
}

export function makePartition(input: PartitionInput): PartitionIdentity {
  const topLevelRegistrableDomain = firstDefined(
    input.topLevelRegistrableDomain,
    input.registrableDomain,
    input.topLevelDomain,
    input.topLevelSite,
  );
  const initiatorOrigin = firstDefined(
    input.initiatorOrigin,
    input.initiatingOrigin,
    input.sourceOrigin,
  );

  if (topLevelRegistrableDomain === undefined) {
    throw new TypeError("partition is missing topLevelRegistrableDomain");
  }
  if (initiatorOrigin === undefined) {
    throw new TypeError("partition is missing initiatorOrigin");
  }

  let crossSiteAncestor = firstDefined(
    input.crossSiteAncestor,
    input.hasCrossSiteAncestor,
    input.crossSiteAncestorBit,
  );
  if (crossSiteAncestor === undefined) crossSiteAncestor = false;

  const configVersion = firstDefined(
    input.configVersion,
    input.configurationVersion,
    "1",
  );

  return Object.freeze({
    topLevelRegistrableDomain: normalizeRegistrableDomain(topLevelRegistrableDomain),
    initiatorOrigin: normalizeOrigin(initiatorOrigin),
    crossSiteAncestor: Boolean(crossSiteAncestor),
    configVersion: String(configVersion),
  });
}

export function clonePartition(partition: PartitionIdentity): PartitionIdentity {
  return Object.freeze({ ...partition });
}

/** Stable key suitable for maps, dedupe indexes, and diagnostics. */
export function partitionKey(partition: PartitionIdentity): string {
  return [
    partition.topLevelRegistrableDomain,
    partition.initiatorOrigin,
    partition.crossSiteAncestor ? "1" : "0",
    partition.configVersion,
  ].join("\u001f");
}

export function partitionsEqual(
  left: PartitionIdentity,
  right: PartitionIdentity,
): boolean {
  return partitionKey(left) === partitionKey(right);
}

export function normalizeScope(
  input: SubscriptionScopeInput | undefined,
): SubscriptionScope {
  const visibleOrigins = firstDefined(
    input?.visibleOrigins,
    input?.allowedSenderOrigins,
  );
  const visiblePartitions = firstDefined(
    input?.visiblePartitions,
    input?.allowedPartitions,
  );

  return Object.freeze({
    ...(visibleOrigins === undefined
      ? {}
      : {
          visibleOrigins: Object.freeze(
            visibleOrigins.map((origin) => normalizeOrigin(origin)),
          ),
        }),
    ...(visiblePartitions === undefined
      ? {}
      : {
          visiblePartitions: Object.freeze(
            visiblePartitions.map((partition) => makePartition(partition)),
          ),
        }),
  });
}

export function scopeAllows(
  scope: SubscriptionScope,
  sendSnapshot: PartitionIdentity,
): boolean {
  if (
    scope.visibleOrigins !== undefined &&
    !scope.visibleOrigins.includes(sendSnapshot.initiatorOrigin)
  ) {
    return false;
  }

  if (
    scope.visiblePartitions !== undefined &&
    !scope.visiblePartitions.some((partition) =>
      partitionsEqual(partition, sendSnapshot),
    )
  ) {
    return false;
  }

  return true;
}
