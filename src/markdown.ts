import {
  decodeHtmlEntitiesOnce,
  sanitizeUrl,
  type SanitizeUrlOptions,
} from "./url.ts";
import {
  DROP_CONTENT_TAGS,
  SELF_CLOSING_IGNORED,
  escapeAttribute,
  escapeHtmlText,
  sanitizeHtml,
  sanitizeHtmlToken,
  scanHtmlTag,
} from "./html.ts";

export interface MarkdownOptions extends SanitizeUrlOptions {
  /** Maximum inline nesting depth before content is treated as plain text. */
  maxInlineDepth?: number;
}

export interface MarkdownTextNode {
  type: "text";
  value: string;
}

export interface MarkdownCodeNode {
  type: "code";
  value: string;
}

export interface MarkdownBreakNode {
  type: "break";
}

export interface MarkdownHtmlNode {
  type: "html";
  value: string;
}

export interface MarkdownEmphasisNode {
  type: "strong" | "em" | "del";
  children: MarkdownInlineNode[];
}

export interface MarkdownLinkNode {
  type: "link";
  href: string | null;
  title: string;
  children: MarkdownInlineNode[];
}

export interface MarkdownImageNode {
  type: "image";
  src: string | null;
  alt: string;
  title: string;
}

export type MarkdownInlineNode =
  | MarkdownTextNode
  | MarkdownCodeNode
  | MarkdownBreakNode
  | MarkdownHtmlNode
  | MarkdownEmphasisNode
  | MarkdownLinkNode
  | MarkdownImageNode;

export interface MarkdownParagraphNode {
  type: "paragraph";
  children: MarkdownInlineNode[];
}

export interface MarkdownHeadingNode {
  type: "heading";
  level: number;
  children: MarkdownInlineNode[];
}

export interface MarkdownBlockquoteNode {
  type: "blockquote";
  children: MarkdownBlockNode[];
}

export interface MarkdownListNode {
  type: "list";
  ordered: boolean;
  start: number;
  items: MarkdownBlockNode[][];
}

export interface MarkdownCodeBlockNode {
  type: "codeBlock";
  language: string;
  value: string;
}

export interface MarkdownThematicBreakNode {
  type: "thematicBreak";
}

export interface MarkdownHtmlBlockNode {
  type: "html";
  value: string;
}

export interface MarkdownTableNode {
  type: "table";
  align: Array<"left" | "center" | "right" | null>;
  header: MarkdownInlineNode[][];
  rows: MarkdownInlineNode[][];
}

export type MarkdownBlockNode =
  | MarkdownParagraphNode
  | MarkdownHeadingNode
  | MarkdownBlockquoteNode
  | MarkdownListNode
  | MarkdownCodeBlockNode
  | MarkdownThematicBreakNode
  | MarkdownHtmlBlockNode
  | MarkdownTableNode;

export interface MarkdownDocument {
  type: "root";
  children: MarkdownBlockNode[];
}

interface LinkDefinition {
  destination: string;
  title: string;
}

interface InlineContext {
  options: MarkdownOptions;
  definitions: ReadonlyMap<string, LinkDefinition>;
  maxDepth: number;
}

const ASCII_PUNCTUATION = /[!"#$%&'()*+,\-./:;<=>?@[\\\]^_`{|}~]/;
const BLANK_LINE = /^[ \t]*$/;
const FENCE_OPEN = /^( {0,3})(`{3,}|~{3,})([^\n]*)$/;
const ATX_HEADING = /^( {0,3})(#{1,6})(?:[ \t]+(.*?))?[ \t]*$/;
const THEMATIC_BREAK = /^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$/;
const BLOCKQUOTE = /^ {0,3}> ?(.*)$/;
const LIST_MARKER = /^( {0,3})([-+*]|\d{1,9}[.)])([ \t]+|$)(.*)$/;
const SETEXT_UNDERLINE = /^ {0,3}(=+|-+)[ \t]*$/;
const LINK_DEFINITION =
  /^ {0,3}\[([^\]]+)\]:[ \t]*(?:<([^>\n]*)>|(\S+))(?:[ \t]+(?:"([^"]*)"|'([^']*)'|\(([^)]*)\)))?[ \t]*$/;
const TABLE_DELIMITER =
  /^ {0,3}\|?[ \t]*:?-{3,}:?[ \t]*(?:\|[ \t]*:?-{3,}:?[ \t]*)+\|?[ \t]*$/;

function normalizeNewlines(input: string): string {
  return input.replace(/\r\n?/g, "\n");
}

function normalizeLabel(input: string): string {
  return input.trim().replace(/\s+/g, " ").toLowerCase();
}

function decodeBackslashEscapes(input: string): string {
  return input.replace(/\\([!"#$%&'()*+,\-./:;<=>?@[\\\]^_`{|}~])/g, "$1");
}

function stripUpToIndent(line: string, width: number): string {
  let consumed = 0;
  let cursor = 0;
  while (cursor < line.length && consumed < width) {
    const char = line[cursor];
    if (char === " ") {
      consumed++;
      cursor++;
    } else if (char === "\t") {
      const nextStop = consumed + (4 - (consumed % 4));
      if (nextStop > width) break;
      consumed = nextStop;
      cursor++;
    } else {
      break;
    }
  }
  return line.slice(cursor);
}

function indentationOf(line: string): number {
  let width = 0;
  for (const char of line) {
    if (char === " ") width++;
    else if (char === "\t") width += 4 - (width % 4);
    else break;
  }
  return width;
}

function isBlank(line: string): boolean {
  return BLANK_LINE.test(line);
}

function htmlBlockKind(
  line: string,
): { kind: "until"; end: string } | { kind: "tag"; tag: string } | { kind: "block" } | null {
  const trimmed = line.replace(/^ {0,3}/, "");

  if (trimmed.startsWith("<!--")) return { kind: "until", end: "-->" };
  if (trimmed.startsWith("<?")) return { kind: "until", end: "?>" };
  if (trimmed.startsWith("<![CDATA[")) return { kind: "until", end: "]]>" };
  if (/^<![A-Za-z]/.test(trimmed)) return { kind: "until", end: ">" };

  const rawTag = /^<(script|pre|style|textarea)(?:[\s>]|$)/i.exec(trimmed);
  if (rawTag !== null) return { kind: "tag", tag: rawTag[1].toLowerCase() };

  if (
    /^<\/?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|section|source|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?:\s|\/?>|$)/i.test(
      trimmed,
    )
  ) {
    return { kind: "block" };
  }

  if (/^<\/?[A-Za-z][A-Za-z0-9:_-]*(?:\s[^>]*)?\/?>\s*$/.test(trimmed)) {
    return { kind: "block" };
  }

  return null;
}

function isBlockStart(line: string): boolean {
  return (
    FENCE_OPEN.test(line) ||
    ATX_HEADING.test(line) ||
    THEMATIC_BREAK.test(line) ||
    BLOCKQUOTE.test(line) ||
    LIST_MARKER.test(line) ||
    /^(?: {4}|\t)/.test(line) ||
    htmlBlockKind(line) !== null
  );
}

function matchLinkDefinition(line: string): { label: string; definition: LinkDefinition } | null {
  const match = LINK_DEFINITION.exec(line);
  if (match === null) return null;
  const destination = decodeBackslashEscapes(match[2] ?? match[3] ?? "");
  const title = match[4] ?? match[5] ?? match[6] ?? "";
  return {
    label: normalizeLabel(match[1]),
    definition: {
      destination,
      title: decodeHtmlEntitiesOnce(decodeBackslashEscapes(title)),
    },
  };
}

function splitTableCells(line: string): string[] {
  let value = line.trim();
  if (value.startsWith("|")) value = value.slice(1);
  if (value.endsWith("|") && !value.endsWith("\\|")) value = value.slice(0, -1);

  const cells: string[] = [];
  let current = "";
  let escaped = false;
  for (const char of value) {
    if (escaped) {
      current += char;
      escaped = false;
      continue;
    }
    if (char === "\\") {
      current += char;
      escaped = true;
      continue;
    }
    if (char === "|") {
      cells.push(current.trim());
      current = "";
      continue;
    }
    current += char;
  }
  cells.push(current.trim());
  return cells;
}

function tableAlignment(line: string): Array<"left" | "center" | "right" | null> | null {
  if (!TABLE_DELIMITER.test(line)) return null;
  return splitTableCells(line).map((cell) => {
    const trimmed = cell.trim();
    const left = trimmed.startsWith(":");
    const right = trimmed.endsWith(":");
    if (left && right) return "center";
    if (left) return "left";
    if (right) return "right";
    return null;
  });
}

function parseBlocks(lines: string[], context: InlineContext): MarkdownBlockNode[] {
  const blocks: MarkdownBlockNode[] = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index];
    if (isBlank(line)) {
      index++;
      continue;
    }

    const fence = FENCE_OPEN.exec(line);
    if (fence !== null) {
      const fenceChar = fence[2][0];
      const fenceLength = fence[2].length;
      const closing = new RegExp(`^ {0,3}${fenceChar === "`" ? "`" : "~"}{${fenceLength},}[ \\t]*$`);
      const body: string[] = [];
      index++;
      while (index < lines.length && !closing.test(lines[index])) {
        body.push(lines[index]);
        index++;
      }
      if (index < lines.length) index++;
      const info = fence[3].trim();
      const language = info.split(/\s+/)[0] ?? "";
      blocks.push({ type: "codeBlock", language, value: body.join("\n") });
      continue;
    }

    const heading = ATX_HEADING.exec(line);
    if (heading !== null) {
      const level = heading[2].length;
      const content = (heading[3] ?? "").replace(/[ \t]+#+[ \t]*$/, "");
      blocks.push({
        type: "heading",
        level,
        children: parseInline(content, context),
      });
      index++;
      continue;
    }

    if (THEMATIC_BREAK.test(line)) {
      blocks.push({ type: "thematicBreak" });
      index++;
      continue;
    }

    if (BLOCKQUOTE.test(line)) {
      const quoted: string[] = [];
      while (index < lines.length) {
        const match = BLOCKQUOTE.exec(lines[index]);
        if (match !== null) {
          quoted.push(match[1]);
          index++;
          continue;
        }
        if (isBlank(lines[index])) {
          quoted.push("");
          index++;
          continue;
        }
        break;
      }
      blocks.push({ type: "blockquote", children: parseBlocks(quoted, context) });
      continue;
    }

    const listMatch = LIST_MARKER.exec(line);
    if (listMatch !== null) {
      const ordered = /^\d/.test(listMatch[2]);
      const start = ordered ? Number.parseInt(listMatch[2], 10) : 1;
      const items: MarkdownBlockNode[][] = [];

      while (index < lines.length) {
        const itemMatch = LIST_MARKER.exec(lines[index]);
        if (itemMatch === null) break;
        const itemOrdered = /^\d/.test(itemMatch[2]);
        if (itemOrdered !== ordered) break;

        const leading = itemMatch[1].length;
        const markerWidth = itemMatch[2].length;
        const spacing = itemMatch[3].length;
        const contentIndent = leading + markerWidth + Math.max(1, spacing);
        const itemLines: string[] = [itemMatch[4]];
        index++;

        while (index < lines.length) {
          const next = lines[index];
          if (isBlank(next)) {
            let probe = index + 1;
            while (probe < lines.length && isBlank(lines[probe])) probe++;
            if (probe < lines.length && indentationOf(lines[probe]) >= contentIndent) {
              itemLines.push("");
              index++;
              continue;
            }
            break;
          }

          const nextMarker = LIST_MARKER.exec(next);
          if (nextMarker !== null && nextMarker[1].length <= leading) break;

          if (indentationOf(next) >= contentIndent) {
            itemLines.push(stripUpToIndent(next, contentIndent));
            index++;
            continue;
          }

          break;
        }

        items.push(parseBlocks(itemLines, context));

        while (index < lines.length && isBlank(lines[index])) index++;
        const following = index < lines.length ? LIST_MARKER.exec(lines[index]) : null;
        if (
          following === null ||
          /^\d/.test(following[2]) !== ordered ||
          following[1].length > leading
        ) {
          break;
        }
      }

      blocks.push({ type: "list", ordered, start, items });
      continue;
    }

    if (/^(?: {4}|\t)/.test(line)) {
      const body: string[] = [];
      while (index < lines.length && (/^(?: {4}|\t)/.test(lines[index]) || isBlank(lines[index]))) {
        body.push(stripUpToIndent(lines[index], 4));
        index++;
      }
      while (body.length > 0 && body[body.length - 1] === "") body.pop();
      blocks.push({ type: "codeBlock", language: "", value: body.join("\n") });
      continue;
    }

    const htmlKind = htmlBlockKind(line);
    if (htmlKind !== null) {
      const htmlLines: string[] = [];
      if (htmlKind.kind === "until") {
        while (index < lines.length) {
          htmlLines.push(lines[index]);
          const done = lines[index].includes(htmlKind.end);
          index++;
          if (done) break;
        }
      } else if (htmlKind.kind === "tag") {
        const closing = new RegExp(`</${htmlKind.tag}\\s*>`, "i");
        while (index < lines.length) {
          htmlLines.push(lines[index]);
          const done = closing.test(lines[index]);
          index++;
          if (done) break;
        }
      } else {
        while (index < lines.length && !isBlank(lines[index])) {
          htmlLines.push(lines[index]);
          index++;
        }
      }

      const safe = sanitizeHtml(htmlLines.join("\n"), context.options);
      if (safe.length > 0) blocks.push({ type: "html", value: safe });
      continue;
    }

    if (index + 1 < lines.length && line.includes("|")) {
      const align = tableAlignment(lines[index + 1]);
      if (align !== null) {
        const header = splitTableCells(line).map((cell) => parseInline(cell, context));
        const rows: MarkdownInlineNode[][] = [];
        index += 2;
        while (index < lines.length && !isBlank(lines[index]) && lines[index].includes("|")) {
          rows.push(splitTableCells(lines[index]).map((cell) => parseInline(cell, context)));
          index++;
        }
        blocks.push({ type: "table", align, header, rows });
        continue;
      }
    }

    if (index + 1 < lines.length && SETEXT_UNDERLINE.test(lines[index + 1])) {
      const level = lines[index + 1].trim().startsWith("=") ? 1 : 2;
      const content = line.replace(/^ {0,3}/, "");
      blocks.push({ type: "heading", level, children: parseInline(content, context) });
      index += 2;
      continue;
    }

    const paragraphLines: string[] = [];
    while (index < lines.length && !isBlank(lines[index])) {
      if (paragraphLines.length > 0 && isBlockStart(lines[index])) break;
      paragraphLines.push(lines[index].replace(/^ {0,3}/, ""));
      index++;
    }
    if (paragraphLines.length > 0) {
      blocks.push({
        type: "paragraph",
        children: parseInline(paragraphLines.join("\n"), context),
      });
    }
  }

  return blocks;
}

function mergeTextNodes(nodes: MarkdownInlineNode[]): MarkdownInlineNode[] {
  const merged: MarkdownInlineNode[] = [];
  for (const node of nodes) {
    const previous = merged[merged.length - 1];
    if (node.type === "text" && previous?.type === "text") {
      previous.value += node.value;
    } else if (node.type === "text" && node.value.length === 0) {
      continue;
    } else {
      merged.push(node);
    }
  }
  return merged;
}

function countRun(input: string, start: number, char: string): number {
  let cursor = start;
  while (cursor < input.length && input[cursor] === char) cursor++;
  return cursor - start;
}

function isEscaped(input: string, index: number): boolean {
  let backslashes = 0;
  let cursor = index - 1;
  while (cursor >= 0 && input[cursor] === "\\") {
    backslashes++;
    cursor--;
  }
  return backslashes % 2 === 1;
}

function findClosingDelimiter(
  input: string,
  start: number,
  char: string,
  length: number,
): number {
  const delimiter = char.repeat(length);
  let cursor = start;
  while (cursor < input.length) {
    const found = input.indexOf(delimiter, cursor);
    if (found === -1) return -1;
    if (!isEscaped(input, found) && countRun(input, found, char) >= length) return found;
    cursor = found + 1;
  }
  return -1;
}

function findClosingBracket(input: string, start: number): number {
  let depth = 1;
  let cursor = start;
  while (cursor < input.length) {
    const char = input[cursor];
    if (char === "\\") {
      cursor += 2;
      continue;
    }
    if (char === "`") {
      const run = countRun(input, cursor, "`");
      const close = findClosingDelimiter(input, cursor + run, "`", run);
      cursor = close === -1 ? cursor + run : close + run;
      continue;
    }
    if (char === "[") depth++;
    if (char === "]") {
      depth--;
      if (depth === 0) return cursor;
    }
    cursor++;
  }
  return -1;
}

interface InlineDestination {
  destination: string;
  title: string;
  end: number;
}

function parseInlineDestination(input: string, start: number): InlineDestination | null {
  let cursor = start;
  while (cursor < input.length && /[ \t\n]/.test(input[cursor])) cursor++;
  if (cursor >= input.length) return null;

  let destination = "";
  if (input[cursor] === "<") {
    cursor++;
    const valueStart = cursor;
    while (cursor < input.length && input[cursor] !== ">") {
      if (input[cursor] === "\n") return null;
      if (input[cursor] === "\\" && cursor + 1 < input.length) cursor++;
      cursor++;
    }
    if (cursor >= input.length) return null;
    destination = input.slice(valueStart, cursor);
    cursor++;
  } else {
    const valueStart = cursor;
    let depth = 0;
    while (cursor < input.length) {
      const char = input[cursor];
      if (char === "\\" && cursor + 1 < input.length) {
        cursor += 2;
        continue;
      }
      if (char === "(") {
        depth++;
        cursor++;
        continue;
      }
      if (char === ")") {
        if (depth === 0) break;
        depth--;
        cursor++;
        continue;
      }
      if (depth === 0 && /[ \t\n]/.test(char)) break;
      cursor++;
    }
    destination = input.slice(valueStart, cursor);
  }

  while (cursor < input.length && /[ \t\n]/.test(input[cursor])) cursor++;

  let title = "";
  if (input[cursor] === "\"" || input[cursor] === "'") {
    const quote = input[cursor];
    cursor++;
    const titleStart = cursor;
    while (cursor < input.length && input[cursor] !== quote) {
      if (input[cursor] === "\\" && cursor + 1 < input.length) cursor++;
      cursor++;
    }
    if (cursor >= input.length) return null;
    title = input.slice(titleStart, cursor);
    cursor++;
  } else if (input[cursor] === "(") {
    cursor++;
    const titleStart = cursor;
    let depth = 1;
    while (cursor < input.length && depth > 0) {
      if (input[cursor] === "\\" && cursor + 1 < input.length) {
        cursor += 2;
        continue;
      }
      if (input[cursor] === "(") depth++;
      if (input[cursor] === ")") depth--;
      if (depth === 0) break;
      cursor++;
    }
    if (depth !== 0 || cursor >= input.length) return null;
    title = input.slice(titleStart, cursor);
    cursor++;
  }

  while (cursor < input.length && /[ \t\n]/.test(input[cursor])) cursor++;
  if (input[cursor] !== ")") return null;

  return {
    destination: decodeBackslashEscapes(destination),
    title: decodeHtmlEntitiesOnce(decodeBackslashEscapes(title)),
    end: cursor + 1,
  };
}

function plainText(nodes: readonly MarkdownInlineNode[]): string {
  let output = "";
  for (const node of nodes) {
    switch (node.type) {
      case "text":
      case "code":
        output += node.value;
        break;
      case "image":
        output += node.alt;
        break;
      case "break":
        output += "\n";
        break;
      case "strong":
      case "em":
      case "del":
      case "link":
        output += plainText(node.children);
        break;
      default:
        break;
    }
  }
  return output;
}

interface HtmlConstruct {
  raw: string;
  end: number;
}

function consumeHtmlConstruct(input: string, start: number): HtmlConstruct | null {
  if (input.startsWith("<!--", start)) {
    const close = input.indexOf("-->", start + 4);
    const end = close === -1 ? input.length : close + 3;
    return { raw: input.slice(start, end), end };
  }

  if (input.startsWith("<![CDATA[", start)) {
    const close = input.indexOf("]]>", start + 9);
    const end = close === -1 ? input.length : close + 3;
    return { raw: input.slice(start, end), end };
  }

  if (input.startsWith("<!", start) || input.startsWith("<?", start)) {
    const close = input.indexOf(">", start + 2);
    const end = close === -1 ? input.length : close + 1;
    return { raw: input.slice(start, end), end };
  }

  const tag = scanHtmlTag(input, start);
  if (tag === null) return null;

  if (
    !tag.closing &&
    DROP_CONTENT_TAGS.has(tag.name) &&
    (!tag.selfClosing || SELF_CLOSING_IGNORED.has(tag.name))
  ) {
    const closePattern = new RegExp(`</${tag.name}\\s*>`, "ig");
    closePattern.lastIndex = tag.end;
    const match = closePattern.exec(input);
    if (match === null) return { raw: input.slice(start), end: input.length };
    const end = match.index + match[0].length;
    return { raw: input.slice(start, end), end };
  }

  return { raw: input.slice(start, tag.end), end: tag.end };
}

function tryParseLink(
  input: string,
  start: number,
  image: boolean,
  context: InlineContext,
  depth: number,
): { node: MarkdownLinkNode | MarkdownImageNode; end: number } | null {
  const contentStart = start + (image ? 2 : 1);
  const closeBracket = findClosingBracket(input, contentStart);
  if (closeBracket === -1) return null;

  const labelText = input.slice(contentStart, closeBracket);
  let cursor = closeBracket + 1;
  let destination: string | null = null;
  let title = "";
  let end = cursor;

  if (input[cursor] === "(") {
    const parsed = parseInlineDestination(input, cursor + 1);
    if (parsed === null) return null;
    destination = parsed.destination;
    title = parsed.title;
    end = parsed.end;
  } else {
    let definition: LinkDefinition | undefined;
    if (input[cursor] === "[") {
      const labelEnd = input.indexOf("]", cursor + 1);
      if (labelEnd === -1) return null;
      const rawLabel = input.slice(cursor + 1, labelEnd);
      definition = context.definitions.get(
        normalizeLabel(rawLabel.length > 0 ? rawLabel : labelText),
      );
      if (definition === undefined) return null;
      end = labelEnd + 1;
    } else {
      definition = context.definitions.get(normalizeLabel(labelText));
      if (definition === undefined) return null;
    }
    destination = definition.destination;
    title = definition.title;
  }

  const safeUrl = sanitizeUrl(destination, context.options);

  if (image) {
    const children = parseInline(labelText, context, depth + 1);
    return {
      node: {
        type: "image",
        src: safeUrl,
        alt: plainText(children),
        title,
      },
      end,
    };
  }

  return {
    node: {
      type: "link",
      href: safeUrl,
      title,
      children: parseInline(labelText, context, depth + 1),
    },
    end,
  };
}

function parseInline(input: string, context: InlineContext, depth = 0): MarkdownInlineNode[] {
  if (depth > context.maxDepth) return input.length > 0 ? [{ type: "text", value: input }] : [];

  const nodes: MarkdownInlineNode[] = [];
  let buffer = "";
  let cursor = 0;

  const flush = (): void => {
    if (buffer.length > 0) {
      nodes.push({ type: "text", value: decodeHtmlEntitiesOnce(buffer) });
      buffer = "";
    }
  };

  while (cursor < input.length) {
    const char = input[cursor];

    if (char === "\\") {
      const next = input[cursor + 1];
      if (next === "\n") {
        flush();
        nodes.push({ type: "break" });
        cursor += 2;
        continue;
      }
      if (next !== undefined && ASCII_PUNCTUATION.test(next)) {
        buffer += next;
        cursor += 2;
        continue;
      }
      buffer += "\\";
      cursor++;
      continue;
    }

    if (char === "\n") {
      flush();
      const previous = nodes[nodes.length - 1];
      if (previous?.type === "text" && /  $/.test(previous.value)) {
        previous.value = previous.value.replace(/ +$/, "");
        nodes.push({ type: "break" });
      } else {
        nodes.push({ type: "text", value: " " });
      }
      cursor++;
      continue;
    }

    if (char === "`") {
      const run = countRun(input, cursor, "`");
      const close = findClosingDelimiter(input, cursor + run, "`", run);
      if (close !== -1) {
        flush();
        let content = input.slice(cursor + run, close).replace(/\n/g, " ");
        if (content.startsWith(" ") && content.endsWith(" ") && content.trim().length > 0) {
          content = content.slice(1, -1);
        }
        nodes.push({ type: "code", value: content });
        cursor = close + run;
        continue;
      }
      buffer += "`".repeat(run);
      cursor += run;
      continue;
    }

    if (char === "!" && input[cursor + 1] === "[") {
      const parsed = tryParseLink(input, cursor, true, context, depth);
      if (parsed !== null) {
        flush();
        nodes.push(parsed.node);
        cursor = parsed.end;
        continue;
      }
    }

    if (char === "[") {
      const parsed = tryParseLink(input, cursor, false, context, depth);
      if (parsed !== null) {
        flush();
        nodes.push(parsed.node);
        cursor = parsed.end;
        continue;
      }
    }

    if (char === "<") {
      const close = input.indexOf(">", cursor + 1);
      if (close !== -1) {
        const inner = input.slice(cursor + 1, close);
        if (/^[A-Za-z][A-Za-z0-9+.-]{1,31}:[^\s<>]*$/.test(inner)) {
          flush();
          const safe = sanitizeUrl(inner, context.options);
          if (safe !== null) {
            nodes.push({
              type: "link",
              href: safe,
              title: "",
              children: [{ type: "text", value: inner }],
            });
          } else {
            nodes.push({ type: "text", value: inner });
          }
          cursor = close + 1;
          continue;
        }
        if (/^[^\s<>@]+@[^\s<>@]+$/.test(inner)) {
          flush();
          nodes.push({ type: "text", value: inner });
          cursor = close + 1;
          continue;
        }
      }

      const construct = consumeHtmlConstruct(input, cursor);
      if (construct !== null) {
        flush();
        const safe =
          construct.raw.includes("</") && /^<(script|style|iframe|object|svg|math|template|textarea|title)/i.test(construct.raw)
            ? sanitizeHtml(construct.raw, context.options)
            : sanitizeHtmlToken(construct.raw, context.options);
        if (safe.length > 0) nodes.push({ type: "html", value: safe });
        cursor = construct.end;
        continue;
      }

      buffer += "<";
      cursor++;
      continue;
    }

    if (char === "*" || char === "_" || (char === "~" && input[cursor + 1] === "~")) {
      const run = char === "~" ? 2 : countRun(input, cursor, char);
      const lengths = char === "~" ? [2] : [3, 2, 1];
      let matched = false;

      for (const length of lengths) {
        if (run < length) continue;
        if (char === "_") {
          const before = input[cursor - 1];
          const after = input[cursor + length];
          if (before !== undefined && after !== undefined && /[A-Za-z0-9]/.test(before) && /[A-Za-z0-9]/.test(after)) {
            continue;
          }
        }
        const close = findClosingDelimiter(input, cursor + length, char, length);
        if (close === -1 || close === cursor + length) continue;
        const content = input.slice(cursor + length, close);
        if (content.trim().length === 0) continue;

        flush();
        const children = parseInline(content, context, depth + 1);
        if (length === 3) {
          nodes.push({ type: "strong", children: [{ type: "em", children }] });
        } else if (length === 2) {
          nodes.push({ type: char === "~" ? "del" : "strong", children });
        } else {
          nodes.push({ type: "em", children });
        }
        cursor = close + length;
        matched = true;
        break;
      }

      if (matched) continue;
      buffer += char;
      cursor++;
      continue;
    }

    buffer += char;
    cursor++;
  }

  flush();
  return mergeTextNodes(nodes);
}

function renderInline(nodes: readonly MarkdownInlineNode[]): string {
  let output = "";
  for (const node of nodes) {
    switch (node.type) {
      case "text":
        output += escapeHtmlText(node.value);
        break;
      case "code":
        output += `<code>${escapeHtmlText(node.value)}</code>`;
        break;
      case "break":
        output += "<br>";
        break;
      case "html":
        output += node.value;
        break;
      case "strong":
        output += `<strong>${renderInline(node.children)}</strong>`;
        break;
      case "em":
        output += `<em>${renderInline(node.children)}</em>`;
        break;
      case "del":
        output += `<del>${renderInline(node.children)}</del>`;
        break;
      case "link": {
        const title = node.title.length > 0 ? ` title="${escapeAttribute(node.title)}"` : "";
        if (node.href === null) {
          output += renderInline(node.children);
        } else {
          output += `<a href="${escapeAttribute(node.href)}"${title}>${renderInline(node.children)}</a>`;
        }
        break;
      }
      case "image": {
        if (node.src === null) {
          output += escapeHtmlText(node.alt);
        } else {
          const title = node.title.length > 0 ? ` title="${escapeAttribute(node.title)}"` : "";
          output += `<img src="${escapeAttribute(node.src)}" alt="${escapeAttribute(node.alt)}"${title}>`;
        }
        break;
      }
      default:
        break;
    }
  }
  return output;
}

function renderBlock(node: MarkdownBlockNode): string {
  switch (node.type) {
    case "paragraph": {
      const content = renderInline(node.children);
      return content.length > 0 ? `<p>${content}</p>` : "";
    }
    case "heading":
      return `<h${node.level}>${renderInline(node.children)}</h${node.level}>`;
    case "blockquote":
      return `<blockquote>\n${node.children
        .map(renderBlock)
        .filter((value) => value.length > 0)
        .join("\n")}\n</blockquote>`;
    case "list": {
      const tag = node.ordered ? "ol" : "ul";
      const start = node.ordered && node.start !== 1 ? ` start="${node.start}"` : "";
      const items = node.items
        .map(
          (item) =>
            `<li>${item
              .map(renderBlock)
              .filter((value) => value.length > 0)
              .join("\n")}</li>`,
        )
        .join("\n");
      return `<${tag}${start}>\n${items}\n</${tag}>`;
    }
    case "codeBlock": {
      const language = /^[A-Za-z0-9_+-]{1,64}$/.test(node.language)
        ? ` class="language-${escapeAttribute(node.language)}"`
        : "";
      return `<pre><code${language}>${escapeHtmlText(node.value)}</code></pre>`;
    }
    case "thematicBreak":
      return "<hr>";
    case "html":
      return node.value;
    case "table": {
      const cell = (tag: string, content: MarkdownInlineNode[], index: number): string =>
        `<${tag}>${renderInline(content)}</${tag}>`;
      const header = node.header
        .map((content, index) => cell("th", content, index))
        .join("");
      const body = node.rows
        .map((row) => `<tr>${row.map((content, index) => cell("td", content, index)).join("")}</tr>`)
        .join("\n");
      return `<table>\n<thead>\n<tr>${header}</tr>\n</thead>\n<tbody>\n${body}\n</tbody>\n</table>`;
    }
    default:
      return "";
  }
}

/**
 * Parse Markdown into an AST. Link/image destinations are sanitized while the
 * tree is built, and raw HTML subtrees are sanitized before being stored.
 */
export function parseMarkdown(
  markdown: string,
  options: MarkdownOptions = {},
): MarkdownDocument {
  const source = typeof markdown === "string" ? normalizeNewlines(markdown) : "";
  const lines = source.split("\n");
  const definitions = new Map<string, LinkDefinition>();
  let fence: { char: string; length: number } | null = null;

  for (let index = 0; index < lines.length; index++) {
    if (fence !== null) {
      const closing = new RegExp(
        `^ {0,3}${fence.char === "`" ? "`" : "~"}{${fence.length},}[ \\t]*$`,
      );
      if (closing.test(lines[index])) fence = null;
      continue;
    }

    const opening = FENCE_OPEN.exec(lines[index]);
    if (opening !== null) {
      fence = { char: opening[2][0], length: opening[2].length };
      continue;
    }

    const parsed = matchLinkDefinition(lines[index]);
    if (parsed !== null) {
      definitions.set(parsed.label, parsed.definition);
      lines[index] = "";
    }
  }

  const context: InlineContext = {
    options,
    definitions,
    maxDepth: options.maxInlineDepth ?? 32,
  };

  return { type: "root", children: parseBlocks(lines, context) };
}

export function renderMarkdownAst(document: MarkdownDocument): string {
  return document.children
    .map(renderBlock)
    .filter((value) => value.length > 0)
    .join("\n");
}

/** Parse and render Markdown with a whitelist-only HTML result. */
export function sanitizeMarkdown(markdown: string, options: MarkdownOptions = {}): string {
  return renderMarkdownAst(parseMarkdown(markdown, options));
}
