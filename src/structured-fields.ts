/**
 * Minimal RFC 8941 Structured Fields dictionary parser, restricted to
 * dictionaries whose member values are all sf-string items (the shape
 * required by the Reporting-Endpoints header).
 *
 * Any deviation — bare keys (boolean true), non-string items, inner lists,
 * parameters, or malformed syntax — fails the whole parse and returns null,
 * so callers can ignore the entire header value.
 */

const KEY_START = /[a-z*]/;
const KEY_CHAR = /[a-z0-9_.*-]/;

export function parseStringDictionary(input: string): Map<string, string> | null {
  const members = new Map<string, string>();
  let pos = 0;

  const skipOws = (): void => {
    while (pos < input.length && (input[pos] === " " || input[pos] === "\t")) {
      pos++;
    }
  };

  const parseKey = (): string | null => {
    if (pos >= input.length || !KEY_START.test(input[pos])) return null;
    const start = pos;
    pos++;
    while (pos < input.length && KEY_CHAR.test(input[pos])) pos++;
    return input.slice(start, pos);
  };

  const parseString = (): string | null => {
    if (input[pos] !== '"') return null;
    pos++;
    let value = "";
    while (pos < input.length) {
      const ch = input[pos];
      if (ch === '"') {
        pos++;
        return value;
      }
      if (ch === "\\") {
        const next = input[pos + 1];
        if (next !== '"' && next !== "\\") return null;
        value += next;
        pos += 2;
        continue;
      }
      const code = ch.charCodeAt(0);
      if (code < 0x20 || code > 0x7e) return null;
      value += ch;
      pos++;
    }
    return null; // unterminated string
  };

  skipOws();
  if (pos >= input.length) return null; // empty value is not a dictionary

  for (;;) {
    const key = parseKey();
    if (key === null) return null;
    // A member without "=" is boolean true; we require string values.
    if (pos >= input.length || input[pos] !== "=") return null;
    pos++;
    const value = parseString();
    if (value === null) return null;
    members.set(key, value); // duplicate keys: last one wins
    skipOws();
    if (pos >= input.length) return members;
    if (input[pos] !== ",") return null;
    pos++;
    skipOws();
    if (pos >= input.length) return null; // trailing comma
  }
}
