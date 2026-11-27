import { JsonPathParseError } from "./errors.js";

export type TokenType =
  | "eof"
  | "dollar"
  | "at"
  | "dot"
  | "dotStar"
  | "recursive"
  | "lbracket"
  | "rbracket"
  | "lparen"
  | "rparen"
  | "comma"
  | "question"
  | "identifier"
  | "number"
  | "string"
  | "star"
  | "and"
  | "or"
  | "not"
  | "eq"
  | "ne"
  | "lt"
  | "le"
  | "gt"
  | "ge"
  | "plus"
  | "minus"
  | "slash"
  | "percent";

export interface Token {
  readonly type: TokenType;
  readonly value: string;
  readonly offset: number;
  readonly end: number;
}

const isDigit = (char: string): boolean => char >= "0" && char <= "9";

const isIdentifierStart = (char: string): boolean =>
  (char >= "a" && char <= "z") ||
  (char >= "A" && char <= "Z") ||
  char === "_" ||
  char === "$";

const isIdentifierPart = (char: string): boolean =>
  isIdentifierStart(char) || isDigit(char);

export function tokenize(source: string): Token[] {
  const tokens: Token[] = [];
  let index = 0;

  const push = (type: TokenType, value: string, start: number): void => {
    tokens.push({ type, value, offset: start, end: index });
  };

  while (index < source.length) {
    const char = source[index]!;

    if (/\s/u.test(char)) {
      index += 1;
      continue;
    }

    const start = index;

    if (char === "$") {
      index += 1;
      push("dollar", "$", start);
      continue;
    }

    if (char === "@") {
      index += 1;
      push("at", "@", start);
      continue;
    }

    if (char === ".") {
      if (source.startsWith(".**", index)) {
        index += 3;
        push("recursive", ".**", start);
      } else if (source.startsWith(".*", index)) {
        index += 2;
        push("dotStar", ".*", start);
      } else {
        index += 1;
        push("dot", ".", start);
      }
      continue;
    }

    if (char === "[") {
      index += 1;
      push("lbracket", "[", start);
      continue;
    }
    if (char === "]") {
      index += 1;
      push("rbracket", "]", start);
      continue;
    }
    if (char === "(") {
      index += 1;
      push("lparen", "(", start);
      continue;
    }
    if (char === ")") {
      index += 1;
      push("rparen", ")", start);
      continue;
    }
    if (char === ",") {
      index += 1;
      push("comma", ",", start);
      continue;
    }
    if (char === "?") {
      index += 1;
      push("question", "?", start);
      continue;
    }
    if (char === "*") {
      index += 1;
      push("star", "*", start);
      continue;
    }
    if (char === "+") {
      index += 1;
      push("plus", "+", start);
      continue;
    }
    if (char === "-") {
      index += 1;
      push("minus", "-", start);
      continue;
    }
    if (char === "/") {
      index += 1;
      push("slash", "/", start);
      continue;
    }
    if (char === "%") {
      index += 1;
      push("percent", "%", start);
      continue;
    }
    if (char === "!") {
      if (source[index + 1] === "=") {
        index += 2;
        push("ne", "!=", start);
      } else {
        index += 1;
        push("not", "!", start);
      }
      continue;
    }
    if (char === "&") {
      if (source[index + 1] === "&") {
        index += 2;
        push("and", "&&", start);
        continue;
      }
      throw new JsonPathParseError("expected '&&'", source, start);
    }
    if (char === "|") {
      if (source[index + 1] === "|") {
        index += 2;
        push("or", "||", start);
        continue;
      }
      throw new JsonPathParseError("expected '||'", source, start);
    }
    if (char === "=") {
      if (source[index + 1] === "=") {
        index += 2;
        push("eq", "==", start);
      } else {
        index += 1;
        push("eq", "=", start);
      }
      continue;
    }
    if (char === "<") {
      if (source[index + 1] === "=") {
        index += 2;
        push("le", "<=", start);
      } else if (source[index + 1] === ">") {
        index += 2;
        push("ne", "<>", start);
      } else {
        index += 1;
        push("lt", "<", start);
      }
      continue;
    }
    if (char === ">") {
      if (source[index + 1] === "=") {
        index += 2;
        push("ge", ">=", start);
      } else {
        index += 1;
        push("gt", ">", start);
      }
      continue;
    }

    if (char === "'" || char === '"') {
      const quote = char;
      index += 1;
      let value = "";
      let closed = false;
      while (index < source.length) {
        const current = source[index]!;
        if (current === quote) {
          if (source[index + 1] === quote) {
            value += quote;
            index += 2;
            continue;
          }
          index += 1;
          closed = true;
          break;
        }
        if (current === "\\") {
          const escape = source[index + 1];
          if (escape === undefined) {
            break;
          }
          switch (escape) {
            case "n":
              value += "\n";
              index += 2;
              break;
            case "r":
              value += "\r";
              index += 2;
              break;
            case "t":
              value += "\t";
              index += 2;
              break;
            case "b":
              value += "\b";
              index += 2;
              break;
            case "f":
              value += "\f";
              index += 2;
              break;
            case "u": {
              const hex = source.slice(index + 2, index + 6);
              if (!/^[0-9a-fA-F]{4}$/u.test(hex)) {
                throw new JsonPathParseError(
                  "invalid Unicode escape",
                  source,
                  index,
                );
              }
              value += String.fromCharCode(Number.parseInt(hex, 16));
              index += 6;
              break;
            }
            default:
              value += escape;
              index += 2;
              break;
          }
          continue;
        }
        value += current;
        index += 1;
      }
      if (!closed) {
        throw new JsonPathParseError("unterminated string literal", source, start);
      }
      push("string", value, start);
      continue;
    }

    if (isDigit(char)) {
      index += 1;
      while (isDigit(source[index] ?? "")) {
        index += 1;
      }
      if (source[index] === "." && isDigit(source[index + 1] ?? "")) {
        index += 1;
        while (isDigit(source[index] ?? "")) {
          index += 1;
        }
      }
      if (source[index] === "e" || source[index] === "E") {
        const exponentStart = index;
        index += 1;
        if (source[index] === "+" || source[index] === "-") {
          index += 1;
        }
        if (!isDigit(source[index] ?? "")) {
          throw new JsonPathParseError(
            "invalid numeric exponent",
            source,
            exponentStart,
          );
        }
        while (isDigit(source[index] ?? "")) {
          index += 1;
        }
      }
      push("number", source.slice(start, index), start);
      continue;
    }

    if (isIdentifierStart(char)) {
      index += 1;
      while (isIdentifierPart(source[index] ?? "")) {
        index += 1;
      }
      push("identifier", source.slice(start, index), start);
      continue;
    }

    throw new JsonPathParseError(`unexpected character '${char}'`, source, start);
  }

  tokens.push({
    type: "eof",
    value: "",
    offset: source.length,
    end: source.length,
  });
  return tokens;
}
