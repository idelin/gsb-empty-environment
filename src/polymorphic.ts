/**
 * Strict polymorphic JSON decoding for TypeScript services.
 *
 * The wire format is the one used by kotlinx.serialization's polymorphic
 * serializer: an object carries a string discriminator ("type") whose value
 * selects a registered subclass. Unlike Json.parse, this decoder also sees
 * duplicate object keys and can therefore reject them before a value is
 * silently overwritten.
 */

export const DISCRIMINATOR = "type";
export const MAX_POLYMORPHIC_DEPTH = 6;

export type DecodeErrorCode =
  | "INVALID_JSON"
  | "DUPLICATE_KEY"
  | "INVALID_OBJECT"
  | "MISSING_TYPE"
  | "INVALID_TYPE"
  | "UNKNOWN_TYPE"
  | "UNKNOWN_FIELD"
  | "MISSING_FIELD"
  | "INVALID_VALUE"
  | "DEPTH_LIMIT";

export type RegistrationErrorCode =
  | "INVALID_SERIAL_NAME"
  | "DUPLICATE_SERIAL_NAME"
  | "DISCRIMINATOR_CONFLICT"
  | "INVALID_VARIANT";

export interface DecodeContext {
  /** Number of polymorphic dispatches performed so far. */
  readonly depth: number;
  /** Location of the current value for diagnostics. */
  readonly path: string;
}

export interface Decoder<T> {
  readonly decode: (value: unknown, context: DecodeContext) => T;
  /**
   * When true, an absent object property is passed to decode as undefined
   * instead of being reported as MISSING_FIELD.
   */
  readonly allowsMissing?: boolean;
}

export type FieldSpec = Readonly<Record<string, Decoder<unknown>>>;
export type FieldValue<D> = D extends Decoder<infer T> ? T : never;
export type FieldValues<F extends FieldSpec> = {
  [K in keyof F]: FieldValue<F[K]>;
};

export interface VariantDefinition<TBase, F extends FieldSpec> {
  /** Fields of the subclass. The discriminator "type" must not be listed. */
  readonly fields: F;
  /** Builds the domain object from already-validated field values. */
  readonly decode: (fields: FieldValues<F>, context: DecodeContext) => TBase;
}

export class PolymorphicDecodeError extends Error {
  readonly code: DecodeErrorCode;
  readonly path: string;
  readonly offset: number | undefined;

  constructor(
    code: DecodeErrorCode,
    message: string,
    options: { path?: string; offset?: number; cause?: unknown } = {},
  ) {
    super(message);
    this.name = "PolymorphicDecodeError";
    this.code = code;
    this.path = options.path ?? "$";
    this.offset = options.offset;
    if (options.cause !== undefined) {
      (this as Error & { cause?: unknown }).cause = options.cause;
    }
  }
}

export class PolymorphicRegistrationError extends Error {
  readonly code: RegistrationErrorCode;

  constructor(code: RegistrationErrorCode, message: string) {
    super(message);
    this.name = "PolymorphicRegistrationError";
    this.code = code;
  }
}

interface InternalVariant<TBase> {
  readonly fields: Record<string, Decoder<unknown>>;
  readonly decode: (fields: Record<string, unknown>, context: DecodeContext) => TBase;
}

const ROOT_CONTEXT: DecodeContext = Object.freeze({ depth: 0, path: "$" });

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function ownStringKeys(value: object): string[] {
  return Object.getOwnPropertyNames(value);
}

function isJsonObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function propertyPath(parent: string, key: string): string {
  if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
    return `${parent}.${key}`;
  }
  return `${parent}[${JSON.stringify(key)}]`;
}

function indexPath(parent: string, index: number): string {
  return `${parent}[${index}]`;
}

function describeValue(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (typeof value === "string") return "string";
  if (typeof value === "number") return "number";
  if (typeof value === "boolean") return "boolean";
  if (typeof value === "object") return "object";
  return typeof value;
}

function invalidValue(expected: string, value: unknown, context: DecodeContext): never {
  throw new PolymorphicDecodeError(
    "INVALID_VALUE",
    `Expected ${expected} at ${context.path}, received ${describeValue(value)}`,
    { path: context.path },
  );
}

function defineField(target: Record<string, unknown>, key: string, value: unknown): void {
  // defineProperty keeps keys such as "__proto__" as plain data properties.
  Object.defineProperty(target, key, {
    value,
    enumerable: true,
    configurable: true,
    writable: true,
  });
}

class StrictJsonParser {
  readonly #text: string;
  readonly #length: number;
  #index = 0;

  constructor(text: string) {
    this.#text = text;
    this.#length = text.length;
  }

  parse(): unknown {
    this.#skipWhitespace();
    const value = this.#parseValue(0, "$");
    this.#skipWhitespace();
    if (this.#index !== this.#length) {
      this.#fail("Unexpected trailing characters", "$", this.#index);
    }
    return value;
  }

  #fail(message: string, path: string, offset: number, code: DecodeErrorCode = "INVALID_JSON"): never {
    throw new PolymorphicDecodeError(code, `${message} at offset ${offset}`, {
      path,
      offset,
    });
  }

  #peek(): string {
    return this.#text[this.#index] ?? "";
  }

  #skipWhitespace(): void {
    while (this.#index < this.#length) {
      const code = this.#text.charCodeAt(this.#index);
      if (code === 0x20 || code === 0x09 || code === 0x0a || code === 0x0d) {
        this.#index += 1;
      } else {
        return;
      }
    }
  }

  #parseValue(depth: number, path: string): unknown {
    if (depth > 256) {
      this.#fail("JSON nesting exceeds the supported limit", path, this.#index);
    }
    this.#skipWhitespace();
    const character = this.#peek();
    switch (character) {
      case "{":
        return this.#parseObject(depth, path);
      case "[":
        return this.#parseArray(depth, path);
      case '"':
        return this.#parseString(path);
      case "t":
        return this.#parseKeyword("true", true, path);
      case "f":
        return this.#parseKeyword("false", false, path);
      case "n":
        return this.#parseKeyword("null", null, path);
      default:
        if (character === "-" || (character >= "0" && character <= "9")) {
          return this.#parseNumber(path);
        }
        this.#fail("Expected a JSON value", path, this.#index);
    }
  }

  #parseKeyword(keyword: string, value: unknown, path: string): unknown {
    if (this.#text.slice(this.#index, this.#index + keyword.length) !== keyword) {
      this.#fail(`Expected "${keyword}"`, path, this.#index);
    }
    this.#index += keyword.length;
    return value;
  }

  #parseObject(depth: number, path: string): Record<string, unknown> {
    this.#index += 1;
    const result: Record<string, unknown> = {};
    const seen = new Set<string>();

    this.#skipWhitespace();
    if (this.#peek() === "}") {
      this.#index += 1;
      return result;
    }

    while (true) {
      this.#skipWhitespace();
      if (this.#peek() !== '"') {
        this.#fail("Expected an object key string", path, this.#index);
      }
      const keyOffset = this.#index;
      const key = this.#parseString(path);
      const keyPath = propertyPath(path, key);

      if (seen.has(key)) {
        this.#fail(`Duplicate object key ${JSON.stringify(key)}`, keyPath, keyOffset, "DUPLICATE_KEY");
      }
      seen.add(key);

      this.#skipWhitespace();
      if (this.#peek() !== ":") {
        this.#fail("Expected ':' after object key", keyPath, this.#index);
      }
      this.#index += 1;

      const value = this.#parseValue(depth + 1, keyPath);
      defineField(result, key, value);
      this.#skipWhitespace();

      const separator = this.#peek();
      if (separator === ",") {
        this.#index += 1;
        continue;
      }
      if (separator === "}") {
        this.#index += 1;
        return result;
      }
      this.#fail("Expected ',' or '}' in object", path, this.#index);
    }
  }

  #parseArray(depth: number, path: string): unknown[] {
    this.#index += 1;
    const result: unknown[] = [];

    this.#skipWhitespace();
    if (this.#peek() === "]") {
      this.#index += 1;
      return result;
    }

    while (true) {
      const itemPath = indexPath(path, result.length);
      result.push(this.#parseValue(depth + 1, itemPath));
      this.#skipWhitespace();

      const separator = this.#peek();
      if (separator === ",") {
        this.#index += 1;
        continue;
      }
      if (separator === "]") {
        this.#index += 1;
        return result;
      }
      this.#fail("Expected ',' or ']' in array", path, this.#index);
    }
  }

  #parseString(path: string): string {
    const start = this.#index;
    this.#index += 1;
    let chunkStart = this.#index;
    let result = "";

    while (this.#index < this.#length) {
      const code = this.#text.charCodeAt(this.#index);
      if (code === 0x22) {
        result += this.#text.slice(chunkStart, this.#index);
        this.#index += 1;
        return result;
      }
      if (code === 0x5c) {
        result += this.#text.slice(chunkStart, this.#index);
        this.#index += 1;
        const escapeOffset = this.#index;
        const escaped = this.#text[this.#index];
        switch (escaped) {
          case '"':
          case "\\":
          case "/":
            result += escaped;
            this.#index += 1;
            break;
          case "b":
            result += "\b";
            this.#index += 1;
            break;
          case "f":
            result += "\f";
            this.#index += 1;
            break;
          case "n":
            result += "\n";
            this.#index += 1;
            break;
          case "r":
            result += "\r";
            this.#index += 1;
            break;
          case "t":
            result += "\t";
            this.#index += 1;
            break;
          case "u": {
            const hex = this.#text.slice(this.#index + 1, this.#index + 5);
            if (!/^[0-9A-Fa-f]{4}$/.test(hex)) {
              this.#fail("Invalid unicode escape", path, escapeOffset);
            }
            result += String.fromCharCode(Number.parseInt(hex, 16));
            this.#index += 5;
            break;
          }
          default:
            this.#fail("Invalid string escape", path, escapeOffset);
        }
        chunkStart = this.#index;
        continue;
      }
      if (code <= 0x1f) {
        this.#fail("Unescaped control character in string", path, this.#index);
      }
      this.#index += 1;
    }

    this.#fail("Unterminated string", path, start);
  }

  #parseNumber(path: string): number {
    const start = this.#index;
    const match = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(
      this.#text.slice(this.#index),
    );
    if (match === null) {
      this.#fail("Invalid number", path, this.#index);
    }
    this.#index += match[0].length;
    const value = Number(match[0]);
    if (!Number.isFinite(value)) {
      this.#fail("Number is outside the finite range", path, start);
    }
    return value;
  }
}

/**
 * Parses JSON while rejecting duplicate object keys at every nesting level.
 * This is the input path used by PolymorphicBase.decode.
 */
export function parseStrictJson(text: string): unknown {
  if (typeof text !== "string") {
    throw new PolymorphicDecodeError("INVALID_JSON", "JSON input must be a string");
  }
  return new StrictJsonParser(text).parse();
}

export const strictJsonParse = parseStrictJson;

function decodeStrictObject<F extends FieldSpec>(
  fields: F,
  value: unknown,
  context: DecodeContext,
): FieldValues<F> {
  if (!isJsonObject(value)) {
    throw new PolymorphicDecodeError(
      "INVALID_OBJECT",
      `Expected an object at ${context.path}, received ${describeValue(value)}`,
      { path: context.path },
    );
  }

  const result: Record<string, unknown> = {};
  for (const key of ownStringKeys(value)) {
    if (!hasOwn(fields, key)) {
      throw new PolymorphicDecodeError(
        "UNKNOWN_FIELD",
        `Unknown field ${JSON.stringify(key)} at ${propertyPath(context.path, key)}`,
        { path: propertyPath(context.path, key) },
      );
    }
    const decoder = fields[key];
    defineField(
      result,
      key,
      decoder.decode(value[key], {
        depth: context.depth,
        path: propertyPath(context.path, key),
      }),
    );
  }

  for (const key of ownStringKeys(fields)) {
    if (hasOwn(result, key)) {
      continue;
    }
    const decoder = fields[key];
    if (decoder.allowsMissing === true) {
      defineField(
        result,
        key,
        decoder.decode(undefined, {
          depth: context.depth,
          path: propertyPath(context.path, key),
        }),
      );
      continue;
    }
    throw new PolymorphicDecodeError(
      "MISSING_FIELD",
      `Missing required field ${JSON.stringify(key)} at ${propertyPath(context.path, key)}`,
      { path: propertyPath(context.path, key) },
    );
  }

  return result as FieldValues<F>;
}

export class PolymorphicBase<TBase> implements Decoder<TBase> {
  readonly #variants = new Map<string, InternalVariant<TBase>>();

  get serialNames(): readonly string[] {
    return Object.freeze([...this.#variants.keys()]);
  }

  register<F extends FieldSpec>(
    serialName: string,
    definition: VariantDefinition<TBase, F>,
  ): this {
    if (typeof serialName !== "string" || serialName.length === 0) {
      throw new PolymorphicRegistrationError(
        "INVALID_SERIAL_NAME",
        "A polymorphic serial name must be a non-empty string",
      );
    }
    if (this.#variants.has(serialName)) {
      throw new PolymorphicRegistrationError(
        "DUPLICATE_SERIAL_NAME",
        `Serial name ${JSON.stringify(serialName)} is already registered`,
      );
    }
    if (
      typeof definition !== "object" ||
      definition === null ||
      typeof definition.fields !== "object" ||
      definition.fields === null ||
      Array.isArray(definition.fields) ||
      typeof definition.decode !== "function"
    ) {
      throw new PolymorphicRegistrationError(
        "INVALID_VARIANT",
        `Variant ${JSON.stringify(serialName)} must provide fields and a decode function`,
      );
    }
    if (hasOwn(definition.fields, DISCRIMINATOR)) {
      throw new PolymorphicRegistrationError(
        "DISCRIMINATOR_CONFLICT",
        `Variant ${JSON.stringify(serialName)} declares a field named "${DISCRIMINATOR}"`,
      );
    }

    const fields: Record<string, Decoder<unknown>> = {};
    for (const key of ownStringKeys(definition.fields)) {
      const decoder = definition.fields[key];
      if (
        typeof decoder !== "object" ||
        decoder === null ||
        typeof decoder.decode !== "function"
      ) {
        throw new PolymorphicRegistrationError(
          "INVALID_VARIANT",
          `Field ${JSON.stringify(key)} of variant ${JSON.stringify(serialName)} is not a decoder`,
        );
      }
      defineField(fields, key, decoder);
    }
    Object.freeze(fields);

    this.#variants.set(serialName, {
      fields,
      decode: definition.decode as InternalVariant<TBase>["decode"],
    });
    return this;
  }

  /**
   * Decodes either serialized JSON (a string) or an already parsed value.
   * Duplicate-key rejection is only possible for serialized input; a plain
   * JavaScript object cannot retain duplicate keys.
   */
  decode(input: string | unknown, context: DecodeContext = ROOT_CONTEXT): TBase {
    const value = typeof input === "string" ? parseStrictJson(input) : input;
    return this.decodeValue(value, context);
  }

  decodeValue(value: unknown, context: DecodeContext = ROOT_CONTEXT): TBase {
    if (!isJsonObject(value)) {
      throw new PolymorphicDecodeError(
        "INVALID_OBJECT",
        `Expected a polymorphic object at ${context.path}, received ${describeValue(value)}`,
        { path: context.path },
      );
    }

    const depth = context.depth + 1;
    if (depth > MAX_POLYMORPHIC_DEPTH) {
      throw new PolymorphicDecodeError(
        "DEPTH_LIMIT",
        `Polymorphic nesting exceeds the limit of ${MAX_POLYMORPHIC_DEPTH} at ${context.path}`,
        { path: context.path },
      );
    }

    if (!hasOwn(value, DISCRIMINATOR)) {
      throw new PolymorphicDecodeError(
        "MISSING_TYPE",
        `Missing "${DISCRIMINATOR}" discriminator at ${context.path}`,
        { path: context.path },
      );
    }

    const serialName = value[DISCRIMINATOR];
    if (typeof serialName !== "string") {
      throw new PolymorphicDecodeError(
        "INVALID_TYPE",
        `Discriminator "${DISCRIMINATOR}" must be a string at ${context.path}`,
        { path: propertyPath(context.path, DISCRIMINATOR) },
      );
    }

    const variant = this.#variants.get(serialName);
    if (variant === undefined) {
      throw new PolymorphicDecodeError(
        "UNKNOWN_TYPE",
        `Unknown ${DISCRIMINATOR} ${JSON.stringify(serialName)} at ${context.path}`,
        { path: propertyPath(context.path, DISCRIMINATOR) },
      );
    }

    const result: Record<string, unknown> = {};
    for (const key of ownStringKeys(value)) {
      if (key === DISCRIMINATOR) {
        continue;
      }
      if (!hasOwn(variant.fields, key)) {
        throw new PolymorphicDecodeError(
          "UNKNOWN_FIELD",
          `Unknown field ${JSON.stringify(key)} for ${DISCRIMINATOR} ${JSON.stringify(serialName)}`,
          { path: propertyPath(context.path, key) },
        );
      }
      const decoder = variant.fields[key];
      defineField(
        result,
        key,
        decoder.decode(value[key], {
          depth,
          path: propertyPath(context.path, key),
        }),
      );
    }

    for (const key of ownStringKeys(variant.fields)) {
      if (hasOwn(result, key)) {
        continue;
      }
      const decoder = variant.fields[key];
      if (decoder.allowsMissing === true) {
        defineField(
          result,
          key,
          decoder.decode(undefined, {
            depth,
            path: propertyPath(context.path, key),
          }),
        );
        continue;
      }
      throw new PolymorphicDecodeError(
        "MISSING_FIELD",
        `Missing required field ${JSON.stringify(key)} for ${DISCRIMINATOR} ${JSON.stringify(serialName)}`,
        { path: propertyPath(context.path, key) },
      );
    }

    return variant.decode(result, { depth, path: context.path });
  }
}

/** Creates an independent base-class registry. */
export function polymorphic<TBase>(): PolymorphicBase<TBase> {
  return new PolymorphicBase<TBase>();
}

export const createPolymorphicBase = polymorphic;

export function stringField(): Decoder<string> {
  return {
    decode(value, context) {
      if (typeof value !== "string") {
        return invalidValue("string", value, context);
      }
      return value;
    },
  };
}

export function numberField(): Decoder<number> {
  return {
    decode(value, context) {
      if (typeof value !== "number" || !Number.isFinite(value)) {
        return invalidValue("finite number", value, context);
      }
      return value;
    },
  };
}

export function booleanField(): Decoder<boolean> {
  return {
    decode(value, context) {
      if (typeof value !== "boolean") {
        return invalidValue("boolean", value, context);
      }
      return value;
    },
  };
}

export function literalField<const T extends string | number | boolean | null>(
  ...allowed: T[]
): Decoder<T> {
  return {
    decode(value, context) {
      if (!allowed.some((candidate) => Object.is(candidate, value))) {
        return invalidValue(
          allowed.map((candidate) => JSON.stringify(candidate)).join(" or "),
          value,
          context,
        );
      }
      return value as T;
    },
  };
}

export function arrayField<T>(itemDecoder: Decoder<T>): Decoder<T[]> {
  return {
    decode(value, context) {
      if (!Array.isArray(value)) {
        return invalidValue("array", value, context);
      }
      return value.map((item, index) =>
        itemDecoder.decode(item, {
          depth: context.depth,
          path: indexPath(context.path, index),
        }),
      );
    },
  };
}

export function optionalField<T>(decoder: Decoder<T>): Decoder<T | undefined> {
  return {
    allowsMissing: true,
    decode(value, context) {
      if (value === undefined) {
        return undefined;
      }
      return decoder.decode(value, context);
    },
  };
}

export function nullableField<T>(decoder: Decoder<T>): Decoder<T | null> {
  return {
    decode(value, context) {
      if (value === null) {
        return null;
      }
      return decoder.decode(value, context);
    },
  };
}

export function objectField<F extends FieldSpec, T = FieldValues<F>>(
  fields: F,
  build?: (values: FieldValues<F>, context: DecodeContext) => T,
): Decoder<T> {
  return {
    decode(value, context) {
      const values = decodeStrictObject(fields, value, context);
      return build === undefined ? (values as unknown as T) : build(values, context);
    },
  };
}

/** Short namespace for the common field decoders. */
export const f = Object.freeze({
  string: stringField,
  number: numberField,
  boolean: booleanField,
  literal: literalField,
  array: arrayField,
  optional: optionalField,
  nullable: nullableField,
  object: objectField,
});
