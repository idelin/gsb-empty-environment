export type Namespace = "html" | "svg" | "math";

export interface ElementNode {
  type: "element";
  name: string;
  ns: Namespace;
  attrs: Array<[string, string]>;
  children: Node[];
}

export interface TextNode {
  type: "text";
  data: string;
}

export interface CommentNode {
  type: "comment";
  data: string;
}

export type Node = ElementNode | TextNode | CommentNode;

const VOID_ELEMENTS = new Set([
  "area", "base", "br", "col", "embed", "hr", "img", "input",
  "link", "meta", "param", "source", "track", "wbr",
]);

const RAW_TEXT_ELEMENTS = new Set(["script", "style", "textarea", "title"]);

interface OpenElement {
  name: string;
  ns: Namespace;
  node: ElementNode;
}

function parseAttrs(source: string, start: number): { attrs: Array<[string, string]>; next: number } {
  const attrs: Array<[string, string]> = [];
  let i = start;
  while (i < source.length) {
    while (i < source.length && /\s/.test(source[i])) i++;
    if (i >= source.length || source[i] === ">" || source[i] === "/") break;
    let name = "";
    while (i < source.length && !/[\s=/>]/.test(source[i])) {
      name += source[i];
      i++;
    }
    let value = "";
    while (i < source.length && /\s/.test(source[i])) i++;
    if (source[i] === "=") {
      i++;
      while (i < source.length && /\s/.test(source[i])) i++;
      const quote = source[i];
      if (quote === '"' || quote === "'") {
        i++;
        const end = source.indexOf(quote, i);
        value = end === -1 ? source.slice(i) : source.slice(i, end);
        i = end === -1 ? source.length : end + 1;
      } else {
        const startVal = i;
        while (i < source.length && !/[\s>]/.test(source[i])) i++;
        value = source.slice(startVal, i);
      }
    }
    if (name) attrs.push([name, value]);
  }
  return { attrs, next: i };
}

/**
 * Parse an HTML fragment with strict foreign-content adjudication:
 * once <svg> or <math> is entered, the parser only returns to the HTML
 * namespace at the matching end tag. HTML integration points
 * (foreignObject/desc/title) are NOT honored, so a payload that would
 * re-flow into HTML as markup after an XML round-trip stays inert.
 */
export function parseFragment(html: string): Node[] {
  const roots: Node[] = [];
  const stack: OpenElement[] = [];
  const currentNs = (): Namespace => (stack.length ? stack[stack.length - 1].ns : "html");
  const append = (node: Node) => {
    if (stack.length) stack[stack.length - 1].node.children.push(node);
    else roots.push(node);
  };

  let i = 0;
  while (i < html.length) {
    const lt = html.indexOf("<", i);
    if (lt === -1) {
      append({ type: "text", data: html.slice(i) });
      break;
    }
    if (lt > i) append({ type: "text", data: html.slice(i, lt) });

    if (html.startsWith("<!--", lt)) {
      const end = html.indexOf("-->", lt + 4);
      const stop = end === -1 ? html.length : end;
      append({ type: "comment", data: html.slice(lt + 4, stop) });
      i = end === -1 ? html.length : stop + 3;
      continue;
    }
    if (html.startsWith("<!", lt) || html.startsWith("<?", lt)) {
      const end = html.indexOf(">", lt);
      i = end === -1 ? html.length : end + 1;
      continue;
    }

    const closeMatch = /^<\/\s*([a-zA-Z][^\s/>]*)\s*>/.exec(html.slice(lt));
    if (closeMatch) {
      const name = closeMatch[1].toLowerCase();
      // Only a matching end tag leaves the current foreign scope.
      for (let k = stack.length - 1; k >= 0; k--) {
        if (stack[k].name === name) {
          stack.length = k;
          break;
        }
      }
      i = lt + closeMatch[0].length;
      continue;
    }

    const openMatch = /^<\s*([a-zA-Z][^\s/>]*)/.exec(html.slice(lt));
    if (!openMatch) {
      append({ type: "text", data: "<" });
      i = lt + 1;
      continue;
    }
    const rawName = openMatch[1];
    const name = rawName.toLowerCase();
    const { attrs, next } = parseAttrs(html, lt + openMatch[0].length);
    let j = next;
    while (j < html.length && /\s/.test(html[j])) j++;
    const selfClosing = html[j] === "/";
    if (selfClosing) j++;
    while (j < html.length && /\s/.test(html[j])) j++;
    i = html[j] === ">" ? j + 1 : j;

    const parentNs = currentNs();
    let ns: Namespace = parentNs;
    if (parentNs === "html") {
      if (name === "svg") ns = "svg";
      else if (name === "math") ns = "math";
    }
    const node: ElementNode = { type: "element", name, ns, attrs, children: [] };
    append(node);

    const isVoid = ns === "html" && VOID_ELEMENTS.has(name);
    if (!selfClosing && !isVoid) {
      if (ns === "html" && RAW_TEXT_ELEMENTS.has(name)) {
        const closeTag = "</" + name;
        const end = html.toLowerCase().indexOf(closeTag, i);
        const stop = end === -1 ? html.length : end;
        if (stop > i) node.children.push({ type: "text", data: html.slice(i, stop) });
        if (end === -1) {
          i = html.length;
        } else {
          const gt = html.indexOf(">", end + closeTag.length);
          i = gt === -1 ? html.length : gt + 1;
        }
      } else {
        stack.push({ name, ns, node });
      }
    }
  }
  return roots;
}

/** Remove every element (and its subtree) whose tag name matches. */
export function stripElements(nodes: Node[], names: Set<string>): Node[] {
  const out: Node[] = [];
  for (const node of nodes) {
    if (node.type === "element") {
      if (names.has(node.name)) continue;
      node.children = stripElements(node.children, names);
    }
    out.push(node);
  }
  return out;
}

function escapeText(data: string): string {
  return data.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttr(data: string): string {
  return data.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}

export function serialize(nodes: Node[]): string {
  let out = "";
  for (const node of nodes) {
    if (node.type === "text") {
      out += escapeText(node.data);
    } else if (node.type === "comment") {
      out += "<!--" + node.data.replace(/-->/g, "--&gt;") + "-->";
    } else {
      const attrs = node.attrs.map(([k, v]) => ` ${k}="${escapeAttr(v)}"`).join("");
      if (node.ns === "html" && VOID_ELEMENTS.has(node.name)) {
        out += `<${node.name}${attrs}>`;
      } else {
        out += `<${node.name}${attrs}>${serialize(node.children)}</${node.name}>`;
      }
    }
  }
  return out;
}

/**
 * Adjudicate an untrusted HTML fragment for the document pipeline.
 * Script elements never survive, whether they sit in pure HTML, inside
 * svg/math foreign content, or after a foreign scope closes. Legitimate
 * svg/math graphics markup is preserved.
 */
export function sanitizeFragment(html: string): string {
  const tree = parseFragment(html);
  return serialize(stripElements(tree, new Set(["script"])));
}

/** Count elements with the given tag name in a parsed/serialized fragment. */
export function countElements(html: string, name: string): number {
  const wanted = name.toLowerCase();
  const walk = (nodes: Node[]): number =>
    nodes.reduce((sum, node) => {
      if (node.type !== "element") return sum;
      return sum + (node.name === wanted ? 1 : 0) + walk(node.children);
    }, 0);
  return walk(parseFragment(html));
}
