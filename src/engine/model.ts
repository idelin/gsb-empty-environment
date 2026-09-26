// Document tree model. Trees are built from plain specs (fixtures); structure
// is fixed after construction, while classes / states / text may mutate.

export interface DocNode {
  id: number;
  tag: string;
  idAttr: string | null;
  classes: Set<string>;
  states: Set<string>;
  text: string | null;
  children: DocNode[];
  parent: DocNode | null;
}

export interface NodeSpec {
  tag: string;
  id?: string;
  classes?: string[];
  states?: string[];
  text?: string;
  children?: NodeSpec[];
}

export function buildDocument(spec: NodeSpec): DocNode {
  let nextId = 1;
  const build = (s: NodeSpec, parent: DocNode | null): DocNode => {
    const node: DocNode = {
      id: nextId++,
      tag: s.tag.toLowerCase(),
      idAttr: s.id ?? null,
      classes: new Set(s.classes ?? []),
      states: new Set(s.states ?? []),
      text: s.text ?? null,
      children: [],
      parent,
    };
    node.children = (s.children ?? []).map((c) => build(c, node));
    return node;
  };
  return build(spec, null);
}

export function flatten(root: DocNode): DocNode[] {
  const out: DocNode[] = [];
  const walk = (n: DocNode): void => {
    out.push(n);
    for (const c of n.children) walk(c);
  };
  walk(root);
  return out;
}
