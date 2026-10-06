import { EachNode, IfNode, IncludeNode, Node, Pos, Span, TextNode, VarNode } from './ast';
import { TemplateSyntaxError } from './errors';

const PATH_HEAD = /^@?[A-Za-z_][A-Za-z0-9_]*$/;
const PATH_SEGMENT = /^[A-Za-z_][A-Za-z0-9_]*$/;
const INCLUDE_NAME = /^[A-Za-z0-9_\-./]+$/;

interface Tag {
  raw: string;
  span: Span;
}

interface BlockFrame {
  type: 'if' | 'each';
  node: IfNode | EachNode;
  inElse: boolean;
}

/** Parse a template source string into an AST. Throws TemplateSyntaxError with a span. */
export function parse(templateName: string, source: string): Node[] {
  const tags = lex(templateName, source);
  const root: Node[] = [];
  const stack: BlockFrame[] = [];

  const currentList = (): Node[] => {
    if (stack.length === 0) return root;
    const top = stack[stack.length - 1];
    if (top.type === 'if') {
      const node = top.node as IfNode;
      return top.inElse ? node.else : node.then;
    }
    return (top.node as EachNode).body;
  };

  for (const item of tags) {
    if (item.kind === 'text') {
      currentList().push({ kind: 'text', text: item.text, span: item.span });
      continue;
    }
    const { raw, span } = item.tag;
    const head = raw.split(/\s+/)[0];
    if (head === '#if') {
      const path = parsePath(restOf(raw, '#if'), span);
      stack.push({ type: 'if', node: { kind: 'if', path, then: [], else: [], span }, inElse: false });
    } else if (head === '#each') {
      const { path, itemName } = parseEach(restOf(raw, '#each'), span);
      stack.push({ type: 'each', node: { kind: 'each', path, itemName, body: [], span }, inElse: false });
    } else if (head === 'else') {
      requireNoArgs(restOf(raw, 'else'), span);
      const top = stack[stack.length - 1];
      if (!top || top.type !== 'if') fail(span, "'{{else}}' without a matching '{{#if}}'");
      if (top.inElse) fail(span, "duplicate '{{else}}' in the same '{{#if}}' block");
      top.inElse = true;
    } else if (head === '/if' || head === '/each') {
      requireNoArgs(restOf(raw, head), span);
      const want = head === '/if' ? 'if' : 'each';
      const top = stack[stack.length - 1];
      if (!top || top.type !== want) fail(span, `'{{${head}}}' without a matching '{{#${want}}}'`);
      stack.pop();
      currentList().push(top.node);
    } else if (head === '>') {
      const name = restOf(raw, '>');
      if (!INCLUDE_NAME.test(name)) fail(span, `invalid include name '${name}'`);
      const node: IncludeNode = { kind: 'include', name, span };
      currentList().push(node);
    } else if (head.startsWith('#') || head.startsWith('/')) {
      fail(span, `unknown block tag '${head}'`);
    } else {
      const path = parsePath(raw, span);
      const node: VarNode = { kind: 'var', path, span };
      currentList().push(node);
    }
  }

  if (stack.length > 0) {
    const open = stack[stack.length - 1].node;
    fail(open.span, `unclosed '{{#${stack[stack.length - 1].type}}}' block`);
  }
  return root;
}

type LexItem = { kind: 'text'; text: string; span: Span } | { kind: 'tag'; tag: Tag };

function lex(template: string, source: string): LexItem[] {
  const items: LexItem[] = [];
  let offset = 0;
  let line = 1;
  let col = 1;

  const pos = (): Pos => ({ offset, line, col });
  const advance = (text: string): void => {
    for (const ch of text) {
      offset += 1;
      if (ch === '\n') {
        line += 1;
        col = 1;
      } else {
        col += 1;
      }
    }
  };

  let cursor = 0;
  while (cursor < source.length) {
    const open = source.indexOf('{{', cursor);
    if (open === -1) {
      const text = source.slice(cursor);
      const start = pos();
      advance(text);
      if (text.length > 0) items.push({ kind: 'text', text, span: { template, start, end: pos() } });
      break;
    }
    if (open > cursor) {
      const text = source.slice(cursor, open);
      const start = pos();
      advance(text);
      items.push({ kind: 'text', text, span: { template, start, end: pos() } });
    }
    const start = pos();
    const close = source.indexOf('}}', open + 2);
    if (close === -1) {
      throw new TemplateSyntaxError('unclosed tag: missing }}', { template, start, end: pos() }, []);
    }
    const raw = source.slice(open + 2, close);
    advance(source.slice(open, close + 2));
    cursor = close + 2;
    const span: Span = { template, start, end: pos() };
    const trimmed = raw.trim();
    if (trimmed.length === 0) fail(span, 'empty tag');
    items.push({ kind: 'tag', tag: { raw: trimmed, span } });
  }
  return items;
}

function restOf(raw: string, keyword: string): string {
  return raw.slice(keyword.length).trim();
}

function parsePath(text: string, span: Span): string[] {
  if (text.length === 0) fail(span, 'expected a variable path');
  const parts = text.split('.');
  if (!PATH_HEAD.test(parts[0])) fail(span, `invalid variable path '${text}'`);
  for (const segment of parts.slice(1)) {
    if (!PATH_SEGMENT.test(segment)) fail(span, `invalid variable path '${text}'`);
  }
  if (parts[0].startsWith('@') && parts.length > 1) fail(span, `loop builtin '${parts[0]}' cannot be nested`);
  return parts;
}

function parseEach(text: string, span: Span): { path: string[]; itemName: string } {
  const match = /^(\S+)(?:\s+as\s+(\S+))?\s*$/.exec(text);
  if (!match) fail(span, "expected '{{#each <path> as <name>}}'");
  const path = parsePath(match[1], span);
  const itemName = match[2] !== undefined ? match[2] : 'item';
  if (!PATH_SEGMENT.test(itemName) || itemName.startsWith('@')) {
    fail(span, `invalid loop variable name '${itemName}'`);
  }
  return { path, itemName };
}

function requireNoArgs(text: string, span: Span): void {
  if (text.length > 0) fail(span, `unexpected arguments '${text}'`);
}

function fail(span: Span, message: string): never {
  throw new TemplateSyntaxError(message, span, []);
}
