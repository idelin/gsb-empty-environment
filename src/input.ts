import { parseHeaders, parsePath } from "./conditions.ts";
import { ExactFraction, parseRational } from "./fraction.ts";
import type {
  CompileInput,
  DelegateInput,
  DelegateTargetInput,
  HeaderCondition,
  MirrorInput,
  PathCondition,
  RationalInput,
  RouteRuleInput,
  RouteTableInput,
  WeightedTargetInput,
} from "./types.ts";

export interface PreparedTarget {
  service: string;
  fraction: ExactFraction;
}

export interface PreparedDelegate {
  tableId: string;
  fraction: ExactFraction;
}

export interface PreparedMirror {
  service: string;
  sampleRate: ExactFraction;
}

export interface PreparedRule {
  id: string;
  index: number;
  path: PathCondition;
  headers: HeaderCondition[];
  targets?: PreparedTarget[];
  delegates?: PreparedDelegate[];
  mirrors: PreparedMirror[];
}

export interface PreparedTable {
  id: string;
  name: string;
  rules: PreparedRule[];
}

export interface PreparedInput {
  rootTableId: string;
  tables: Map<string, PreparedTable>;
  tableIds: string[];
}

export class RouteCompileError extends Error {
  readonly code: string;
  readonly sourceRuleIds: string[];

  constructor(code: string, message: string, sourceRuleIds: readonly string[] = []) {
    super(message);
    this.name = "RouteCompileError";
    this.code = code;
    this.sourceRuleIds = [...sourceRuleIds];
  }
}

const TARGET_KEY_ALIASES = ["service", "cluster", "name", "destination"] as const;
const TABLE_KEY_ALIASES = ["table", "target", "to", "name"] as const;

function firstString(
  value: object,
  keys: readonly string[],
): string | undefined {
  const record = value as Record<string, unknown>;
  for (const key of keys) {
    const candidate = record[key];
    if (typeof candidate === "string" && candidate.trim().length > 0) {
      return candidate.trim();
    }
  }
  return undefined;
}

function interpretShareFractions(
  fractions: readonly ExactFraction[],
  label: string,
  sourceRuleIds: readonly string[],
): ExactFraction[] {
  if (fractions.length === 0) {
    throw new RouteCompileError("invalid-weight", `${label} list must not be empty`, sourceRuleIds);
  }

  const sum = fractions.reduce((total, fraction) => total.add(fraction), ExactFraction.zero());

  if (sum.equals(ExactFraction.from(100))) {
    return fractions.map((fraction) => fraction.divide(ExactFraction.from(100)));
  }

  if (sum.equals(ExactFraction.one())) {
    return [...fractions];
  }

  throw new RouteCompileError(
    "invalid-weight",
    `${label} must sum to exactly 100 (integer percentages) or 1 (fractions); got ${sum.toView().text}`,
    sourceRuleIds,
  );
}

function parseTargetList(
  input: readonly WeightedTargetInput[],
  sourceRuleIds: readonly string[],
): PreparedTarget[] {
  if (input.length === 0) {
    throw new RouteCompileError("invalid-rule", "targets must not be empty", sourceRuleIds);
  }

  const values: ExactFraction[] = [];
  const services: string[] = [];
  let missingWeightCount = 0;

  for (const target of input) {
    const service = firstString(target, TARGET_KEY_ALIASES);
    if (service === undefined) {
      throw new RouteCompileError(
        "invalid-rule",
        "each weighted target requires a service/cluster/name/destination",
        sourceRuleIds,
      );
    }
    services.push(service);
    if (target.weight === undefined) {
      missingWeightCount += 1;
      values.push(ExactFraction.zero());
    } else {
      values.push(parseRational(target.weight as RationalInput, `target "${service}" weight`));
    }
  }

  if (missingWeightCount > 0) {
    if (input.length !== 1 || missingWeightCount !== 1) {
      throw new RouteCompileError(
        "invalid-weight",
        "a target weight may only be omitted when there is exactly one target",
        sourceRuleIds,
      );
    }
    values[0] = ExactFraction.from(100);
  }

  const shares = interpretShareFractions(values, "target weights", sourceRuleIds);

  const merged = new Map<string, ExactFraction>();
  for (let index = 0; index < services.length; index += 1) {
    const service = services[index];
    const share = shares[index];
    const existing = merged.get(service);
    merged.set(service, existing === undefined ? share : existing.add(share));
  }

  return [...merged.entries()].map(([service, fraction]) => ({ service, fraction }));
}

function delegateRefs(input: DelegateInput, sourceRuleIds: readonly string[]): PreparedDelegate[] {
  const raw = Array.isArray(input) ? [...input] : [input];
  if (raw.length === 0) {
    throw new RouteCompileError("empty-delegation", "delegate list must not be empty", sourceRuleIds);
  }

  const values: ExactFraction[] = [];
  const tables: string[] = [];
  let missingWeightCount = 0;

  for (const item of raw) {
    let tableId: string | undefined;
    let weight: RationalInput | undefined;
    if (typeof item === "string") {
      tableId = item.trim();
    } else {
      tableId = firstString(item as DelegateTargetInput, TABLE_KEY_ALIASES);
      weight = (item as DelegateTargetInput).weight;
    }

    if (tableId === undefined || tableId.length === 0) {
      throw new RouteCompileError("invalid-rule", "delegate requires a table id", sourceRuleIds);
    }
    tables.push(tableId);

    if (weight === undefined) {
      missingWeightCount += 1;
      values.push(ExactFraction.zero());
    } else {
      values.push(parseRational(weight, `delegate "${tableId}" weight`));
    }
  }

  if (missingWeightCount > 0) {
    if (missingWeightCount !== raw.length) {
      throw new RouteCompileError(
        "invalid-weight",
        "delegate weights must be specified for all branches or for none",
        sourceRuleIds,
      );
    }
    const equalShare = ExactFraction.from(1, raw.length);
    for (let index = 0; index < values.length; index += 1) {
      values[index] = equalShare;
    }
  } else {
    const shares = interpretShareFractions(values, "delegate weights", sourceRuleIds);
    for (let index = 0; index < shares.length; index += 1) {
      values[index] = shares[index];
    }
  }

  return tables.map((tableId, index) => ({ tableId, fraction: values[index] }));
}

function parseMirrors(
  input: MirrorInput | readonly MirrorInput[] | undefined,
  extra: readonly MirrorInput[] | undefined,
  sourceRuleIds: readonly string[],
): PreparedMirror[] {
  const raw = [
    ...(input === undefined ? [] : Array.isArray(input) ? input : [input]),
    ...(extra ?? []),
  ];

  return raw.map((mirror) => {
    const service = firstString(mirror, TARGET_KEY_ALIASES);
    if (service === undefined) {
      throw new RouteCompileError("invalid-rule", "mirror requires a service", sourceRuleIds);
    }

    const sampleInput = mirror.sampleRate ?? mirror.sample ?? mirror.rate ?? 1;
    const sampleRate = parseRational(sampleInput, `mirror "${service}" sample rate`);
    if (sampleRate.greaterThan(ExactFraction.one())) {
      throw new RouteCompileError(
        "invalid-rule",
        `mirror "${service}" sample rate must be between 0 and 1`,
        sourceRuleIds,
      );
    }
    return { service, sampleRate };
  });
}

function prepareTable(
  tableId: string,
  input: RouteTableInput | readonly RouteRuleInput[],
): PreparedTable {
  const table: RouteTableInput = Array.isArray(input)
    ? { rules: input as readonly RouteRuleInput[] }
    : (input as RouteTableInput);

  if (!Array.isArray(table.rules)) {
    throw new RouteCompileError("invalid-rule", `route table "${tableId}" requires a rules array`);
  }

  const rules: PreparedRule[] = [];
  const ids = new Set<string>();

  for (let index = 0; index < table.rules.length; index += 1) {
    const raw = table.rules[index];
    const ruleId = typeof raw.id === "string" && raw.id.trim().length > 0
      ? raw.id.trim()
      : `${tableId}#${index + 1}`;
    if (ids.has(ruleId)) {
      throw new RouteCompileError(
        "invalid-rule",
        `duplicate rule id "${ruleId}" in table "${tableId}"`,
      );
    }
    ids.add(ruleId);

    const sourceRuleIds = [ruleId];
    const targetsInput = raw.targets ?? raw.weightedTargets;
    const hasTargets = targetsInput !== undefined;
    const hasDelegate = raw.delegate !== undefined;

    if (hasTargets && hasDelegate) {
      throw new RouteCompileError(
        "invalid-rule",
        `rule "${ruleId}" cannot declare both targets and delegate`,
        sourceRuleIds,
      );
    }

    rules.push({
      id: ruleId,
      index,
      path: parsePath(raw.path ?? raw.match),
      headers: parseHeaders(raw.headers ?? raw.header),
      targets: hasTargets ? parseTargetList(targetsInput, sourceRuleIds) : undefined,
      delegates: hasDelegate ? delegateRefs(raw.delegate as DelegateInput, sourceRuleIds) : undefined,
      mirrors: parseMirrors(raw.mirror, raw.mirrors, sourceRuleIds),
    });
  }

  return {
    id: tableId,
    name: typeof table.name === "string" && table.name.trim().length > 0
      ? table.name.trim()
      : tableId,
    rules,
  };
}

export function prepareInput(input: CompileInput): PreparedInput {
  if (input === null || input === undefined || !("root" in input)) {
    throw new RouteCompileError("invalid-rule", "compile input requires a root route table");
  }

  const rootTableInput = input.root;
  const rootName = !Array.isArray(rootTableInput) &&
      typeof (rootTableInput as RouteTableInput).name === "string" &&
      (rootTableInput as RouteTableInput).name!.trim().length > 0
    ? (rootTableInput as RouteTableInput).name!.trim()
    : undefined;
  const rootTableId = input.rootTableId?.trim() || rootName || "root";

  const tables = new Map<string, PreparedTable>();
  const rootTable = prepareTable(rootTableId, rootTableInput);
  tables.set(rootTableId, rootTable);

  for (const [tableId, rawTable] of Object.entries(input.tables ?? {})) {
    const normalizedId = tableId.trim();
    if (normalizedId.length === 0) {
      throw new RouteCompileError("invalid-rule", "child table id must not be empty");
    }
    if (tables.has(normalizedId)) {
      throw new RouteCompileError(
        "invalid-rule",
        `duplicate table id "${normalizedId}" (including root table)`,
      );
    }
    tables.set(normalizedId, prepareTable(normalizedId, rawTable));
  }

  return {
    rootTableId,
    tables,
    tableIds: [...tables.keys()],
  };
}

export function resolveTableReference(
  prepared: PreparedInput,
  reference: string,
): PreparedTable | undefined {
  const direct = prepared.tables.get(reference);
  if (direct !== undefined) {
    return direct;
  }

  for (const table of prepared.tables.values()) {
    if (table.name === reference) {
      return table;
    }
  }
  return undefined;
}
