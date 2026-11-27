import { JweSecurityError } from "./errors.ts";

export type StrictJsonValue =
  | null
  | boolean
  | number
  | string
  | StrictJsonValue[]
  | StrictJsonObject;

export interface StrictJsonObject {
  [key: string]: StrictJsonValue;
}

const WHITESPACE = new Set([0x20, 0x09, 0x0a, 0x0d]);
const HEX_PAIR = /^[0-9a-fA-F]{4}$/;
const MAX_JSON_DEPTH = 64;

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

export function isStrictJsonObject(
  value: StrictJsonValue,
): value is StrictJsonObject {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/**
 * Parses RFC 8259 JSON without relying on JSON.parse. This makes duplicate-key
 * rejection and strict object construction possible before any policy logic.
 */
export function parseStrictJson(text: string): StrictJsonValue {
  let position = 0;
  const length = text.length;

  const fail = (message: string, offset = position): never => {
    throw new JweSecurityError("JWE_HEADER_JSON_INVALID", message, { offset });
  };

  const skipWhitespace = (): void => {
    while (position < length && WHITESPACE.has(text.charCodeAt(position))) {
      position += 1;
    }
  };

  const parseString = (path: string): string => {
    if (text.charCodeAt(position) !== 0x22) {
      fail(`expected a JSON string at ${path}`);
    }
    position += 1;

    let result = "";
    while (position < length) {
      const code = text.charCodeAt(position);
      if (code === 0x22) {
        position += 1;
        return result;
      }
      if (code === 0x5c) {
        position += 1;
        if (position >= length) {
          fail(`unterminated escape sequence at ${path}`);
        }
        const escaped = text[position];
        position += 1;
        switch (escaped) {
          case '"':
          case "\\":
          case "/":
            result += escaped;
            break;
          case "b":
            result += "\b";
            break;
          case "f":
            result += "\f";
            break;
          case "n":
            result += "\n";
            break;
          case "r":
            result += "\r";
            break;
          case "t":
            result += "\t";
            break;
          case "u": {
            const hex = text.slice(position, position + 4);
            if (!HEX_PAIR.test(hex)) {
              fail(`invalid Unicode escape at ${path}`, position);
            }
            position += 4;
            const codeUnit = Number.parseInt(hex, 16);

            if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
              if (
                text[position] !== "\\" ||
                text[position + 1] !== "u"
              ) {
                fail(`unpaired high surrogate at ${path}`, position);
              }
              const lowHex = text.slice(position + 2, position + 6);
              if (!HEX_PAIR.test(lowHex)) {
                fail(`invalid low surrogate at ${path}`, position + 2);
              }
              const low = Number.parseInt(lowHex, 16);
              if (low < 0xdc00 || low > 0xdfff) {
                fail(`unpaired high surrogate at ${path}`, position);
              }
              result += String.fromCharCode(codeUnit, low);
              position += 6;
            } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
              fail(`unpaired low surrogate at ${path}`, position - 4);
            } else {
              result += String.fromCharCode(codeUnit);
            }
            break;
          }
          default:
            fail(`invalid escape sequence at ${path}`, position - 1);
        }
        continue;
      }

      if (code <= 0x1f) {
        fail(`unescaped control character at ${path}`, position);
      }
      result += text[position];
      position += 1;
    }

    return fail(`unterminated string at ${path}`, position);
  };

  const parseNumber = (): number => {
    const start = position;

    if (text[position] === "-") {
      position += 1;
    }
    if (position >= length) {
      fail("incomplete JSON number", start);
    }

    if (text[position] === "0") {
      position += 1;
      if (position < length && /\d/.test(text[position])) {
        fail("JSON numbers must not have leading zeroes", position);
      }
    } else if (text[position] >= "1" && text[position] <= "9") {
      position += 1;
      while (position < length && /\d/.test(text[position])) {
        position += 1;
      }
    } else {
      fail("invalid JSON number", start);
    }

    if (text[position] === ".") {
      position += 1;
      if (position >= length || !/\d/.test(text[position])) {
        fail("JSON fraction requires at least one digit", position);
      }
      while (position < length && /\d/.test(text[position])) {
        position += 1;
      }
    }

    if (text[position] === "e" || text[position] === "E") {
      position += 1;
      if (text[position] === "+" || text[position] === "-") {
        position += 1;
      }
      if (position >= length || !/\d/.test(text[position])) {
        fail("JSON exponent requires at least one digit", position);
      }
      while (position < length && /\d/.test(text[position])) {
        position += 1;
      }
    }

    const value = Number(text.slice(start, position));
    if (!Number.isFinite(value)) {
      fail("JSON number is outside the finite IEEE-754 range", start);
    }
    return value;
  };

  const parseObject = (path: string, depth: number): StrictJsonObject => {
    if (text[position] !== "{") {
      fail(`expected an object at ${path}`);
    }
    position += 1;
    skipWhitespace();

    const object = Object.create(null) as StrictJsonObject;
    if (text[position] === "}") {
      position += 1;
      return object;
    }

    while (true) {
      skipWhitespace();
      if (text[position] !== '"') {
        fail(`object keys must be strings at ${path}`);
      }
      const key = parseString(`${path}.key`);
      skipWhitespace();
      if (text[position] !== ":") {
        fail(`missing colon after key ${JSON.stringify(key)}`);
      }
      position += 1;
      skipWhitespace();

      if (hasOwn(object, key)) {
        throw new JweSecurityError(
          "JWE_HEADER_DUPLICATE_KEY",
          `duplicate JSON object key ${JSON.stringify(key)}`,
          { key, path },
        );
      }

      object[key] = parseValue(`${path}.${key}`, depth + 1);
      skipWhitespace();

      if (text[position] === ",") {
        position += 1;
        continue;
      }
      if (text[position] === "}") {
        position += 1;
        return object;
      }
      fail(`expected ',' or '}' at ${path}`);
    }
  };

  const parseArray = (path: string, depth: number): StrictJsonValue[] => {
    if (text[position] !== "[") {
      fail(`expected an array at ${path}`);
    }
    position += 1;
    skipWhitespace();

    const array: StrictJsonValue[] = [];
    if (text[position] === "]") {
      position += 1;
      return array;
    }

    let index = 0;
    while (true) {
      array.push(parseValue(`${path}[${index}]`, depth + 1));
      index += 1;
      skipWhitespace();

      if (text[position] === ",") {
        position += 1;
        continue;
      }
      if (text[position] === "]") {
        position += 1;
        return array;
      }
      fail(`expected ',' or ']' at ${path}`);
    }
  };

  const parseLiterals = (): StrictJsonValue | undefined => {
    if (text.startsWith("true", position)) {
      position += 4;
      return true;
    }
    if (text.startsWith("false", position)) {
      position += 5;
      return false;
    }
    if (text.startsWith("null", position)) {
      position += 4;
      return null;
    }
    return undefined;
  };

  const parseValue = (path: string, depth: number): StrictJsonValue => {
    if (depth > MAX_JSON_DEPTH) {
      fail(`JSON nesting exceeds the maximum depth of ${MAX_JSON_DEPTH}`, position);
    }
    skipWhitespace();
    const char = text[position];
    if (char === "{") {
      return parseObject(path, depth);
    }
    if (char === "[") {
      return parseArray(path, depth);
    }
    if (char === '"') {
      return parseString(path);
    }

    const literal = parseLiterals();
    if (literal !== undefined) {
      return literal;
    }
    if (char === "-" || (char >= "0" && char <= "9")) {
      return parseNumber();
    }
    return fail(`unexpected token at ${path}`);
  };

  const value = parseValue("$", 0);
  skipWhitespace();
  if (position !== length) {
    fail("trailing data after JSON value", position);
  }
  return value;
}
