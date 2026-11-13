import type { Token } from "./types.ts";

const RAW_TEXT_TAGS = new Set(["script", "style", "title", "textarea"]);

function decodeEntity(input: string): string {
  const named: Record<string, string> = {
    amp: "&",
    lt: "<",
    gt: ">",
    quot: '"',
    apos: "'",
    nbsp: "\u00a0",
  };
  if (input[0] !== "&" || input[input.length - 1] !== ";") return input;
  const body = input.slice(1, -1);
  if (body[0] === "#") {
  const hex = body[1] === "x" || body[1] === "X";
  const digits = hex ? body.slice(2) : body.slice(1);
  const code = parseInt(digits, hex ? 16 : 10);
  if (Number.isFinite(code)) return String.fromCodePoint(code);
  }
  if (named[body] !== undefined) return named[body];
  return input;
}

export function decodeEntities(value: string): string {
  return value.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]+);/g, decodeEntity);
}

export function tokenize(html: string): Token[] {
  const tokens: Token[] = [];
  let pos = 0;
  const length = html.length;

  const emitText = (raw: string) => {
    if (raw.length > 0) tokens.push({ kind: "text", data: raw });
  };

  while (pos < length) {
    if (html.charCodeAt(pos) !== 60 /* < */) {
      const next = html.indexOf("<", pos);
      const end = next === -1 ? length : next;
      emitText(html.slice(pos, end));
      pos = end;
      continue;
    }

    // Comment
    if (html.startsWith("<!--", pos)) {
      const end = html.indexOf("-->", pos + 4);
      tokens.push({ kind: "comment" });
      pos = end === -1 ? length : end + 3;
      continue;
    }

    // DOCTYPE
    if (html[ pos + 1 ] === "!") {
      const end = html.indexOf(">", pos);
      pos = end === -1 ? length : end + 1;
      tokens.push({ kind: "doctype" });
      continue;
    }

    // CDATA (typically inside SVG)
    if (html.startsWith("<![CDATA[", pos)) {
      const end = html.indexOf("]]>", pos + 9);
      const data = end === -1 ? html.slice(pos + 9) : html.slice(pos + 9, end);
      emitText(data);
      pos = end === -1 ? length : end + 3;
      continue;
    }

    // End tag
    if (html[ pos + 1 ] === "/") {
      const close = html.indexOf(">", pos);
      if (close === -1) {
        emitText(html.slice(pos));
        pos = length;
        continue;
      }
      const tagName = html.slice(pos + 2, close).trim().toLowerCase();
      if (tagName) tokens.push({ kind: "endTag", tagName });
      pos = close + 1;
      continue;
    }

    // Start tag
    const tagStart = pos + 1;
    let cursor = tagStart;
    while (cursor < length && /[a-zA-Z0-9:-]/.test(html[cursor])) cursor++;
    const tagName = html.slice(tagStart, cursor).toLowerCase();
    if (!tagName) {
      emitText(html.slice(pos, pos + 1));
      pos++;
      continue;
    }

    const attributes: Array<[string, string]> = [];
    let selfClosing = false;
    while (cursor < length) {
      while (cursor < length && /\s/.test(html[cursor])) cursor++;
      if (cursor >= length) break;
      if (html[cursor] === ">") {
        cursor++;
        break;
      }
      if (html[cursor] === "/" && html[cursor + 1] === ">") {
        selfClosing = true;
        cursor += 2;
        break;
      }

      const attrStart = cursor;
      while (cursor < length && /[^=\s/>]/.test(html[cursor])) cursor++;
      const attrName = html.slice(attrStart, cursor).toLowerCase();
      let attrValue = "";

      while (cursor < length && /\s/.test(html[cursor])) cursor++;
      if (html[cursor] === "=") {
        cursor++;
        while (cursor < length && /\s/.test(html[cursor])) cursor++;
        const quote = html[cursor];
        if (quote === '"' || quote === "'") {
          const valueEnd = html.indexOf(quote, cursor + 1);
          if (valueEnd === -1) {
            attrValue = html.slice(cursor + 1);
            cursor = length;
          } else {
            attrValue = html.slice(cursor + 1, valueEnd);
            cursor = valueEnd + 1;
          }
        } else {
          const valueStart = cursor;
          while (cursor < length && /[^\s>]/.test(html[cursor])) cursor++;
          attrValue = html.slice(valueStart, cursor);
        }
      }

      if (attrName && !attributes.some(([name]) => name === attrName)) {
        attributes.push([attrName, decodeEntities(attrValue)]);
      }
    }

    tokens.push({ kind: "startTag", tagName, attributes, selfClosing });

    // Raw text elements: consume until the matching end tag as a single text run.
    if (RAW_TEXT_TAGS.has(tagName) && !selfClosing) {
      const needle = "</" + tagName;
      let search = pos = cursor;
      let found = -1;
      while (search < length) {
        const hit = html.toLowerCase().indexOf(needle, search);
        if (hit === -1) break;
        const after = hit + needle.length;
        let tail = after;
        while (tail < length && /\s/.test(html[tail])) tail++;
        if (html[tail] === ">" || html[tail] === "/") {
          found = hit;
          break;
        }
        search = hit + 1;
      }
      if (found === -1) {
        emitText(html.slice(cursor));
        pos = length;
      } else {
        emitText(html.slice(cursor, found));
        const close = html.indexOf(">", found);
        tokens.push({ kind: "endTag", tagName });
        pos = close === -1 ? length : close + 1;
      }
    } else {
      pos = cursor;
    }
  }

  return tokens;
}
