import type { SqlValue, TenantReadPolicy, TenantPolicyRule } from "./types.ts";
import { digest } from "./ast.ts";
import { normalizeValue } from "./value.ts";

export interface NormalizedPolicy {
  readonly version: string;
  readonly subject: string;
  readonly tenantColumn: (table: string) => string;
  readonly visible: (
    table: string,
    row: Readonly<Record<string, SqlValue>>,
  ) => boolean;
  readonly source: TenantReadPolicy;
}

function asSet(value: unknown): Set<string> | undefined {
  if (value === undefined || value === null) return undefined;
  if (value instanceof Set) return new Set([...value].map(String));
  if (Array.isArray(value)) return new Set(value.map(String));
  return undefined;
}

function rowTenantKey(row: Readonly<Record<string, SqlValue>>, column: string): string | null {
  const value = row[column];
  if (value === undefined || value.kind === "null") return null;
  return value.kind === "enum" ? value.value : value.name;
}

function ruleMatches(rule: TenantPolicyRule, subject: string, table: string, tenant: string | null): boolean {
  if (rule.subject !== undefined && rule.subject !== "*" && rule.subject !== subject) return false;
  if (rule.table !== undefined && rule.table !== "*" && rule.table !== table) return false;
  if (rule.tenant !== undefined && rule.tenant !== null && rule.tenant !== tenant) return false;
  return true;
}

export function normalizePolicy(policy: TenantReadPolicy | undefined): NormalizedPolicy {
  if (policy === undefined) {
    return {
      version: "no-policy",
      subject: "anonymous",
      tenantColumn: () => "tenant",
      visible: () => true,
      source: {},
    };
  }

  const subject = String(policy.subject ?? policy.principal ?? "");
  const version = String(
    policy.version ?? policy.policyVersion ?? policy.revision ?? digest(policy),
  );
  const fixedAllowed =
    asSet(policy.allowedTenants) ??
    asSet(policy.visibleTenants) ??
    asSet(policy.tenants) ??
    (policy.tenant === undefined ? undefined : new Set([String(policy.tenant)]));

  const grantMap = policy.grants ?? policy.tenantsBySubject;
  const granted = grantMap === undefined
    ? undefined
    : asSet(grantMap[subject] ?? grantMap["*"]);

  const rules = policy.rules ?? [];
  const configured =
    policy.allowAll === true ||
    policy.denyAll === true ||
    fixedAllowed !== undefined ||
    granted !== undefined ||
    policy.allowNullTenant === true ||
    rules.length > 0 ||
    policy.canRead !== undefined ||
    policy.rowVisible !== undefined;

  const tenantColumn = (table: string): string =>
    policy.tenantColumns?.[table] ?? policy.tenantColumn ?? "tenant";

  const visible = (
    table: string,
    row: Readonly<Record<string, SqlValue>>,
  ): boolean => {
    if (policy.denyAll === true) return false;
    if (policy.allowAll === true) return true;
    if (fixedAllowed?.has("*") === true || granted?.has("*") === true) return true;

    if (policy.rowVisible !== undefined) {
      return policy.rowVisible(row, { subject, table }) === true;
    }

    const column = tenantColumn(table);
    const tenant = rowTenantKey(row, column);
    if (tenant === null && policy.allowNullTenant !== true) return false;

    if (policy.canRead !== undefined) {
      return policy.canRead(subject, tenant, { table, row }) === true;
    }

    let decision: boolean | undefined;
    for (const rule of rules) {
      if (ruleMatches(rule, subject, table, tenant)) decision = rule.allow;
    }

    if (decision !== undefined) return decision;
    if (fixedAllowed !== undefined) return tenant !== null && fixedAllowed.has(tenant);
    if (granted !== undefined) return tenant !== null && granted.has(tenant);

    return !configured;
  };

  return { version, subject, tenantColumn, visible, source: policy };
}

export function policyTenantLabels(policy: TenantReadPolicy | undefined): string[] {
  if (policy === undefined) return [];
  const labels = new Set<string>();
  const add = (value: unknown): void => {
    if (value === undefined || value === null) return;
    if (value instanceof Set) {
      for (const entry of value) labels.add(String(entry));
      return;
    }
    if (Array.isArray(value)) {
      for (const entry of value) labels.add(String(entry));
      return;
    }
    if (typeof value === "object") {
      for (const entry of Object.values(value as Record<string, unknown>)) add(entry);
    }
  };
  add(policy.allowedTenants);
  add(policy.visibleTenants);
  add(policy.tenants);
  if (policy.tenant !== undefined) labels.add(String(policy.tenant));
  add(policy.grants);
  add(policy.tenantsBySubject);
  for (const rule of policy.rules ?? []) {
    if (rule.tenant !== undefined && rule.tenant !== null) labels.add(String(rule.tenant));
  }
  return [...labels].sort();
}

export function normalizeRow(
  raw: Readonly<Record<string, unknown>>,
): Record<string, SqlValue> {
  const result: Record<string, SqlValue> = {};
  for (const [key, value] of Object.entries(raw)) {
    result[key] = normalizeValue(value);
  }
  return result;
}
