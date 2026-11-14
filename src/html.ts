import {
  decodeHtmlEntitiesOnce,
  sanitizeUrl,
  type SanitizeUrlOptions,
} from "./url.ts";

export interface SanitizeHtmlOptions extends SanitizeUrlOptions {
  allowedTags?: readonly string[];
  allowedAttributes?: Readonly<Record<string, readonly string[]>>;
}

export interface HtmlTextNode {
  type: "text";
  value: string;
}

export interface HtmlElementNode {
  type: "element";
  tag: string;
  attrs: Array<[string, string]>;
  children: HtmlNode[];
}

export type HtmlNode = HtmlTextNode | HtmlElementNode;

export interface HtmlAttribute {
  name: string;
  value: string;
}

export type HtmlToken =
  | { type: "text"; value: string }
  | { type: "start"; name: string; attrs: HtmlAttribute[]; selfClosing: boolean }
  | { type: "end"; name: string }
  | { type: "comment" }
  | { type: "declaration" };

/** Tags Markdown output may contain. Raw HTML is restricted to the same set. */
export const ALLOWED_TAGS: ReadonlySet<string> = new Set([
  "a",
  "abbr",
  "b",
  "blockquote",
  "br",
  "caption",
  "code",
  "dd",
  "del",
  "details",
  "dfn",
  "dl",
  "dt",
  "em",
  "figcaption",
  "figure",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "hr",
  "i",
  "img",
  "ins",
  "kbd",
  "li",
  "mark",
  "ol",
  "p",
  "pre",
  "q",
  "s",
  "samp",
  "small",
  "span",
  "strong",
  "sub",
  "summary",
  "sup",
  "table",
  "tbody",
  "td",
  "tfoot",
  "th",
  "thead",
  "time",
  "tr",
  "u",
  "ul",
  "var",
]);

const GLOBAL_ATTRIBUTES = ["title", "lang", "dir", "id"] as const;

export const ALLOWED_ATTRIBUTES: Readonly<Record<string, readonly string[]>> = {
  a: ["href", "title", "lang", "dir", "id"],
  img: ["src", "alt", "title", "width", "height", "loading"],
  ol: ["start", "reversed", "type"],
  li: ["value"],
  td: ["colspan", "rowspan", "headers"],
  th: ["colspan", "rowspan", "scope", "headers"],
  time: ["datetime"],
  details: ["open"],
  code: ["class"],
  pre: ["class"],
};

const VOID_TAGS: ReadonlySet<string> = new Set([
  "area",
  "base",
  "br",
  "col",
  "embed",
  "hr",
  "img",
  "input",
  "link",
  "meta",
  "param",
  "source",
  "track",
  "wbr",
]);

/**
 * Elements whose contents are never allowed to survive as markup or text.
 * Dropping the whole subtree is important for script neutralization and for
 * avoiding HTML/SVG/MathML mutation-XSS quirks.
 */
export const DROP_CONTENT_TAGS: ReadonlySet<string> = new Set([
  "applet",
  "embed",
  "frame",
  "frameset",
  "iframe",
  "math",
  "noembed",
  "noframes",
  "noscript",
  "object",
  "plaintext",
  "script",
  "select",
  "style",
  "svg",
  "template",
  "textarea",
  "title",
  "xmp",
]);

/**
 * The HTML parser ignores a trailing slash on these start tags, so they still
 * open a subtree even when written as `<script/>`.
 */
export const SELF_CLOSING_IGNORED: ReadonlySet<string> = new Set([
  "applet",
  "iframe",
  "noembed",
  "noframes",
  "noscript",
  "object",
  "plaintext",
  "script",
  "select",
  "style",
  "template",
  "textarea",
  "title",
  "xmp",
]);

const BLOCK_TAGS: ReadonlySet<string> = new Set([
  "address",
  "article",
  "aside",
  "blockquote",
  "caption",
  "dd",
  "details",
  "div",
  "dl",
  "dt",
  "fieldset",
  "figcaption",
  "figure",
  "footer",
  "form",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "header",
  "hr",
  "li",
  "main",
  "nav",
  "ol",
  "p",
  "pre",
  "section",
  "table",
  "tbody",
  "td",
  "tfoot",
  "th",
  "thead",
  "tr",
  "ul",
]);

const URL_ATTRIBUTES: ReadonlySet<string> = new Set(["href", "src"]);
const MAX_HTML_DEPTH = 256;

export function escapeHtmlText(input: string): string {
  return input
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

export function escapeAttribute(input: string): string {
  return input
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

export function isAllowedTag(tag: string): boolean {
  return ALLOWED_TAGS.has(tag);
}

function isAsciiWhitespace(char: string): boolean {
  return char === " " || char === "\t" || char === "\n" || char === "\r" || char === "\f";
}

function scanTag(
  input: string,
  start: number,
): { token: Exclude<HtmlToken, { type: "text" } | { type: "comment" } | { type: "declaration" }>; end: number } | null {
  if (input[start] !== "<") return null;

  let cursor = start + 1;
  let closing = false;
  if (input[cursor] === "/") {
    closing = true;
    cursor++;
  }

  const nameMatch = /^[A-Za-z][A-Za-z0-9:_-]*/.exec(input.slice(cursor));
  if (nameMatch === null) return null;

  const name = nameMatch[0].toLowerCase();
  cursor += nameMatch[0].length;

  if (closing) {
    const close = input.indexOf(">", cursor);
    if (close === -1) return null;
    return { token: { type: "end", name }, end: close + 1 };
  }

  const attrs: HtmlAttribute[] = [];
  let selfClosing = false;
  let closed = false;

  while (cursor < input.length) {
    while (cursor < input.length && isAsciiWhitespace(input[cursor])) cursor++;
    if (cursor >= input.length) break;

    if (input[cursor] === ">") {
      cursor++;
      closed = true;
      break;
    }
    if (input[cursor] === "/" && input[cursor + 1] === ">") {
      selfClosing = true;
      cursor += 2;
      closed = true;
      break;
    }

    const nameStart = cursor;
    while (
      cursor < input.length &&
      !isAsciiWhitespace(input[cursor]) &&
      input[cursor] !== ">" &&
      input[cursor] !== "/" &&
      input[cursor] !== "="
    ) {
      cursor++;
    }
    if (cursor === nameStart) {
      cursor++;
      continue;
    }

    const attrName = input.slice(nameStart, cursor).toLowerCase();
    while (cursor < input.length && isAsciiWhitespace(input[cursor])) cursor++;

    let attrValue = "";
    if (input[cursor] === "=") {
      cursor++;
      while (cursor < input.length && isAsciiWhitespace(input[cursor])) cursor++;

      if (input[cursor] === "\"" || input[cursor] === "'") {
        const quote = input[cursor];
        cursor++;
        const valueStart = cursor;
        while (cursor < input.length && input[cursor] !== quote) cursor++;
        attrValue = input.slice(valueStart, cursor);
        if (cursor < input.length) cursor++;
      } else {
        const valueStart = cursor;
        while (
          cursor < input.length &&
          !isAsciiWhitespace(input[cursor]) &&
          input[cursor] !== ">"
        ) {
          cursor++;
        }
        attrValue = input.slice(valueStart, cursor);
      }
    }

    attrs.push({ name: attrName, value: decodeHtmlEntitiesOnce(attrValue) });
  }

  if (!closed) return null;
  return { token: { type: "start", name, attrs, selfClosing }, end: cursor };
}

export function tokenizeHtml(input: string): HtmlToken[] {
  const tokens: HtmlToken[] = [];
  let cursor = 0;
  let textStart = 0;

  const flushText = (end: number): void => {
    if (end > textStart) {
      tokens.push({ type: "text", value: input.slice(textStart, end) });
    }
  };

  while (cursor < input.length) {
    if (input[cursor] !== "<") {
      cursor++;
      continue;
    }

    if (input.startsWith("<!--", cursor)) {
      flushText(cursor);
      const close = input.indexOf("-->", cursor + 4);
      cursor = close === -1 ? input.length : close + 3;
      tokens.push({ type: "comment" });
      textStart = cursor;
      continue;
    }

    if (input.startsWith("<![CDATA[", cursor)) {
      flushText(cursor);
      const close = input.indexOf("]]>", cursor + 9);
      cursor = close === -1 ? input.length : close + 3;
      tokens.push({ type: "declaration" });
      textStart = cursor;
      continue;
    }

    if (input.startsWith("<!", cursor) || input.startsWith("<?", cursor)) {
      flushText(cursor);
      const close = input.indexOf(">", cursor + 2);
      cursor = close === -1 ? input.length : close + 1;
      tokens.push({ type: "declaration" });
      textStart = cursor;
      continue;
    }

    const tag = scanTag(input, cursor);
    if (tag === null) {
      cursor++;
      continue;
    }

    flushText(cursor);
    tokens.push(tag.token);
    cursor = tag.end;
    textStart = cursor;
  }

  flushText(input.length);
  return tokens;
}

function appendText(parent: HtmlNode[], value: string): void {
  if (value.length === 0) return;
  const previous = parent[parent.length - 1];
  if (previous !== undefined && previous.type === "text") {
    previous.value += value;
  } else {
    parent.push({ type: "text", value });
  }
}

function sanitizeAttributes(
  tag: string,
  attrs: readonly HtmlAttribute[],
  options: SanitizeHtmlOptions,
): Array<[string, string]> {
  const configured = options.allowedAttributes?.[tag];
  const allowed = new Set<string>([...GLOBAL_ATTRIBUTES, ...(configured ?? ALLOWED_ATTRIBUTES[tag] ?? [])]);
  const safe: Array<[string, string]> = [];
  const seen = new Set<string>();

  for (const attr of attrs) {
    const name = attr.name.toLowerCase();
    if (seen.has(name) || !allowed.has(name)) continue;
    seen.add(name);

    if (name === "class") {
      if (!/^language-[A-Za-z0-9_+-]{1,64}$/.test(attr.value)) continue;
      safe.push([name, attr.value]);
      continue;
    }

    if (URL_ATTRIBUTES.has(name)) {
      const url = sanitizeUrl(attr.value, options);
      if (url === null) continue;
      safe.push([name, url]);
      continue;
    }

    switch (name) {
      case "id":
        if (!/^[A-Za-z][A-Za-z0-9_.:-]{0,127}$/.test(attr.value)) continue;
        safe.push([name, attr.value]);
        break;
      case "lang":
        if (!/^[A-Za-z0-9-]{1,35}$/.test(attr.value)) continue;
        safe.push([name, attr.value]);
        break;
      case "dir":
        if (!/^(?:ltr|rtl|auto)$/i.test(attr.value)) continue;
        safe.push([name, attr.value.toLowerCase()]);
        break;
      case "width":
      case "height":
        if (!/^(?:\d{1,5}|\d{1,3}%)$/.test(attr.value)) continue;
        safe.push([name, attr.value]);
        break;
      case "start":
      case "value":
      case "colspan":
      case "rowspan":
        if (!/^\d{1,6}$/.test(attr.value)) continue;
        safe.push([name, attr.value]);
        break;
      case "reversed":
      case "open":
        safe.push([name, "open"]);
        break;
      case "loading":
        if (attr.value !== "lazy" && attr.value !== "eager") continue;
        safe.push([name, attr.value]);
        break;
      case "scope":
        if (!/^(?:row|col|rowgroup|colgroup)$/i.test(attr.value)) continue;
        safe.push([name, attr.value.toLowerCase()]);
        break;
      case "headers":
        if (!/^[A-Za-z0-9_.:-]+(?:\s+[A-Za-z0-9_.:-]+)*$/.test(attr.value)) continue;
        safe.push([name, attr.value]);
        break;
      case "type":
        if (!/^(?:1|a|A|i|I)$/.test(attr.value)) continue;
        safe.push([name, attr.value]);
        break;
      default:
        safe.push([name, attr.value]);
        break;
    }
  }

  return safe;
}

function renderAttributes(attrs: readonly [string, string][]): string {
  let output = "";
  for (const [name, value] of attrs) {
    if (value === "open" && (name === "reversed" || name === "open")) {
      output += ` ${name}`;
    } else {
      output += ` ${name}="${escapeAttribute(value)}"`;
    }
  }
  return output;
}

export function renderStartTag(
  tag: string,
  attrs: readonly [string, string][],
  _selfClosing = false,
): string {
  return `<${tag}${renderAttributes(attrs)}>`;
}

function renderNodes(nodes: readonly HtmlNode[]): string {
  let output = "";
  for (const node of nodes) {
    if (node.type === "text") {
      output += escapeHtmlText(node.value);
      continue;
    }
    output += renderStartTag(node.tag, node.attrs);
    if (!VOID_TAGS.has(node.tag)) {
      output += renderNodes(node.children);
      output += `</${node.tag}>`;
    }
  }
  return output;
}

/**
 * Parse HTML into a tree and keep only declared tags/attributes. Comments,
 * declarations, event handlers, inline styles, and script-capable subtrees are
 * removed. Text and attribute values are always re-escaped on output.
 */
export function sanitizeHtml(input: string, options: SanitizeHtmlOptions = {}): string {
  if (typeof input !== "string" || input.length === 0) return "";

  const tokens = tokenizeHtml(input);
  const root: HtmlNode[] = [];
  const stack: Array<{ tag: string; children: HtmlNode[] }> = [
    { tag: "#root", children: root },
  ];
  let skip: { tag: string; depth: number } | null = null;
  let dropRest = false;

  const currentChildren = (): HtmlNode[] => stack[stack.length - 1].children;

  for (const token of tokens) {
    if (dropRest) break;

    if (skip !== null) {
      if (token.type === "start" && token.name === skip.tag && !token.selfClosing) {
        skip.depth++;
      } else if (token.type === "end" && token.name === skip.tag) {
        skip.depth--;
        if (skip.depth <= 0) skip = null;
      }
      continue;
    }

    if (token.type === "text") {
      appendText(currentChildren(), decodeHtmlEntitiesOnce(token.value));
      continue;
    }

    if (token.type === "comment" || token.type === "declaration") continue;

    if (token.type === "start") {
      const tag = token.name;

      if (DROP_CONTENT_TAGS.has(tag)) {
        if (tag === "plaintext") {
          dropRest = true;
        } else if (!token.selfClosing || SELF_CLOSING_IGNORED.has(tag)) {
          skip = { tag, depth: 1 };
        }
        continue;
      }

      if (BLOCK_TAGS.has(tag)) {
        while (stack.length > 1 && stack[stack.length - 1].tag === "p") stack.pop();
      }
      if (tag === "li") {
        while (stack.length > 1 && stack[stack.length - 1].tag === "li") stack.pop();
      }
      if (tag === "dt" || tag === "dd") {
        while (
          stack.length > 1 &&
          (stack[stack.length - 1].tag === "dt" || stack[stack.length - 1].tag === "dd")
        ) {
          stack.pop();
        }
      }
      if (tag === "p") {
        while (stack.length > 1 && stack[stack.length - 1].tag === "p") stack.pop();
      }

      if (!ALLOWED_TAGS.has(tag)) continue;

      const attrs = sanitizeAttributes(tag, token.attrs, options);
      if (tag === "img" && !attrs.some(([name]) => name === "src")) continue;

      const element: HtmlElementNode = { type: "element", tag, attrs, children: [] };
      currentChildren().push(element);

      if (!VOID_TAGS.has(tag) && !token.selfClosing && stack.length < MAX_HTML_DEPTH) {
        stack.push({ tag, children: element.children });
      }
      continue;
    }

    if (token.type === "end") {
      for (let index = stack.length - 1; index >= 1; index--) {
        if (stack[index].tag === token.name) {
          stack.length = index;
          break;
        }
      }
    }
  }

  return renderNodes(root);
}

/**
 * Sanitize a single raw HTML token (used by the inline Markdown parser).
 * Unknown/malformed tokens become empty rather than being re-emitted.
 */
export function sanitizeHtmlToken(raw: string, options: SanitizeHtmlOptions = {}): string {
  if (raw.startsWith("<!--") || raw.startsWith("<!") || raw.startsWith("<?")) return "";

  const tag = scanTag(raw, 0);
  if (tag === null || tag.end !== raw.length) return "";

  const token = tag.token;
  if (token.type === "end") {
    if (!ALLOWED_TAGS.has(token.name) || VOID_TAGS.has(token.name)) return "";
    return `</${token.name}>`;
  }
  if (!ALLOWED_TAGS.has(token.name) || DROP_CONTENT_TAGS.has(token.name)) return "";

  const attrs = sanitizeAttributes(token.name, token.attrs, options);
  if (token.name === "img" && !attrs.some(([name]) => name === "src")) return "";
  return renderStartTag(token.name, attrs, token.selfClosing);
}

export function scanHtmlTag(
  input: string,
  start: number,
): { name: string; end: number; closing: boolean; selfClosing: boolean } | null {
  const tag = scanTag(input, start);
  if (tag === null) return null;
  if (tag.token.type === "end") {
    return { name: tag.token.name, end: tag.end, closing: true, selfClosing: false };
  }
  return {
    name: tag.token.name,
    end: tag.end,
    closing: false,
    selfClosing: tag.token.selfClosing,
  };
}

export function isVoidTag(tag: string): boolean {
  return VOID_TAGS.has(tag);
}
