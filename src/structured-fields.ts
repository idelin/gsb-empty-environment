export type StructuredFieldBareItem =
  | { readonly type: "token"; readonly value: string }
  | { readonly type: "string"; readonly value: string }
  | { readonly type: "integer"; readonly value: number }
  | { readonly type: "decimal"; readonly value: number }
  | { readonly type: "binary"; readonly value: string }
  | { readonly type: "boolean"; readonly value: boolean };

export interface StructuredFieldItem {
  readonly item: StructuredFieldBareItem;
  readonly parameters: ReadonlyMap<string, StructuredFieldBareItem>;
}

interface Cursor {
  index: number;
}

const isAlpha = (character: string | undefined): boolean =>
  character !== undefined &&
  ((character >= "A" && character <= "Z") ||
    (character >= "a" && character <= "z"));

const isLowerAlpha = (character: string | undefined): boolean =>
  character !== undefined && character >= "a" && character <= "z";

const isDigit = (character: string | undefined): boolean =>
  character !== undefined && character >= "0" && character <= "9";

const isTokenStart = (character: string | undefined): boolean =>
  character === "*" || isAlpha(character);

const isTokenCharacter = (character: string | undefined): boolean => {
  if (isTokenStart(character) || isDigit(character)) {
    return true;
  }

  switch (character) {
    case "!":
    case "#":
    case "$":
    case "%":
    case "&":
    case "'":
    case "+":
    case "-":
    case ".":
    case "^":
    case "_":
    case "`":
    case "|":
    case "~":
      return true;
    default:
      return false;
  }
};

const isKeyStart = (character: string | undefined): boolean =>
  character === "*" || isLowerAlpha(character);

const isKeyCharacter = (character: string | undefined): boolean =>
  isKeyStart(character) || isDigit(character) || character === "_" ||
  character === "-" || character === ".";

const isStringCharacter = (
  character: string | undefined,
): character is string => {
  if (character === undefined) {
    return false;
  }

  const code = character.charCodeAt(0);
  return (code >= 0x20 && code <= 0x21) ||
    (code >= 0x23 && code <= 0x5b) ||
    (code >= 0x5d && code <= 0x7e);
};

const skipSpaces = (input: string, cursor: Cursor): void => {
  while (input[cursor.index] === " ") {
    cursor.index += 1;
  }
};

const parseToken = (
  input: string,
  cursor: Cursor,
): StructuredFieldBareItem | null => {
  if (!isTokenStart(input[cursor.index] ?? "")) {
    return null;
  }

  const start = cursor.index;
  cursor.index += 1;
  while (
    cursor.index < input.length &&
    isTokenCharacter(input[cursor.index])
  ) {
    cursor.index += 1;
  }

  return { type: "token", value: input.slice(start, cursor.index) };
};

const parseString = (
  input: string,
  cursor: Cursor,
): StructuredFieldBareItem | null => {
  if (input[cursor.index] !== '"') {
    return null;
  }

  cursor.index += 1;
  let value = "";

  while (cursor.index < input.length) {
    const character = input[cursor.index];
    if (character === '"') {
      cursor.index += 1;
      return { type: "string", value };
    }

    if (character === "\\") {
      const escaped = input[cursor.index + 1];
      if (escaped !== '"' && escaped !== "\\") {
        return null;
      }
      value += escaped;
      cursor.index += 2;
      continue;
    }

    if (!isStringCharacter(character)) {
      return null;
    }

    value += character;
    cursor.index += 1;
  }

  return null;
};

const parseNumber = (
  input: string,
  cursor: Cursor,
): StructuredFieldBareItem | null => {
  const start = cursor.index;
  if (input[cursor.index] === "-") {
    cursor.index += 1;
  }

  const integerStart = cursor.index;
  while (cursor.index < input.length && isDigit(input[cursor.index])) {
    cursor.index += 1;
  }

  const integerDigits = cursor.index - integerStart;
  if (integerDigits === 0) {
    return null;
  }

  if (input[cursor.index] === ".") {
    cursor.index += 1;
    const fractionStart = cursor.index;
    while (cursor.index < input.length && isDigit(input[cursor.index])) {
      cursor.index += 1;
    }

    const fractionDigits = cursor.index - fractionStart;
    if (
      integerDigits > 12 ||
      fractionDigits === 0 ||
      fractionDigits > 3
    ) {
      return null;
    }

    return {
      type: "decimal",
      value: Number(input.slice(start, cursor.index)),
    };
  }

  if (integerDigits > 15) {
    return null;
  }

  return {
    type: "integer",
    value: Number(input.slice(start, cursor.index)),
  };
};

const parseBinary = (
  input: string,
  cursor: Cursor,
): StructuredFieldBareItem | null => {
  if (input[cursor.index] !== ":") {
    return null;
  }

  cursor.index += 1;
  const start = cursor.index;

  while (cursor.index < input.length && input[cursor.index] !== ":") {
    const character = input[cursor.index];
    if (
      !isAlpha(character) &&
      !isDigit(character) &&
      character !== "+" &&
      character !== "/" &&
      character !== "="
    ) {
      return null;
    }
    cursor.index += 1;
  }

  if (input[cursor.index] !== ":") {
    return null;
  }

  const value = input.slice(start, cursor.index);
  cursor.index += 1;
  return { type: "binary", value };
};

const parseBoolean = (
  input: string,
  cursor: Cursor,
): StructuredFieldBareItem | null => {
  if (input[cursor.index] !== "?") {
    return null;
  }

  const value = input[cursor.index + 1];
  if (value !== "0" && value !== "1") {
    return null;
  }

  cursor.index += 2;
  return { type: "boolean", value: value === "1" };
};

const parseBareItem = (
  input: string,
  cursor: Cursor,
): StructuredFieldBareItem | null => {
  const character = input[cursor.index] ?? "";

  if (character === '"') {
    return parseString(input, cursor);
  }
  if (character === ":") {
    return parseBinary(input, cursor);
  }
  if (character === "?") {
    return parseBoolean(input, cursor);
  }
  if (character === "-" || isDigit(character)) {
    return parseNumber(input, cursor);
  }

  return parseToken(input, cursor);
};

const parseKey = (input: string, cursor: Cursor): string | null => {
  if (!isKeyStart(input[cursor.index] ?? "")) {
    return null;
  }

  const start = cursor.index;
  cursor.index += 1;
  while (
    cursor.index < input.length &&
    isKeyCharacter(input[cursor.index])
  ) {
    cursor.index += 1;
  }

  return input.slice(start, cursor.index);
};

const parseParameters = (
  input: string,
  cursor: Cursor,
): Map<string, StructuredFieldBareItem> | null => {
  const parameters = new Map<string, StructuredFieldBareItem>();

  while (input[cursor.index] === ";") {
    cursor.index += 1;
    skipSpaces(input, cursor);

    const key = parseKey(input, cursor);
    if (key === null) {
      return null;
    }

    let value: StructuredFieldBareItem = { type: "boolean", value: true };
    if (input[cursor.index] === "=") {
      cursor.index += 1;
      const parsedValue = parseBareItem(input, cursor);
      if (parsedValue === null) {
        return null;
      }
      value = parsedValue;
    }

    parameters.set(key, value);
  }

  return parameters;
};

export const parseStructuredFieldItem = (
  input: string,
): StructuredFieldItem | null => {
  if (typeof input !== "string") {
    return null;
  }

  const cursor: Cursor = { index: 0 };
  skipSpaces(input, cursor);

  const item = parseBareItem(input, cursor);
  if (item === null) {
    return null;
  }

  const parameters = parseParameters(input, cursor);
  if (parameters === null) {
    return null;
  }

  skipSpaces(input, cursor);
  if (cursor.index !== input.length) {
    return null;
  }

  return { item, parameters };
};
