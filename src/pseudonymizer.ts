import { createHmac } from "node:crypto";
import { canonicalize } from "./canonicalize.ts";
import { compilePattern, matches, formatPath, type CompiledPattern, type ConcreteSegment } from "./pathPattern.ts";
import { PseudonymizationError } from "./errors.ts";
import type {
  FieldRule,
  Issue,
  JsonValue,
  LogEntry,
  Logger,
  MappingEntry,
  MappingTable,
  PseudonymizeResult,
  Stats,
  UnauthorizedPolicy,
} from "./types.ts";

export const REDACTED = "[REDACTED]";

interface CompiledRule {
  action: FieldRule["action"];
  domain?: string;
  pattern: CompiledPattern;
}

interface InternalEntry {
  domain: string;
  pseudonym: string;
  valueHash: string;
  value: JsonValue;
  paths: string[];
}

export interface PseudonymizerOptions {
  /** Secret keying material. Rotation changes all pseudonyms; keep it out of the data and the logs. */
  secret: string | Buffer;
  rules: FieldRule[];
  /** Leaf matched by no rule: "redact" (default) replaces it, "error" also records an UNAUTHORIZED_FIELD issue. Never passes through. */
  unauthorizedPolicy?: UnauthorizedPolicy;
  /** Reuse a previously exported mapping to keep pseudonyms stable across runs. */
  mapping?: MappingTable;
  logger?: Logger;
}

export class Pseudonymizer {
  #secret: Buffer;
  #rules: CompiledRule[];
  #unauthorizedPolicy: UnauthorizedPolicy;
  #logger: Logger;
  /** domain -> pseudonym -> entry */
  #table = new Map<string, Map<string, InternalEntry>>();
  #issues: Issue[] = [];
  #stats: Stats = { pseudonymized: 0, redacted: 0, allowed: 0 };

  constructor(options: PseudonymizerOptions) {
    if (options.secret === undefined || options.secret === null) {
      throw new PseudonymizationError("INVALID_RULE", "$", "a secret is required");
    }
    this.#secret = Buffer.isBuffer(options.secret) ? options.secret : Buffer.from(options.secret, "utf8");
    this.#unauthorizedPolicy = options.unauthorizedPolicy ?? "redact";
    this.#logger = options.logger ?? (() => {});
    this.#rules = (options.rules ?? []).map((rule) => {
      const pattern = compilePattern(rule.pattern);
      if (rule.action === "pseudonymize" && (!rule.domain || rule.domain.length === 0)) {
        throw new PseudonymizationError("INVALID_RULE", "$", `rule "${rule.pattern}" requires a domain`);
      }
      const compiled: CompiledRule = { action: rule.action, pattern };
      if (rule.domain !== undefined) compiled.domain = rule.domain;
      return compiled;
    });
    for (const entry of options.mapping?.entries ?? []) {
      this.#addEntry({ ...entry, paths: [...entry.paths] });
    }
  }

  pseudonymize(input: JsonValue): PseudonymizeResult {
    this.#issues = [];
    this.#stats = { pseudonymized: 0, redacted: 0, allowed: 0 };
    const data = this.#walk(input, [], new Set());
    return { data, issues: [...this.#issues], stats: { ...this.#stats } };
  }

  /** Mapping export — contains original values; store and transmit it separately from the redacted data. */
  exportMapping(): MappingTable {
    const entries: MappingEntry[] = [];
    for (const byPseudonym of this.#table.values()) {
      for (const e of byPseudonym.values()) {
        entries.push({ domain: e.domain, pseudonym: e.pseudonym, valueHash: e.valueHash, value: e.value, paths: [...e.paths] });
      }
    }
    entries.sort((a, b) => (a.domain + a.pseudonym).localeCompare(b.domain + b.pseudonym));
    return { version: 1, entries };
  }

  #walk(node: unknown, path: ConcreteSegment[], stack: Set<unknown>): JsonValue {
    const rule = this.#findRule(path);
    if (rule) return this.#applyRule(rule, node, path);

    if (Array.isArray(node)) {
      if (stack.has(node)) return this.#nonJson(path, "cyclic structure is not JSON-compatible");
      stack.add(node);
      const out = node.map((item, i) => this.#walk(item, [...path, { kind: "index", index: i }], stack));
      stack.delete(node);
      return out;
    }
    if (node !== null && typeof node === "object") {
      const proto = Object.getPrototypeOf(node);
      if (proto !== Object.prototype && proto !== null) {
        return this.#nonJson(path, "non-plain object is not JSON-compatible");
      }
      if (stack.has(node)) return this.#nonJson(path, "cyclic structure is not JSON-compatible");
      stack.add(node);
      const out: Record<string, JsonValue> = {};
      for (const key of Object.keys(node)) {
        out[key] = this.#walk((node as Record<string, unknown>)[key], [...path, { kind: "key", name: key }], stack);
      }
      stack.delete(node);
      return out;
    }
    if (node === null || typeof node === "string" || typeof node === "boolean") {
      return this.#unauthorized(path, node as JsonValue);
    }
    if (typeof node === "number") {
      if (!Number.isFinite(node)) return this.#nonJson(path, "non-finite number is not JSON-compatible");
      return this.#unauthorized(path, node);
    }
    return this.#nonJson(path, `type "${typeof node}" is not JSON-compatible`);
  }

  #applyRule(rule: CompiledRule, node: unknown, path: ConcreteSegment[]): JsonValue {
    const here = formatPath(path);
    if (rule.action === "redact") {
      this.#stats.redacted++;
      this.#log({ level: "info", code: "REDACTED", path: here, message: `redacted ${here}` });
      return REDACTED;
    }
    let canonical: string;
    try {
      canonical = canonicalize(node, here);
    } catch (err) {
      if (err instanceof PseudonymizationError) return this.#nonJson(path, err.message);
      throw err;
    }
    if (rule.action === "allow") {
      this.#stats.allowed++;
      return node as JsonValue;
    }
    const domain = rule.domain!;
    const digest = this.#hmac(`${domain}${canonical}`);
    const pseudonym = `psn_${digest.slice(0, 24)}`;
    const valueHash = digest;
    const existing = this.#table.get(domain)?.get(pseudonym);
    if (existing) {
      if (existing.valueHash !== valueHash) {
        const message = `pseudonym collision in domain "${domain}"`;
        this.#issues.push({ code: "COLLISION", path: here, message });
        this.#log({ level: "error", code: "COLLISION", path: here, domain, pseudonym, message: `${message} at ${here}` });
        return REDACTED;
      }
      if (!existing.paths.includes(here)) existing.paths.push(here);
    } else {
      this.#addEntry({ domain, pseudonym, valueHash, value: node as JsonValue, paths: [here] });
    }
    this.#stats.pseudonymized++;
    this.#log({ level: "info", code: "PSEUDONYMIZED", path: here, domain, pseudonym, message: `pseudonymized ${here} -> ${pseudonym}` });
    return pseudonym;
  }

  #unauthorized(path: ConcreteSegment[], _value: JsonValue): JsonValue {
    const here = formatPath(path);
    this.#stats.redacted++;
    if (this.#unauthorizedPolicy === "error") {
      const message = `no rule authorizes field at ${here}`;
      this.#issues.push({ code: "UNAUTHORIZED_FIELD", path: here, message });
      this.#log({ level: "error", code: "UNAUTHORIZED_FIELD", path: here, message });
    } else {
      this.#log({ level: "warn", code: "UNAUTHORIZED_FIELD", path: here, message: `redacted unauthorized field at ${here}` });
    }
    return REDACTED;
  }

  #nonJson(path: ConcreteSegment[], reason: string): JsonValue {
    const here = formatPath(path);
    const message = `${reason} at ${here}`;
    this.#issues.push({ code: "NON_JSON_VALUE", path: here, message });
    this.#log({ level: "error", code: "NON_JSON_VALUE", path: here, message });
    this.#stats.redacted++;
    return REDACTED;
  }

  #findRule(path: ConcreteSegment[]): CompiledRule | undefined {
    if (path.length === 0) return undefined;
    return this.#rules.find((r) => matches(r.pattern, path));
  }

  #addEntry(entry: InternalEntry): void {
    let byPseudonym = this.#table.get(entry.domain);
    if (!byPseudonym) {
      byPseudonym = new Map();
      this.#table.set(entry.domain, byPseudonym);
    }
    byPseudonym.set(entry.pseudonym, entry);
  }

  #hmac(input: string): string {
    return createHmac("sha256", this.#secret).update(input, "utf8").digest("hex");
  }

  #log(entry: LogEntry): void {
    this.#logger(entry);
  }
}
