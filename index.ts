export interface ElementNode {
  type: "element";
  name: string;
  attributes: Record<string, string>;
  children: Node[];
}

export interface TextNode {
  type: "text";
  text: string;
}

export type Node = ElementNode | TextNode;

const HTML_INTEGRATION_ENCODINGS = new Set([
  "text/html",
  "application/xhtml+xml",
]);

export function isHtmlIntegrationPointEncoding(
  encoding: string | null | undefined,
): boolean {
  if (encoding == null) return false;
  const normalized = encoding.trim().toLowerCase();
  return HTML_INTEGRATION_ENCODINGS.has(normalized);
}

const ENCODING_ATTR_RE =
  /\bencoding\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+))/i;

export function extractEncoding(startTag: string): string | null {
  const match = ENCODING_ATTR_RE.exec(startTag);
  if (!match) return null;
  return match[1] ?? match[2] ?? match[3] ?? null;
}

const TAG_RE = /<!--[\s\S]*?-->|<\/?[A-Za-z][^>]*>|[^<]+|</g;
const TAG_NAME_RE = /^<\/?\s*([A-Za-z][^\s/>]*)/;
const ATTR_RE =
  /([^\s"'=<>`/]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;

function parseAttributes(tagSource: string): Record<string, string> {
  const attributes: Record<string, string> = {};
  const nameMatch = TAG_NAME_RE.exec(tagSource);
  if (!nameMatch) return attributes;
  const rest = tagSource.slice(tagSource.indexOf(nameMatch[1]) + nameMatch[1].length);
  for (const match of rest.matchAll(ATTR_RE)) {
    const name = match[1].toLowerCase();
    if (name === "/" || name === ">") continue;
    attributes[name] = match[2] ?? match[3] ?? match[4] ?? "";
  }
  return attributes;
}

export function parseHtmlFragment(source: string): Node[] {
  const root: ElementNode = {
    type: "element",
    name: "#root",
    attributes: {},
    children: [],
  };
  const stack: ElementNode[] = [root];
  for (const token of source.match(TAG_RE) ?? []) {
    if (token.startsWith("<!--")) continue;
    if (token.startsWith("</")) {
      const nameMatch = TAG_NAME_RE.exec(token);
      if (!nameMatch) continue;
      const name = nameMatch[1].toLowerCase();
      for (let i = stack.length - 1; i > 0; i--) {
        if (stack[i].name === name) {
          stack.length = i;
          break;
        }
      }
      continue;
    }
    if (token.startsWith("<") && token.length > 1) {
      const nameMatch = TAG_NAME_RE.exec(token);
      if (!nameMatch) continue;
      const element: ElementNode = {
        type: "element",
        name: nameMatch[1].toLowerCase(),
        attributes: parseAttributes(token),
        children: [],
      };
      stack[stack.length - 1].children.push(element);
      if (!token.endsWith("/>")) stack.push(element);
      continue;
    }
    stack[stack.length - 1].children.push({ type: "text", text: token });
  }
  return root.children;
}

export function parseAnnotationXml(
  encoding: string | null | undefined,
  content: string,
): Node[] {
  if (isHtmlIntegrationPointEncoding(encoding)) {
    return parseHtmlFragment(content);
  }
  if (content.length === 0) return [];
  return [{ type: "text", text: content }];
}

export function parseAnnotationXmlTag(
  startTag: string,
  content: string,
): Node[] {
  return parseAnnotationXml(extractEncoding(startTag), content);
}
