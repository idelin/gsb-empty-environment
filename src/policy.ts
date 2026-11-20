import { cidrContains, parseCidr, parseIp } from "./address.ts";
import type { ParsedCidr } from "./address.ts";
import { normalizeHostname, normalizeSuffix } from "./hostname.ts";

export type RuleAction = "allow" | "deny";

export interface PrefixRule {
  cidr: string;
  action: RuleAction;
}

export interface TenantPolicyConfig {
  rules: PrefixRule[];
  allowedSuffixes: string[];
  deadlineMs?: number;
}

interface CompiledRule extends ParsedCidr {
  action: RuleAction;
}

export class TenantPolicy {
  readonly tenantId: string;
  readonly generation: number;
  readonly allowedSuffixes: string[];
  readonly deadlineMs: number;
  private readonly rules: CompiledRule[];

  constructor(tenantId: string, config: TenantPolicyConfig, generation: number) {
    this.tenantId = tenantId;
    this.generation = generation;
    this.allowedSuffixes = config.allowedSuffixes.map(normalizeSuffix);
    this.deadlineMs = config.deadlineMs ?? Number.POSITIVE_INFINITY;
    this.rules = [];
    for (const rule of config.rules) {
      const parsed = parseCidr(rule.cidr);
      if (!parsed) throw new Error(`invalid CIDR in tenant policy: ${rule.cidr}`);
      this.rules.push({ ...parsed, action: rule.action });
    }
  }

  isAddressAllowed(address: string): boolean {
    const ip = parseIp(address);
    if (!ip) return false;
    let best: CompiledRule | null = null;
    for (const rule of this.rules) {
      if (rule.family !== ip.family) continue;
      if (!cidrContains(rule, ip)) continue;
      if (best === null || rule.length > best.length) {
        best = rule;
      } else if (rule.length === best.length && rule.action === "deny") {
        best = rule;
      }
    }
    return best !== null && best.action === "allow";
  }

  allowsHostname(name: string): boolean {
    const normalized = normalizeHostname(name);
    return this.allowedSuffixes.some(
      suffix => normalized === suffix || normalized.endsWith(`.${suffix}`),
    );
  }
}
