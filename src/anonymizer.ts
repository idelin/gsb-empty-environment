import { MappingTable } from "./mapping.ts";
import { formatPath, matchRule, parseRulePath } from "./paths.ts";
import { Pseudonymizer, DEFAULT_PSEUDONYM_LENGTH } from "./pseudonym.ts";
import type {
  AnonymizeError,
  AnonymizeOptions,
  AnonymizeResult,
  FieldRule,
  JsonValue,
  PathSegment,
} from "./types.ts";

interface CompiledRule {
  domain: string;
  action: "pseudonymize" | "redact" | "drop";
  segments: ReturnType<typeof parseRulePath>;
}

const REDACTED = "[REDACTED]";

export function anonymize(input: JsonValue, options: AnonymizeOptions): AnonymizeResult {
  const onUnknown = options.onUnknownField ?? "error";
  const pseudonymizer = new Pseudonymizer(
    options.key,
    options.pseudonymLength ?? DEFAULT_PSEUDONYM_LENGTH,
  );
  const table = new MappingTable(pseudonymizer);
  const errors: AnonymizeError[] = [];
  const stats = { pseudonymized: 0, redacted: 0, dropped: 0, collisions: 0 };

  const rules: CompiledRule[] = options.rules.map((rule: FieldRule) => {
    try {
      return {
        domain: rule.domain,
        action: rule.action ?? "pseudonymize",
        segments: parseRulePath(rule.path),
      };
    } catch {
      errors.push({
        code: "INVALID_RULE",
        path: "$",
        message: `Invalid rule path "${rule.path}" for domain "${rule.domain}"`,
      });
      return null;
    }
  }).filter((r): r is CompiledRule => r !== null);

  const findRule = (path: PathSegment[]): CompiledRule | undefined =>
    rules.find((r) => matchRule(r.segments, path));

  const walk = (node: JsonValue, path: PathSegment[]): JsonValue => {
    const rule = findRule(path);
    if (rule) {
      const at = formatPath(path);
      switch (rule.action) {
        case "pseudonymize": {
          stats.pseudonymized += 1;
          return table.register(rule.domain, node, at);
        }
        case "redact":
          stats.redacted += 1;
          return REDACTED;
        case "drop":
          stats.dropped += 1;
          return REDACTED;
      }
    }
    if (Array.isArray(node)) {
      return node.map((item, index) => walk(item, [...path, index]));
    }
    if (node !== null && typeof node === "object") {
      const out: Record<string, JsonValue> = {};
      for (const key of Object.keys(node)) {
        const childPath = [...path, key];
        const childRule = findRule(childPath);
        if (childRule?.action === "drop") {
          stats.dropped += 1;
          continue;
        }
        out[key] = walk(node[key] as JsonValue, childPath);
      }
      return out;
    }
    const at = formatPath(path);
    if (onUnknown === "error") {
      errors.push({
        code: "UNAUTHORIZED_FIELD",
        path: at,
        message: `Field at ${at} is not covered by any rule and was redacted`,
      });
    }
    stats.redacted += 1;
    return REDACTED;
  };

  const data = walk(input, []);
  const mapping = table.export();
  stats.collisions = mapping.collisions.length;
  return { data, mapping, errors: [...errors, ...mapping.collisions], stats };
}
