import type {
  AggregateRule,
  Json,
  OnError,
  Schema,
  ValidationIssue,
  ValidationResult,
  StreamStatus,
} from "./types.ts";

export class ValidationError extends Error {
  readonly issues: ValidationIssue[];

  constructor(issues: ValidationIssue[]) {
    super(issues[0]?.message ?? "validation failed");
    this.name = "ValidationError";
    this.issues = issues;
  }
}

function escapeToken(token: string): string {
  return token.replace(/~/g, "~0").replace(/\//g, "~1");
}

function typeOf(value: Json): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function matchesType(value: Json, type: Schema["type"]): boolean {
  if (type === undefined) return true;
  const actual = typeOf(value);
  if (type === "integer") {
    return actual === "number" && Number.isInteger(value as number);
  }
  if (type === "number") return actual === "number";
  return actual === type;
}

export class StreamArrayValidator {
  private readonly schema: Schema;
  private readonly onError: OnError;
  private readonly seen: Map<string, Set<string>>;
  private readonly sums: Map<string, number>;
  private readonly issues: ValidationIssue[] = [];
  private length = 0;
  private state: "open" | "ended" = "open";
  private result: ValidationResult | null = null;

  constructor(schema: Schema, onError: OnError = "collect") {
    this.schema = schema;
    this.onError = onError;
    this.seen = new Map((schema.uniqueKeys ?? []).map((key) => [key, new Set()]));
    this.sums = new Map((schema.aggregates ?? []).map((rule) => [rule.key, 0]));
  }

  push(item: Json): ValidationIssue[] {
    if (this.state === "ended") {
      throw new Error("cannot push() after finish(): the stream has ended");
    }

    const index = this.length;
    const itemPath = `/${index}`;
    const found: ValidationIssue[] = [];

    this.validateItem(item, itemPath, "items", found);

    this.checkUniqueKeys(item, itemPath, found);

    if (this.schema.maxItems !== undefined && index >= this.schema.maxItems) {
      found.push({
        instancePath: itemPath,
        rulePath: "maxItems",
        rule: "maxItems",
        message: `array length ${index + 1} exceeds maxItems ${this.schema.maxItems}`,
      });
    }

    if (found.length > 0) {
      if (this.onError === "throw") {
        throw new ValidationError(found);
      }
      this.issues.push(...found);
      return found;
    }

    this.length += 1;
    this.observe(item);
    return [];
  }

  finish(): ValidationResult {
    if (this.state === "ended" && this.result) {
      return this.result;
    }

    const found: ValidationIssue[] = [];

    if (this.schema.minItems !== undefined && this.length < this.schema.minItems) {
      found.push({
        instancePath: "",
        rulePath: "minItems",
        rule: "minItems",
        message: `array length ${this.length} is below minItems ${this.schema.minItems}`,
      });
    }

    for (const rule of this.schema.aggregates ?? []) {
      this.checkAggregate(rule, found);
    }

    this.state = "ended";
    const allIssues = this.issues.concat(found);
    this.result = {
      valid: allIssues.length === 0,
      state: "ended",
      length: this.length,
      issues: allIssues,
      sums: Object.fromEntries(this.sums),
    };
    return this.result;
  }

  status(): StreamStatus {
    return {
      state: this.state,
      length: this.length,
      valid: this.state === "ended" ? (this.result?.valid ?? false) : false,
    };
  }

  private observe(item: Json): void {
    for (const key of this.schema.uniqueKeys ?? []) {
      const value = readPath(item, key);
      if (value !== undefined) {
        this.seen.get(key)!.add(serializeKey(value));
      }
    }
    for (const rule of this.schema.aggregates ?? []) {
      const value = readPath(item, rule.key);
      if (typeof value === "number" && Number.isFinite(value)) {
        this.sums.set(rule.key, (this.sums.get(rule.key) ?? 0) + value);
      }
    }
  }

  private checkAggregate(rule: AggregateRule, out: ValidationIssue[]): void {
    const total = this.sums.get(rule.key) ?? 0;
    const bounds = rule.sum;
    if (!bounds) return;

    if (bounds.minimum !== undefined && total < bounds.minimum) {
      out.push({
        instancePath: "",
        rulePath: `aggregates/${escapeToken(rule.key)}/sum/minimum`,
        rule: "aggregates",
        message: `sum of "${rule.key}" is ${total}, below minimum ${bounds.minimum}`,
      });
    }
    if (bounds.maximum !== undefined && total > bounds.maximum) {
      out.push({
        instancePath: "",
        rulePath: `aggregates/${escapeToken(rule.key)}/sum/maximum`,
        rule: "aggregates",
        message: `sum of "${rule.key}" is ${total}, exceeds maximum ${bounds.maximum}`,
      });
    }
  }

  private validateItem(
    value: Json,
    instancePath: string,
    rulePath: string,
    out: ValidationIssue[],
  ): void {
    if (this.schema.items) {
      this.validateValue(value, this.schema.items, instancePath, "items", out);
      return;
    }
    this.validateValue(value, this.schema, instancePath, rulePath, out);
  }

  private validateValue(
    value: Json,
    schema: Schema,
    instancePath: string,
    rulePath: string,
    out: ValidationIssue[],
  ): void {
    if (schema.type !== undefined && !matchesType(value, schema.type)) {
      out.push({
        instancePath,
        rulePath: `${rulePath}/type`,
        rule: "type",
        message: `expected type "${schema.type}", got "${typeOf(value)}"`,
      });
      return;
    }

    if (schema.enum !== undefined && !schema.enum.some((option) => jsonEqual(option, value))) {
      out.push({
        instancePath,
        rulePath: `${rulePath}/enum`,
        rule: "enum",
        message: "value is not one of the allowed enum values",
      });
    }

    if (typeof value === "string") {
      if (schema.minLength !== undefined && value.length < schema.minLength) {
        out.push({
          instancePath,
          rulePath: `${rulePath}/minLength`,
          rule: "minLength",
          message: `string length ${value.length} is below minLength ${schema.minLength}`,
        });
      }
      if (schema.maxLength !== undefined && value.length > schema.maxLength) {
        out.push({
          instancePath,
          rulePath: `${rulePath}/maxLength`,
          rule: "maxLength",
          message: `string length ${value.length} exceeds maxLength ${schema.maxLength}`,
        });
      }
      if (schema.pattern !== undefined) {
        const re = new RegExp(schema.pattern, "u");
        if (!re.test(value)) {
          out.push({
            instancePath,
            rulePath: `${rulePath}/pattern`,
            rule: "pattern",
            message: `string does not match pattern ${schema.pattern}`,
          });
        }
      }
    }

    if (typeof value === "number") {
      if (schema.minimum !== undefined && value < schema.minimum) {
        out.push({
          instancePath,
          rulePath: `${rulePath}/minimum`,
          rule: "minimum",
          message: `value ${value} is below minimum ${schema.minimum}`,
        });
      }
      if (schema.maximum !== undefined && value > schema.maximum) {
        out.push({
          instancePath,
          rulePath: `${rulePath}/maximum`,
          rule: "maximum",
          message: `value ${value} exceeds maximum ${schema.maximum}`,
        });
      }
    }

    if (typeOf(value) === "object") {
      const obj = value as Record<string, Json>;
      for (const requiredKey of schema.required ?? []) {
        if (!(requiredKey in obj)) {
          out.push({
            instancePath: `${instancePath}/${escapeToken(requiredKey)}`,
            rulePath: `${rulePath}/required`,
            rule: "required",
            message: `missing required property "${requiredKey}"`,
          });
        }
      }
      for (const [property, propertySchema] of Object.entries(schema.properties ?? {})) {
        if (property in obj) {
          this.validateValue(
            obj[property],
            propertySchema,
            `${instancePath}/${escapeToken(property)}`,
            `${rulePath}/properties/${escapeToken(property)}`,
            out,
          );
        }
      }
    }

    if (typeOf(value) === "array" && schema.items) {
      const arr = value as Json[];
      arr.forEach((element, i) => {
        this.validateValue(
          element,
          schema.items!,
          `${instancePath}/${i}`,
          `${rulePath}/items`,
          out,
        );
      });
    }

  }

  private checkUniqueKeys(
    value: Json,
    instancePath: string,
    out: ValidationIssue[],
  ): void {
    for (const key of this.schema.uniqueKeys ?? []) {
      const raw = readPath(value, key);
      if (raw === undefined) continue;
      const serialized = serializeKey(raw);
      const known = this.seen.get(key)!;
      if (known.has(serialized)) {
        const fieldPath = `${instancePath}/${key.split(".").map(escapeToken).join("/")}`;
        out.push({
          instancePath: fieldPath,
          rulePath: `uniqueKeys/${escapeToken(key)}`,
          rule: "uniqueKeys",
          message: `duplicate value for unique key "${key}": ${displayValue(raw)}`,
        });
      }
    }
  }
}

function readPath(value: Json, dottedPath: string): Json | undefined {
  const parts = dottedPath.split(".");
  let current: Json = value;
  for (const part of parts) {
    if (current !== null && typeof current === "object" && !Array.isArray(current)) {
      if (!(part in (current as Record<string, Json>))) return undefined;
      current = (current as Record<string, Json>)[part];
    } else {
      return undefined;
    }
  }
  return current;
}

function serializeKey(value: Json): string {
  return JSON.stringify(value);
}

function displayValue(value: Json): string {
  return typeof value === "string" ? `"${value}"` : String(value);
}

function jsonEqual(a: Json, b: Json): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function validateArray(items: Iterable<Json>, schema: Schema, onError: OnError = "collect"): ValidationResult {
  const validator = new StreamArrayValidator(schema, onError);
  for (const item of items) {
    validator.push(item);
  }
  return validator.finish();
}
