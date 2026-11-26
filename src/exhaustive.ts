import {
  analyzeAcceptance,
  auditBridges,
  decideNormalizedCoalition,
  decideNormalizedDevice,
  normalizedDeviceForId,
} from "./analyze.ts";
import { canonicalDigest } from "./canonical.ts";
import {
  DEVICE_DOMAIN_END,
  rangeContains,
  toDeviceId,
  type ScalarDeviceId,
} from "./interval.ts";
import { normalizePolicy } from "./normalize.ts";
import type {
  AcceptancePolicy,
  AuthorizationClass,
  CoalitionDecision,
  DomainEvaluation,
  DomainEvaluationOptions,
  IdDecision,
  PartitionCell,
} from "./types.ts";

export interface ExhaustiveOptions extends DomainEvaluationOptions {
  readonly coalitionMaxSize?: number;
  readonly includeCoalitions?: boolean;
}

export interface ExhaustiveCoalitionCase {
  readonly id: string;
  readonly members: readonly string[];
  readonly decision: CoalitionDecision;
}

export interface ExhaustiveResult {
  readonly ok: boolean;
  readonly domain: DomainEvaluation;
  readonly coalitions: readonly ExhaustiveCoalitionCase[];
  readonly escalationCases: readonly ExhaustiveCoalitionCase[];
}

function cellFor(partitions: readonly PartitionCell[], id: bigint): PartitionCell | undefined {
  let low = 0;
  let high = partitions.length - 1;
  while (low <= high) {
    const middle = (low + high) >>> 1;
    const cell = partitions[middle]!;
    const start = BigInt(cell.start);
    const end = BigInt(cell.end);
    if (id < start) high = middle - 1;
    else if (id > end) low = middle + 1;
    else return cell;
  }
  return undefined;
}

function combinations<T>(items: readonly T[], size: number): T[][] {
  if (size <= 0 || size > items.length) return [];
  if (size === 1) return items.map((item) => [item]);
  const result: T[][] = [];
  const walk = (start: number, current: T[]): void => {
    if (current.length === size) {
      result.push([...current]);
      return;
    }
    for (let index = start; index <= items.length - (size - current.length); index += 1) {
      current.push(items[index]!);
      walk(index + 1, current);
      current.pop();
    }
  };
  walk(0, []);
  return result;
}

export function evaluateDomain(
  policy: AcceptancePolicy,
  options: DomainEvaluationOptions = {},
): DomainEvaluation {
  const model = normalizePolicy(policy);
  const audits = auditBridges(model, policy);
  const start = options.start === undefined ? 0n : toDeviceId(options.start);
  const defaultEnd = start + 31n > (DEVICE_DOMAIN_END - 1n) ? DEVICE_DOMAIN_END - 1n : start + 31n;
  const end = options.end === undefined ? defaultEnd : toDeviceId(options.end);
  if (start > end) throw new RangeError("domain start exceeds end");
  const limit = options.limit ?? policy.exhaustiveLimit ?? 65_536;
  const size = Number(end - start + 1n);
  if (!Number.isSafeInteger(size) || size > limit) {
    throw new RangeError(
      `refusing to enumerate ${size} ids; raise the explicit limit only for a small device domain`,
    );
  }
  const partitions = analyzeAcceptance(policy).partitions;
  const decisions: IdDecision[] = [];
  const summary: Record<AuthorizationClass, number> = {
    authorized: 0,
    bridged: 0,
    partial: 0,
    revoked: 0,
    denied: 0,
  };
  const partitionMismatches: {
    id: string;
    symbol: AuthorizationClass;
    expected: AuthorizationClass;
  }[] = [];

  for (let id = start; id <= end; id += 1n) {
    const device = normalizedDeviceForId(model, id);
    const decision = decideNormalizedDevice(model, audits, device);
    summary[decision.authorization] += 1;
    decisions.push({
      id: id.toString(10),
      revoked: decision.revoked,
      direct: decision.direct,
      bridged: decision.bridged,
      eligible: decision.eligible,
      authorization: decision.authorization,
    });
    if (options.comparePartitions !== false) {
      const cell = cellFor(partitions, id);
      if (cell !== undefined && cell.authorization !== decision.authorization) {
        partitionMismatches.push({
          id: id.toString(10),
          symbol: cell.authorization,
          expected: decision.authorization,
        });
      }
    }
  }

  return {
    start: start.toString(10),
    end: end.toString(10),
    size,
    ...(options.includeIds === false ? {} : { ids: decisions }),
    summary,
    partitionMismatches,
  };
}

export function exhaustiveCheck(
  policy: AcceptancePolicy,
  options: ExhaustiveOptions = {},
): ExhaustiveResult {
  const domain = evaluateDomain(policy, options);
  const model = normalizePolicy(policy);
  const audits = auditBridges(model, policy);
  const start = BigInt(domain.start);
  const end = BigInt(domain.end);
  const maxSize =
    options.coalitionMaxSize ??
    Math.min(6, Math.max(2, model.message.threshold));
  const revokedIds: bigint[] = [];
  for (let id = start; id <= end; id += 1n) {
    if (rangeContains(model.revocation, id)) revokedIds.push(id);
  }

  const cases: ExhaustiveCoalitionCase[] = [];
  if (options.includeCoalitions !== false) {
    for (let size = 1; size <= Math.min(maxSize, revokedIds.length); size += 1) {
      for (const group of combinations(revokedIds, size)) {
        const members = group.map((id) => id.toString(10));
        const decision = decideNormalizedCoalition(
          model,
          audits,
          `exhaustive:${size}:${canonicalDigest(members).slice(0, 12)}`,
          members,
        );
        cases.push({ id: decision.coalitionId, members, decision });
      }
    }
  }
  const escalationCases = cases.filter(
    (entry) => entry.decision.revokedRisk && entry.decision.newReadRights,
  );
  return {
    ok: domain.partitionMismatches.length === 0,
    domain,
    coalitions: cases,
    escalationCases,
  };
}

export function evaluateIdDecision(
  policy: AcceptancePolicy,
  id: ScalarDeviceId,
): IdDecision {
  const model = normalizePolicy(policy);
  const audits = auditBridges(model, policy);
  const value = toDeviceId(id);
  const decision = decideNormalizedDevice(model, audits, normalizedDeviceForId(model, value));
  return {
    id: value.toString(10),
    revoked: decision.revoked,
    direct: decision.direct,
    bridged: decision.bridged,
    eligible: decision.eligible,
    authorization: decision.authorization,
  };
}
