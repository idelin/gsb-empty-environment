const NAMED_REFERENCES: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
  nbsp: "\u00a0",
  copy: "\u00a9",
  reg: "\u00ae",
  trade: "\u2122",
  hellip: "\u2026",
  mdash: "\u2014",
  ndash: "\u2013",
  lsquo: "\u2018",
  rsquo: "\u2019",
  ldquo: "\u201c",
  rdquo: "\u201d",
  laquo: "\u00ab",
  raquo: "\u00bb",
  times: "\u00d7",
  divide: "\u00f7",
  plusmn: "\u00b1",
  deg: "\u00b0",
  micro: "\u00b5",
  para: "\u00b6",
  sect: "\u00a7",
  middot: "\u00b7",
  bull: "\u2022",
  euro: "\u20ac",
  pound: "\u00a3",
  yen: "\u00a5",
  cent: "\u00a2",
  curren: "\u00a4",
  brvbar: "\u00a6",
  uml: "\u00a8",
  ordf: "\u00aa",
  ordm: "\u00ba",
  not: "\u00ac",
  shy: "\u00ad",
  macr: "\u00af",
  acute: "\u00b4",
  cedil: "\u00b8",
  sup1: "\u00b9",
  sup2: "\u00b2",
  sup3: "\u00b3",
  frac14: "\u00bc",
  frac12: "\u00bd",
  frac34: "\u00be",
  iquest: "\u00bf",
  iexcl: "\u00a1",
  szlig: "\u00df",
  colon: ":",
  semi: ";",
  sol: "/",
  bsol: "\\",
  lpar: "(",
  rpar: ")",
  commat: "@",
  equals: "=",
  quest: "?",
  excl: "!",
  num: "#",
  dollar: "$",
  percnt: "%",
  ast: "*",
  plus: "+",
  comma: ",",
  period: ".",
  hyphen: "-",
  lowbar: "_",
  grave: "`",
  lcub: "{",
  rcub: "}",
  lbrack: "[",
  rbrack: "]",
  verbar: "|",
  NewLine: "\n",
  Tab: "\t",
};

const LEGACY_REFERENCES_WITHOUT_SEMICOLON = new Set([
  "amp",
  "lt",
  "gt",
  "quot",
  "nbsp",
  "copy",
  "reg",
]);

function codePointToString(codePoint: number): string {
  if (
    !Number.isFinite(codePoint) ||
    codePoint <= 0 ||
    codePoint > 0x10ffff ||
    (codePoint >= 0xd800 && codePoint <= 0xdfff)
  ) {
    return "\ufffd";
  }
  return String.fromCodePoint(codePoint);
}

export function decodeCharacterReferences(input: string): string {
  if (!input.includes("&")) {
    return input;
  }

  let output = "";
  let index = 0;

  while (index < input.length) {
    const ampersand = input.indexOf("&", index);
    if (ampersand === -1) {
      output += input.slice(index);
      break;
    }

    output += input.slice(index, ampersand);

    if (input.startsWith("&#", ampersand)) {
      let cursor = ampersand + 2;
      let radix = 10;
      if (input[cursor] === "x" || input[cursor] === "X") {
        radix = 16;
        cursor += 1;
      }

      const digitsStart = cursor;
      while (
        cursor < input.length &&
        (radix === 16
          ? /[0-9a-fA-F]/.test(input[cursor])
          : /[0-9]/.test(input[cursor]))
      ) {
        cursor += 1;
      }

      if (cursor === digitsStart) {
        output += "&";
        index = ampersand + 1;
        continue;
      }

      const codePoint = Number.parseInt(
        input.slice(digitsStart, cursor),
        radix,
      );
      output += codePointToString(codePoint);
      if (input[cursor] === ";") {
        cursor += 1;
      }
      index = cursor;
      continue;
    }

    if (/[A-Za-z]/.test(input[ampersand + 1] ?? "")) {
      let cursor = ampersand + 1;
      while (cursor < input.length && /[A-Za-z0-9]/.test(input[cursor])) {
        cursor += 1;
      }
      const name = input.slice(ampersand + 1, cursor);
      const hasSemicolon = input[cursor] === ";";
      const value =
        NAMED_REFERENCES[name] ??
        NAMED_REFERENCES[name.toLowerCase()];

      if (
        value !== undefined &&
        (hasSemicolon || LEGACY_REFERENCES_WITHOUT_SEMICOLON.has(name))
      ) {
        output += value;
        index = hasSemicolon ? cursor + 1 : cursor;
        continue;
      }
    }

    output += "&";
    index = ampersand + 1;
  }

  return output;
}

export function escapeText(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

export function escapeAttribute(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}
