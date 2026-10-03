import { formatPath, parsePath, type PathSegment } from "./path.ts";
import type { JsonValue } from "./flatten.ts";

export interface ExtractStats {
  /** JSON value nodes actually constructed in memory. */
  materializedValues: number;
  /** Top-level values skipped without constructing anything. */
  skippedValues: number;
  /** Wanted paths that were not present in the document. */
  missingPaths: string[];
}

export interface ExtractResult {
  /** Leaf path -> extracted value (one entry per found wanted path). */
  values: Map<string, JsonValue>;
  stats: ExtractStats;
}

/**
 * Selectively extracts the given leaf paths from a raw JSON string.
 *
 * The scanner walks the text once. Subtrees that cannot contain a wanted
 * path are skipped by a raw character scan: no objects, arrays or scalars
 * are allocated for them, so extracting `user.address.city` never
 * deserializes e.g. a large `items` list in the same document.
 */
export function extractPathsFromRaw(
  raw: string,
  wantedPaths: readonly string[],
): ExtractResult {
  const wanted = new Set<string>();
  const ancestors = new Set<string>();
  for (const path of wantedPaths) {
    const segments = parsePath(path);
    wanted.add(formatPath(segments));
    for (let i = 0; i < segments.length; i += 1) {
      ancestors.add(formatPath(segments.slice(0, i)));
    }
  }

  const extractor = new SelectiveExtractor(raw, wanted, ancestors);
  return extractor.run();
}

class SelectiveExtractor {
  private cursor = 0;
  private readonly raw: string;
  private readonly wanted: ReadonlySet<string>;
  private readonly ancestors: ReadonlySet<string>;
  private readonly values = new Map<string, JsonValue>();
  private readonly stats: ExtractStats = {
    materializedValues: 0,
    skippedValues: 0,
    missingPaths: [],
  };

  constructor(
    raw: string,
    wanted: ReadonlySet<string>,
    ancestors: ReadonlySet<string>,
  ) {
    this.raw = raw;
    this.wanted = wanted;
    this.ancestors = ancestors;
  }

  run(): ExtractResult {
    this.visit([]);
    this.skipWhitespace();
    if (this.cursor !== this.raw.length) {
      throw this.error("Unexpected trailing content");
    }
    for (const path of this.wanted) {
      if (!this.values.has(path)) {
        this.stats.missingPaths.push(path);
      }
    }
    return { values: this.values, stats: this.stats };
  }

  /** Routes the value at `segments`: materialize, descend, or skip. */
  private visit(segments: readonly PathSegment[]): void {
    const path = formatPath(segments);
    if (this.wanted.has(path)) {
      this.values.set(path, this.parseValue());
      return;
    }
    this.skipWhitespace();
    const ch = this.raw[this.cursor];
    if (!this.ancestors.has(path)) {
      this.skipValue();
      this.stats.skippedValues += 1;
      return;
    }
    if (ch === "{") {
      this.cursor += 1;
      this.skipWhitespace();
      if (this.raw[this.cursor] === "}") {
        this.cursor += 1;
        return;
      }
      while (true) {
        this.skipWhitespace();
        const key = this.parseString();
        this.skipWhitespace();
        this.expect(":");
        this.visit([...segments, key]);
        this.skipWhitespace();
        const next = this.raw[this.cursor];
        if (next === ",") {
          this.cursor += 1;
          continue;
        }
        if (next === "}") {
          this.cursor += 1;
          return;
        }
        throw this.error("Expected ',' or '}' in object");
      }
    }
    if (ch === "[") {
      this.cursor += 1;
      this.skipWhitespace();
      if (this.raw[this.cursor] === "]") {
        this.cursor += 1;
        return;
      }
      let index = 0;
      while (true) {
        this.visit([...segments, index]);
        index += 1;
        this.skipWhitespace();
        const next = this.raw[this.cursor];
        if (next === ",") {
          this.cursor += 1;
          continue;
        }
        if (next === "]") {
          this.cursor += 1;
          return;
        }
        throw this.error("Expected ',' or ']' in array");
      }
    }
    // An ancestor path that is itself a scalar: nothing wanted can live
    // below it, so just consume the scalar.
    this.skipValue();
    this.stats.skippedValues += 1;
  }

  /** Full JSON parse that builds the value (used only on wanted paths). */
  private parseValue(): JsonValue {
    this.skipWhitespace();
    const ch = this.raw[this.cursor];
    this.stats.materializedValues += 1;
    if (ch === "{") {
      this.cursor += 1;
      const obj: { [key: string]: JsonValue } = {};
      this.skipWhitespace();
      if (this.raw[this.cursor] === "}") {
        this.cursor += 1;
        return obj;
      }
      while (true) {
        this.skipWhitespace();
        const key = this.parseString();
        this.skipWhitespace();
        this.expect(":");
        obj[key] = this.parseValue();
        this.skipWhitespace();
        const next = this.raw[this.cursor];
        if (next === ",") {
          this.cursor += 1;
          continue;
        }
        if (next === "}") {
          this.cursor += 1;
          return obj;
        }
        throw this.error("Expected ',' or '}' in object");
      }
    }
    if (ch === "[") {
      this.cursor += 1;
      const arr: JsonValue[] = [];
      this.skipWhitespace();
      if (this.raw[this.cursor] === "]") {
        this.cursor += 1;
        return arr;
      }
      while (true) {
        arr.push(this.parseValue());
        this.skipWhitespace();
        const next = this.raw[this.cursor];
        if (next === ",") {
          this.cursor += 1;
          continue;
        }
        if (next === "]") {
          this.cursor += 1;
          return arr;
        }
        throw this.error("Expected ',' or ']' in array");
      }
    }
    if (ch === '"') {
      return this.parseString();
    }
    if (ch === "t") {
      this.expectLiteral("true");
      return true;
    }
    if (ch === "f") {
      this.expectLiteral("false");
      return false;
    }
    if (ch === "n") {
      this.expectLiteral("null");
      return null;
    }
    return this.parseNumber();
  }

  /**
   * Advances past one JSON value using a raw character scan only.
   * Nothing is allocated for the skipped content.
   */
  private skipValue(): void {
    this.skipWhitespace();
    const ch = this.raw[this.cursor];
    if (ch === "{" || ch === "[") {
      let depth = 0;
      while (this.cursor < this.raw.length) {
        const c = this.raw[this.cursor];
        if (c === '"') {
          this.skipStringRaw();
          continue;
        }
        if (c === "{" || c === "[") {
          depth += 1;
        } else if (c === "}" || c === "]") {
          depth -= 1;
          if (depth === 0) {
            this.cursor += 1;
            return;
          }
        }
        this.cursor += 1;
      }
      throw this.error("Unterminated container while skipping");
    }
    if (ch === '"') {
      this.skipStringRaw();
      return;
    }
    // Scalar: consume until a structural delimiter.
    while (this.cursor < this.raw.length) {
      const c = this.raw[this.cursor];
      if (c === "," || c === "}" || c === "]" || c === " " ||
          c === "\t" || c === "\n" || c === "\r") {
        return;
      }
      this.cursor += 1;
    }
  }

  /** Moves past a string token without decoding it. */
  private skipStringRaw(): void {
    this.cursor += 1; // opening quote
    while (this.cursor < this.raw.length) {
      const c = this.raw[this.cursor];
      if (c === "\\") {
        this.cursor += 2;
        continue;
      }
      this.cursor += 1;
      if (c === '"') {
        return;
      }
    }
    throw this.error("Unterminated string");
  }

  private parseString(): string {
    if (this.raw[this.cursor] !== '"') {
      throw this.error("Expected string");
    }
    this.cursor += 1;
    let out = "";
    while (this.cursor < this.raw.length) {
      const c = this.raw[this.cursor];
      if (c === '"') {
        this.cursor += 1;
        return out;
      }
      if (c === "\\") {
        const esc = this.raw[this.cursor + 1];
        switch (esc) {
          case '"': out += '"'; this.cursor += 2; continue;
          case "\\": out += "\\"; this.cursor += 2; continue;
          case "/": out += "/"; this.cursor += 2; continue;
          case "b": out += "\b"; this.cursor += 2; continue;
          case "f": out += "\f"; this.cursor += 2; continue;
          case "n": out += "\n"; this.cursor += 2; continue;
          case "r": out += "\r"; this.cursor += 2; continue;
          case "t": out += "\t"; this.cursor += 2; continue;
          case "u": {
            const hex = this.raw.slice(this.cursor + 2, this.cursor + 6);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
              throw this.error("Invalid \\u escape");
            }
            out += String.fromCharCode(parseInt(hex, 16));
            this.cursor += 6;
            continue;
          }
          default:
            throw this.error(`Invalid escape '\\${esc}'`);
        }
      }
      out += c;
      this.cursor += 1;
    }
    throw this.error("Unterminated string");
  }

  private parseNumber(): number {
    const start = this.cursor;
    if (this.raw[this.cursor] === "-") {
      this.cursor += 1;
    }
    while (this.cursor < this.raw.length && /[0-9]/.test(this.raw[this.cursor])) {
      this.cursor += 1;
    }
    if (this.raw[this.cursor] === ".") {
      this.cursor += 1;
      while (this.cursor < this.raw.length && /[0-9]/.test(this.raw[this.cursor])) {
        this.cursor += 1;
      }
    }
    if (this.raw[this.cursor] === "e" || this.raw[this.cursor] === "E") {
      this.cursor += 1;
      if (this.raw[this.cursor] === "+" || this.raw[this.cursor] === "-") {
        this.cursor += 1;
      }
      while (this.cursor < this.raw.length && /[0-9]/.test(this.raw[this.cursor])) {
        this.cursor += 1;
      }
    }
    if (this.cursor === start) {
      throw this.error("Invalid number");
    }
    return Number(this.raw.slice(start, this.cursor));
  }

  private expectLiteral(literal: string): void {
    if (this.raw.startsWith(literal, this.cursor)) {
      this.cursor += literal.length;
      return;
    }
    throw this.error(`Expected '${literal}'`);
  }

  private expect(ch: string): void {
    if (this.raw[this.cursor] !== ch) {
      throw this.error(`Expected '${ch}'`);
    }
    this.cursor += 1;
  }

  private skipWhitespace(): void {
    while (this.cursor < this.raw.length) {
      const c = this.raw[this.cursor];
      if (c === " " || c === "\t" || c === "\n" || c === "\r") {
        this.cursor += 1;
      } else {
        return;
      }
    }
  }

  private error(message: string): Error {
    return new SyntaxError(`${message} at offset ${this.cursor}`);
  }
}
