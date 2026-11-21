const utf8Decoder = new TextDecoder("utf-8", { fatal: true });

export function safeDecodeURIComponent(value: string): string | undefined {
  try {
    return decodeURIComponent(value);
  } catch {
    return undefined;
  }
}

export function decodeForm(value: string): string | undefined {
  const decoded = safeDecodeURIComponent(value.replace(/\+/g, " "));
  return decoded === value ? undefined : decoded;
}

export function decodeBase64(value: string): string | undefined {
  if (!/^[A-Za-z0-9+/_-]+={0,2}$/.test(value)) {
    return undefined;
  }

  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const unpadded = normalized.replace(/=+$/, "");
  if (unpadded.length === 0 || unpadded.length % 4 === 1) {
    return undefined;
  }

  try {
    const bytes = Buffer.from(
      unpadded.padEnd(Math.ceil(unpadded.length / 4) * 4, "="),
      "base64",
    );
    const decoded = utf8Decoder.decode(bytes);
    return decoded.length > 0 ? decoded : undefined;
  } catch {
    return undefined;
  }
}

export function decodeHex(value: string): string | undefined {
  if (!/^(?:[0-9A-Fa-f]{2})+$/.test(value)) {
    return undefined;
  }
  try {
    return utf8Decoder.decode(Buffer.from(value, "hex"));
  } catch {
    return undefined;
  }
}

export function decodeUnicodeEscapes(value: string): string | undefined {
  if (!/\\u(?:\{[0-9A-Fa-f]+\}|[0-9A-Fa-f]{4})|\\x[0-9A-Fa-f]{2}/.test(value)) {
    return undefined;
  }

  let changed = false;
  const decoded = value.replace(
    /\\u\{([0-9A-Fa-f]+)\}|\\u([0-9A-Fa-f]{4})|\\x([0-9A-Fa-f]{2})/g,
    (_match, codePoint: string | undefined, unit: string | undefined, byte: string | undefined) => {
      changed = true;
      if (codePoint) {
        const number = Number.parseInt(codePoint, 16);
        return Number.isFinite(number) && number <= 0x10ffff
          ? String.fromCodePoint(number)
          : "\uFFFD";
      }
      if (unit) {
        return String.fromCharCode(Number.parseInt(unit, 16));
      }
      return String.fromCharCode(Number.parseInt(byte as string, 16));
    },
  );
  return changed ? decoded : undefined;
}

const NAMED_ENTITIES: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
  nbsp: "\u00A0",
  commat: "@",
  period: ".",
  plus: "+",
  sol: "/",
  colon: ":",
  lowbar: "_",
  hyphen: "-",
  num: "#",
  percnt: "%",
  equals: "=",
  quest: "?",
};

export function decodeHtmlEntities(value: string): string | undefined {
  if (!value.includes("&")) {
    return undefined;
  }

  let changed = false;
  const decoded = value.replace(
    /&(#x[0-9A-Fa-f]+|#X[0-9A-Fa-f]+|#\d+|[A-Za-z]{2,8});/g,
    (match, entity: string) => {
      if (entity.startsWith("#x") || entity.startsWith("#X")) {
        const number = Number.parseInt(entity.slice(2), 16);
        if (Number.isFinite(number) && number <= 0x10ffff) {
          changed = true;
          return String.fromCodePoint(number);
        }
        return match;
      }
      if (entity.startsWith("#")) {
        const number = Number.parseInt(entity.slice(1), 10);
        if (Number.isFinite(number) && number <= 0x10ffff) {
          changed = true;
          return String.fromCodePoint(number);
        }
        return match;
      }
      const named = NAMED_ENTITIES[entity.toLowerCase()];
      if (named !== undefined) {
        changed = true;
        return named;
      }
      return match;
    },
  );
  return changed ? decoded : undefined;
}

export function encodeUnicodeEscapes(value: string, upper = false): string {
  let escaped = "";
  for (let index = 0; index < value.length; index += 1) {
    let hex = value.charCodeAt(index).toString(16).padStart(4, "0");
    if (upper) hex = hex.toUpperCase();
    escaped += `\\u${hex}`;
  }
  return escaped;
}

export function encodeHtmlEntities(value: string): string {
  let encoded = "";
  for (const character of value) {
    const codePoint = character.codePointAt(0) as number;
    encoded += `&#x${codePoint.toString(16)};`;
  }
  return encoded;
}

export function encodeJsonEscapes(value: string): string {
  return JSON.stringify(value).slice(1, -1);
}
