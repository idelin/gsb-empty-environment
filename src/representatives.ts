import type {
  DatabaseSchema,
  PartitionCertificate,
  Plan,
  RepresentativeClass,
  RepresentativeColumnDomain,
  RepresentativeDatabase,
  RepresentativeDomain,
  RepresentativeTableDomain,
  SqlValue,
  TenantReadPolicy,
} from "./types.ts";
import { canonicalJson, digest, planDigest } from "./ast.ts";
import { analyzePlans, type PlanAnalysis } from "./analysis.ts";
import { policyTenantLabels } from "./policy.ts";
import { valueKey } from "./value.ts";

export const DEFAULT_MAX_CLASSES = 300_000;

export class RepresentativeLimitError extends Error {
  constructor(limit: number) {
    super(`Representative universe exceeds the configured limit of ${limit} classes`);
    this.name = "RepresentativeLimitError";
  }
}

type ColumnInfo = RepresentativeColumnDomain;

function closedSet(values: readonly string[]): Set<string> {
  return new Set(values);
}

function freshPrefix(base: string, existing: ReadonlySet<string>): string {
  let prefix = base;
  for (;;) {
    let collides = false;
    for (const value of existing) {
      if (value.startsWith(prefix)) {
        collides = true;
        break;
      }
    }
    if (!collides) return prefix;
    prefix += "\u0000";
  }
}

function tableColumns(
  table: string,
  plans: readonly Plan[],
  analysis: PlanAnalysis,
  schema: DatabaseSchema | undefined,
  policy: TenantReadPolicy | undefined,
): ColumnInfo[] {
  const schemaColumns = schema?.tables[table]?.columns ?? {};
  const names = new Set(analysis.columnsByTable[table] ?? []);

  const tenantLabels = new Set(policyTenantLabels(policy));
  const result: ColumnInfo[] = [];

  const projected = new Set<string>();
  const markProjected = (plan: Plan): void => {
    if (plan.kind === "project") {
      for (const entry of plan.columns) {
        if (entry.expr.kind === "column") {
          const owner = entry.expr.table === undefined || entry.expr.table === ""
            ? table
            : analysis.aliases[entry.expr.table] ?? entry.expr.table;
          projected.add(`${owner}\u0000${entry.expr.column}`);
        }
      }
    }
    if (plan.kind === "filter") markProjected(plan.input);
    else if (plan.kind === "join") {
      markProjected(plan.left);
      markProjected(plan.right);
    } else if (plan.kind === "unionAll") {
      for (const input of plan.inputs) markProjected(input);
    }
  };
  for (const plan of plans) markProjected(plan);

  const baseOutput = (plan: Plan): Set<string> => {
    switch (plan.kind) {
      case "scan":
        return new Set(
          (analysis.columnsByTable[plan.table] ?? []).map(
            (column) => `${plan.table}\u0000${column}`,
          ),
        );
      case "filter":
        return baseOutput(plan.input);
      case "join": {
        const output = baseOutput(plan.left);
        for (const key of baseOutput(plan.right)) output.add(key);
        return output;
      }
      case "project":
        return new Set();
      case "unionAll": {
        const output = new Set<string>();
        for (const input of plan.inputs) {
          for (const key of baseOutput(input)) output.add(key);
        }
        return output;
      }
    }
  };
  for (const plan of plans) {
    for (const key of baseOutput(plan)) projected.add(key);
  }

  const componentUsage = (start: string): { id: boolean; enums: boolean } => {
    const seen = new Set<string>();
    const queue = [start];
    const usage = { id: false, enums: false };
    while (queue.length > 0) {
      const node = queue.pop() as string;
      if (seen.has(node)) continue;
      seen.add(node);
      const own = analysis.columnUsage[node];
      if (own !== undefined) {
        usage.id ||= own.id;
        usage.enums ||= own.enums;
      }
      for (const next of analysis.equalityEdges[node] ?? []) queue.push(next);
    }
    return usage;
  };

  for (const name of [...names].sort()) {
    const declared = schemaColumns[name];
    const isTenant = analysis.tenantColumns[table] === name;
    const usage = componentUsage(`${table}\u0000${name}`);
    let inferred: "enum" | "id" | "any";
    if (usage.id && usage.enums) inferred = "any";
    else if (usage.id) inferred = "id";
    else if (usage.enums) inferred = "enum";
    else inferred = isTenant ? "enum" : "id";
    let kind: "enum" | "id" | "any" = declared?.kind ?? inferred;
    const nullable = declared?.nullable ?? true;

    const openEnums = new Set(analysis.enumLiterals);
    if (isTenant && declared?.kind !== "id") {
      for (const label of tenantLabels) openEnums.add(label);
    }

    let enumMode: "closed" | "open" | "none";
    let enumValues: string[];
    if (kind === "id") {
      enumMode = "none";
      enumValues = [];
    } else if (declared?.enumValues !== undefined) {
      enumMode = "closed";
      enumValues = [...declared.enumValues].sort();
    } else {
      enumMode = "open";
      enumValues = [...openEnums].sort();
    }

    const idMode = kind === "enum" ? "none" : "open";
    const node = `${table}\u0000${name}`;
    const hasColumnEquality = (analysis.equalityEdges[node] ?? []).length > 0;
    const isProjected = projected.has(node);
    const customRowVisibility =
      policy?.rowVisible !== undefined || policy?.canRead !== undefined;
    const needsDistinct = isProjected || hasColumnEquality || customRowVisibility;
    const freshEnumClasses = enumMode === "closed" || enumMode === "none"
      ? "none"
      : needsDistinct ? "canonical" : "single";
    const freshIdClasses = idMode === "none"
      ? "none"
      : needsDistinct ? "canonical" : "single";

    result.push({
      name,
      kind,
      nullable,
      enumMode,
      enumValues,
      freshEnumClasses,
      idMode,
      freshIdClasses,
    });
  }
  return result;
}

export function deriveRepresentativeDomain(
  plans: readonly Plan[],
  policy?: TenantReadPolicy,
  schema?: DatabaseSchema,
): RepresentativeDomain {
  const analysis = analyzePlans(plans, policy, schema);
  const tables: RepresentativeTableDomain[] = analysis.tables.map((table) => {
    const maxRows = Math.max(0, Math.min(2, schema?.tables[table]?.maxRows ?? 2));
    return {
      name: table,
      maxRows,
      columns: tableColumns(table, plans, analysis, schema, policy),
    };
  });

  const ids = new Set(analysis.identifierConstants);
  for (const label of policyTenantLabels(policy)) ids.add(label);

  return {
    tables,
    identifierConstants: [...ids].sort(),
    enumLiterals: [...analysis.enumLiterals].sort(),
    rowCounts: [0, 1, 2],
    freshIdentifierClasses: "canonical-first-use",
    freshEnumClasses: "canonical-first-use",
  };
}

function canonicalRow(row: Readonly<Record<string, SqlValue>>): string {
  const keys = Object.keys(row).sort();
  return JSON.stringify(keys.map((key) => [key, valueKey(row[key])]));
}

function compareText(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

function canonicalDatabaseText(database: RepresentativeDatabase): string {
  const tables = Object.keys(database).sort(compareText);
  return tables.map((table) => {
    const rows = database[table].map(canonicalRow).sort(compareText);
    return JSON.stringify([table, rows]);
  }).join(";");
}

export function canonicalizeDatabase(database: RepresentativeDatabase): RepresentativeDatabase {
  const result: Record<string, readonly Readonly<Record<string, SqlValue>>[]> = {};
  for (const table of Object.keys(database).sort(compareText)) {
    const rows = database[table]
      .map((row) => {
        const copy: Record<string, SqlValue> = {};
        for (const [column, value] of Object.entries(row)) copy[column] = value;
        return { row: copy, key: canonicalRow(copy) };
      })
      .sort((left, right) => compareText(left.key, right.key))
      .map((entry) => entry.row);
    result[table] = rows;
  }
  return result;
}

export function databaseClassKey(database: RepresentativeDatabase): string {
  return digest(canonicalDatabaseText(database));
}

function rowTags(
  table: string,
  rows: readonly Readonly<Record<string, SqlValue>>[],
  domain: RepresentativeDomain,
): string[] {
  const tags = new Set<string>();
  if (rows.length === 0) tags.add(`empty:${table}`);
  if (rows.length === 2) {
    const first = canonicalRow(rows[0]);
    const second = canonicalRow(rows[1]);
    if (first === second) tags.add(`duplicate:${table}`);
  }

  const columns = domain.tables.find((entry) => entry.name === table)?.columns ?? [];
  for (const row of rows) {
    if (columns.length > 0 && columns.every((column) => row[column.name]?.kind === "null")) {
      tags.add(`all-null-row:${table}`);
    }
  }
  return [...tags];
}

function databaseTags(database: RepresentativeDatabase, domain: RepresentativeDomain): string[] {
  const tags = new Set<string>();
  const idPositions = new Map<string, number>();
  const constants = new Set(domain.identifierConstants);
  const enumConstants = new Set(domain.enumLiterals);

  for (const table of Object.keys(database).sort()) {
    for (const tag of rowTags(table, database[table], domain)) tags.add(tag);
    for (const row of database[table]) {
      for (const value of Object.values(row)) {
        if (value.kind === "id") {
          idPositions.set(value.name, (idPositions.get(value.name) ?? 0) + 1);
          if (constants.has(value.name)) tags.add(`constant-id:${value.name}`);
        } else if (value.kind === "enum") {
          if (enumConstants.has(value.value)) {
            tags.add(`constant-enum:${value.value}`);
          } else {
            tags.add(`enum:${value.value}`);
          }
        }
      }
    }
  }

  for (const [name, count] of idPositions) {
    if (count > 1) tags.add(`shared-id:${name}`);
  }
  tags.add("identifier-partition");
  return [...tags].sort();
}

function leftJoinRightTables(plan: Plan, result: Set<string>): Set<string> {
  switch (plan.kind) {
    case "scan":
      return result;
    case "filter":
      return leftJoinRightTables(plan.input, result);
    case "project":
      return leftJoinRightTables(plan.input, result);
    case "join": {
      leftJoinRightTables(plan.left, result);
      leftJoinRightTables(plan.right, result);
      if (plan.joinType === "left") {
        const scans: string[] = [];
        const gather = (node: Plan): void => {
          if (node.kind === "scan") scans.push(node.table);
          else if (node.kind === "filter" || node.kind === "project") gather(node.input);
          else if (node.kind === "join") {
            gather(node.left);
            gather(node.right);
          } else for (const input of node.inputs) gather(input);
        };
        gather(plan.right);
        for (const table of scans) result.add(table);
      }
      return result;
    }
    case "unionAll":
      for (const input of plan.inputs) leftJoinRightTables(input, result);
      return result;
  }
}

interface DomainState {
  readonly nextId: number;
  readonly nextEnum: number;
}

interface Slot {
  readonly table: string;
  readonly rowIndex: number;
  readonly column: ColumnInfo;
}

function freshIdName(index: number, constants: ReadonlySet<string>): string {
  return `${freshPrefix("\u0000fid:", constants)}${index}`;
}

function freshEnumName(index: number, literals: ReadonlySet<string>): string {
  return `${freshPrefix("\u0000fenum:", literals)}${index}`;
}

function columnOptions(
  domain: RepresentativeDomain,
  column: ColumnInfo,
  state: DomainState,
  constants: ReadonlySet<string>,
  literals: ReadonlySet<string>,
): { value: SqlValue; next: DomainState }[] {
  const options: { value: SqlValue; next: DomainState }[] = [];
  if (column.nullable) {
    options.push({ value: { kind: "null" }, next: state });
  }

  if (column.enumMode !== "none") {
    for (const value of column.enumValues) {
      options.push({ value: { kind: "enum", value }, next: state });
    }
    if (column.enumMode === "open") {
      if (column.freshEnumClasses === "single") {
        options.push({
          value: { kind: "enum", value: freshEnumName(0, literals) },
          next: state,
        });
      } else if (column.freshEnumClasses === "canonical") {
        for (let index = 0; index <= state.nextEnum; index += 1) {
          options.push({
            value: { kind: "enum", value: freshEnumName(index, literals) },
            next: index === state.nextEnum
              ? { ...state, nextEnum: state.nextEnum + 1 }
              : state,
          });
        }
      }
    }
  }

  if (column.idMode === "open") {
    for (const value of domain.identifierConstants) {
      options.push({ value: { kind: "id", name: value }, next: state });
    }
    if (column.freshIdClasses === "single") {
      options.push({
        value: { kind: "id", name: freshIdName(0, constants) },
        next: state,
      });
    } else if (column.freshIdClasses === "canonical") {
      for (let index = 0; index <= state.nextId; index += 1) {
        options.push({
          value: { kind: "id", name: freshIdName(index, constants) },
          next: index === state.nextId
            ? { ...state, nextId: state.nextId + 1 }
            : state,
        });
      }
    }
  }

  const seen = new Set<string>();
  return options.filter((option) => {
    const key = valueKey(option.value);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export interface EnumeratedRepresentatives {
  readonly classes: readonly RepresentativeClass[];
  readonly truncated: boolean;
}

export function enumerateDomainClasses(
  domain: RepresentativeDomain,
  maxClasses: number = DEFAULT_MAX_CLASSES,
): EnumeratedRepresentatives {
  const tables = [...domain.tables].sort((left, right) => left.name.localeCompare(right.name));
  const constants = closedSet(domain.identifierConstants);
  const literals = closedSet(domain.enumLiterals);
  const classes = new Map<string, RepresentativeClass>();
  let truncated = false;

  const addDatabase = (database: RepresentativeDatabase): boolean => {
    const canonical = canonicalizeDatabase(database);
    const key = digest(canonicalDatabaseText(canonical));
    if (!classes.has(key)) {
      classes.set(key, {
        key,
        database: canonical,
        tags: databaseTags(canonical, domain),
      });
      if (classes.size > maxClasses) {
        truncated = true;
        return false;
      }
    }
    return true;
  };

  const rowCountChoices = (table: RepresentativeTableDomain): number[] => {
    const allowed = new Set(domain.rowCounts);
    const result: number[] = [];
    for (let count = 0; count <= table.maxRows; count += 1) {
      if (allowed.has(count)) result.push(count);
    }
    return result.length > 0 ? result : [0];
  };

  const recurseCounts = (tableIndex: number, counts: number[]): boolean => {
    if (truncated) return false;
    if (tableIndex === tables.length) {
      const slots: Slot[] = [];
      for (let index = 0; index < tables.length; index += 1) {
        const table = tables[index];
        for (let row = 0; row < counts[index]; row += 1) {
          for (const column of table.columns) {
            slots.push({ table: table.name, rowIndex: row, column });
          }
        }
      }

      const rowsByTable: Record<string, Record<string, SqlValue>[]> = {};
      for (let index = 0; index < tables.length; index += 1) {
        rowsByTable[tables[index].name] = Array.from(
          { length: counts[index] },
          () => ({}),
        );
      }

      const assign = (index: number, state: DomainState): boolean => {
        if (truncated) return false;
        if (index === slots.length) {
          return addDatabase(rowsByTable);
        }
        const slot = slots[index];
        const target = rowsByTable[slot.table][slot.rowIndex];
        const options = columnOptions(domain, slot.column, state, constants, literals);
        for (const option of options) {
          target[slot.column.name] = option.value;
          if (assign(index + 1, option.next) === false && truncated) return false;
        }
        delete target[slot.column.name];
        return true;
      };

      assign(0, { nextId: 0, nextEnum: 0 });
      return !truncated;
    }

    for (const count of rowCountChoices(tables[tableIndex])) {
      counts[tableIndex] = count;
      if (!recurseCounts(tableIndex + 1, counts)) return false;
    }
    return true;
  };

  recurseCounts(0, Array.from({ length: tables.length }, () => 0));
  const ordered = [...classes.values()].sort((left, right) => compareText(left.key, right.key));
  return { classes: ordered, truncated };
}

export function buildRepresentativeDatabases(
  plans: readonly Plan[],
  policy?: TenantReadPolicy,
  schema?: DatabaseSchema,
  maxClasses: number = DEFAULT_MAX_CLASSES,
): RepresentativeDatabase[] {
  const domain = deriveRepresentativeDomain(plans, policy, schema);
  return enumerateDomainClasses(domain, maxClasses).classes.map((entry) => entry.database);
}

export const constructRepresentativeDatabases = buildRepresentativeDatabases;

function domainDigestInput(domain: RepresentativeDomain): unknown {
  return domain;
}

export function domainsEqual(left: RepresentativeDomain, right: RepresentativeDomain): boolean {
  return canonicalJson(domainDigestInput(left)) === canonicalJson(domainDigestInput(right));
}

export function certificateDigest(
  certificate: Omit<PartitionCertificate, "digest"> | PartitionCertificate,
): string {
  const { digest: _ignored, ...body } = certificate as PartitionCertificate;
  return digest(body);
}

export function buildPartitionCertificate(
  reference: Plan,
  candidate: Plan,
  policy: TenantReadPolicy | undefined,
  schema: DatabaseSchema | undefined,
  equivalent: boolean,
  maxClasses: number = DEFAULT_MAX_CLASSES,
): { certificate: PartitionCertificate; truncated: boolean } {
  const domain = deriveRepresentativeDomain([reference, candidate], policy, schema);
  const enumerated = enumerateDomainClasses(domain, maxClasses);
  const root = {
    format: "bag-partition-certificate-v1" as const,
    referencePlanDigest: planDigest(reference),
    candidatePlanDigest: planDigest(candidate),
    policyVersion: String(
      policy?.version ?? policy?.policyVersion ?? policy?.revision ??
        digest(policy ?? {}),
    ),
    domain,
    equivalent,
    classes: enumerated.classes,
    classCount: enumerated.classes.length,
  };
  return {
    certificate: { ...root, digest: certificateDigest(root) },
    truncated: enumerated.truncated,
  };
}

export { leftJoinRightTables };
