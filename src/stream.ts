import type { StreamSchema } from "./schema.ts";
import {
  canonicalize,
  escapePointer,
  validateValue,
  type ValidationError,
} from "./validate.ts";

export type ErrorMode = "fail-fast" | "collect";

export interface StreamResult {
  /** True only when finish() ran and no error was ever found. */
  valid: boolean;
  errors: ValidationError[];
  itemCount: number;
}

/** Thrown by push() in fail-fast mode; carries the errors that aborted the stream. */
export class StreamAbortedError extends Error {
  readonly errors: ValidationError[];
  constructor(errors: ValidationError[]) {
    super(`stream aborted with ${errors.length} error(s): ${errors[0]?.message}`);
    this.name = "StreamAbortedError";
    this.errors = errors;
  }
}

/**
 * Incremental validator for a large JSON array delivered item by item.
 *
 * - push(item) checks everything that is decidable per item: the item
 *   schema, maxItems, uniqueness (whole-item and uniqueKeys), aggregate max.
 * - finish() checks global conditions that a partial stream cannot prove:
 *   minItems and aggregate min. Only finish() may report overall success.
 *
 * Memory: O(number of distinct items) hashes for uniqueness — items
 * themselves are never retained.
 */
export class ArrayStreamValidator {
  private readonly schema: StreamSchema;
  private readonly mode: ErrorMode;
  private state: "open" | "finished" = "open";
  private count = 0;
  private readonly errors: ValidationError[] = [];
  private readonly seenItems = new Set<string>();
  private readonly seenKeys = new Map<string, Set<string>>();
  private readonly sums: number[];

  constructor(schema: StreamSchema, mode: ErrorMode = "collect") {
    this.schema = schema;
    this.mode = mode;
    this.sums = (schema.aggregates ?? []).map(() => 0);
    for (const key of schema.uniqueKeys ?? []) {
      this.seenKeys.set(key, new Set());
    }
  }

  /** Number of items accepted so far. */
  get itemCount(): number {
    return this.count;
  }

  /** "pending" until finish(); no overall verdict exists before that. */
  get verdict(): "pending" | "valid" | "invalid" {
    if (this.state !== "finished") return "pending";
    return this.errors.length === 0 ? "valid" : "invalid";
  }

  /** Errors observed so far (empty in fail-fast mode after abort — see thrown error). */
  get collectedErrors(): readonly ValidationError[] {
    return this.errors;
  }

  /**
   * Validate the next item. Returns the errors found for this push
   * (empty when the item is fine so far). In fail-fast mode the first
   * error closes the stream and throws StreamAbortedError instead.
   */
  push(item: unknown): ValidationError[] {
    if (this.state !== "open") {
      throw new Error("cannot push: stream is already finished");
    }
    const index = this.count;
    const itemPath = "/" + index;
    const found: ValidationError[] = [];
    const fail = (instancePath: string, schemaPath: string, message: string) => {
      found.push({ instancePath, schemaPath, message });
    };

    // Length: maxItems is decidable immediately; minItems only at finish().
    if (this.schema.maxItems !== undefined && index >= this.schema.maxItems) {
      fail(itemPath, "/maxItems", `must have <= ${this.schema.maxItems} items`);
    }

    // Per-item schema.
    validateValue(item, this.schema.items, itemPath, "/items", found);

    // Whole-item uniqueness (canonical hash, key-order independent).
    if (this.schema.uniqueItems === true) {
      const hash = canonicalize(item);
      if (this.seenItems.has(hash)) {
        fail(itemPath, "/uniqueItems", "must be unique (duplicates an earlier item)");
      } else {
        this.seenItems.add(hash);
      }
    }

    // Unique keys.
    for (const [key, seen] of this.seenKeys) {
      const value =
        item !== null && typeof item === "object"
          ? (item as Record<string, unknown>)[key]
          : undefined;
      if (value === undefined) {
        fail(itemPath, "/uniqueKeys", `missing unique key '${key}'`);
        continue;
      }
      const hash = canonicalize(value);
      if (seen.has(hash)) {
        fail(
          itemPath + "/" + escapePointer(key),
          "/uniqueKeys",
          `duplicate value for unique key '${key}'`,
        );
      } else {
        seen.add(hash);
      }
    }

    // Aggregates: max is decidable per push; min only at finish().
    (this.schema.aggregates ?? []).forEach((rule, i) => {
      const value =
        item !== null && typeof item === "object"
          ? (item as Record<string, unknown>)[rule.field]
          : undefined;
      if (typeof value !== "number") {
        fail(
          itemPath + "/" + escapePointer(rule.field),
          `/aggregates/${i}/field`,
          `aggregate field '${rule.field}' must be a number`,
        );
        return;
      }
      this.sums[i] += value;
      if (rule.max !== undefined && this.sums[i] > rule.max) {
        fail(
          itemPath + "/" + escapePointer(rule.field),
          `/aggregates/${i}/max`,
          `running sum of '${rule.field}' is ${this.sums[i]}, exceeds max ${rule.max}`,
        );
      }
    });

    this.count++;
    if (found.length > 0) {
      if (this.mode === "fail-fast") {
        this.state = "finished";
        throw new StreamAbortedError(found);
      }
      this.errors.push(...found);
    }
    return found;
  }

  /**
   * Close the stream and evaluate global conditions (minItems, aggregate
   * min). This is the only way to obtain an overall "valid" verdict.
   */
  finish(): StreamResult {
    if (this.state !== "open") {
      throw new Error("finish() already called");
    }
    this.state = "finished";
    const found: ValidationError[] = [];

    if (this.schema.minItems !== undefined && this.count < this.schema.minItems) {
      found.push({
        instancePath: "",
        schemaPath: "/minItems",
        message: `must have >= ${this.schema.minItems} items, got ${this.count}`,
      });
    }

    (this.schema.aggregates ?? []).forEach((rule, i) => {
      if (rule.min !== undefined && this.sums[i] < rule.min) {
        found.push({
          instancePath: "",
          schemaPath: `/aggregates/${i}/min`,
          message: `sum of '${rule.field}' is ${this.sums[i]}, below min ${rule.min}`,
        });
      }
    });

    this.errors.push(...found);
    return {
      valid: this.errors.length === 0,
      errors: [...this.errors],
      itemCount: this.count,
    };
  }
}
