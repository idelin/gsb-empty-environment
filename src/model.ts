/**
 * 文档模型：节点 = 文本 + 无序格式集合 + 可选锚点。
 * 锚点带左右亲和性；链接不可嵌套（同一节点至多一个 link 标记）。
 */

export type MarkType = 'bold' | 'italic' | 'underline' | 'strike' | 'code' | 'link';

export type Affinity = 'left' | 'right';

export interface Mark {
  type: MarkType;
  attrs?: Record<string, string>;
}

export interface Anchor {
  id: string;
  affinity: Affinity;
}

export interface DocNode {
  id: string;
  text: string;
  marks: Mark[];
  anchor?: Anchor;
}

export interface Doc {
  nodes: DocNode[];
}

export interface FlatChar {
  char: string;
  marks: string[];
}

export function markKey(mark: Mark): string {
  let key = mark.type;
  if (mark.attrs) {
    const names = Object.keys(mark.attrs).sort();
    for (const name of names) {
      key += '|' + JSON.stringify(name) + '=' + JSON.stringify(mark.attrs[name]);
    }
  }
  return key;
}

function hasLink(marks: Mark[]): boolean {
  for (const mark of marks) {
    if (mark.type === 'link') return true;
  }
  return false;
}

export function normalizeMarks(marks: Mark[]): Mark[] {
  const seen: Set<string> = new Set();
  const out: Mark[] = [];
  for (const mark of marks) {
    const key = markKey(mark);
    if (seen.has(key)) continue;
    if (mark.type === 'link' && hasLink(out)) continue;
    seen.add(key);
    out.push(mark);
  }
  return out;
}

export function sameMarks(a: Mark[], b: Mark[]): boolean {
  if (a.length !== b.length) return false;
  const keys: Set<string> = new Set();
  for (const mark of a) keys.add(markKey(mark));
  for (const mark of b) {
    if (!keys.has(markKey(mark))) return false;
  }
  return true;
}

export function docLength(doc: Doc): number {
  let length = 0;
  for (const node of doc.nodes) length += node.text.length;
  return length;
}

export function flatten(doc: Doc): FlatChar[] {
  const out: FlatChar[] = [];
  for (const node of doc.nodes) {
    const keys: string[] = [];
    for (const mark of normalizeMarks(node.marks)) keys.push(markKey(mark));
    keys.sort();
    for (const char of node.text) {
      out.push({ char, marks: keys.slice() });
    }
  }
  return out;
}

export function render(doc: Doc): string {
  let text = '';
  for (const node of doc.nodes) text += node.text;
  return text;
}

export function equivalent(a: Doc, b: Doc): boolean {
  const fa = flatten(a);
  const fb = flatten(b);
  if (fa.length !== fb.length) return false;
  for (let i = 0; i < fa.length; i++) {
    if (fa[i].char !== fb[i].char) return false;
    if (fa[i].marks.join('') !== fb[i].marks.join('')) return false;
  }
  return true;
}
