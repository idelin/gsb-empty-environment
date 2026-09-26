// Core data model: element tree, selectors, rules, cascade candidates, traces.

export interface ElNode {
  id: number;
  tag: string;
  name?: string; // element id used by #selectors
  classes: string[];
  states: Set<string>; // pseudo-class state flags: 'hover', 'focus', ...
  text?: string;
  children: ElNode[];
  parent: ElNode | null;
}

export interface Pseudo {
  name: string; // 'first-child' | 'last-child' | 'nth-child' | 'hover' | 'focus' | 'not'
  arg?: string;
  not?: Compound; // parsed argument for :not(...)
}

export interface Compound {
  tag?: string;
  id?: string;
  classes: string[];
  pseudos: Pseudo[];
}

export interface SelectorPart {
  compound: Compound;
  combinator: ' ' | '>' | null; // combinator linking this part to the previous part
}

export interface ComplexSelector {
  parts: SelectorPart[]; // left-to-right
}

export interface Declaration {
  prop: string;
  value: string;
}

export interface Rule {
  selector: ComplexSelector;
  declarations: Declaration[];
  order: number; // source order of the rule
  source: string; // human-readable selector text
}

export type Specificity = [number, number, number]; // [id, class/pseudo, tag]

export interface Candidate {
  source: string; // rule source text or 'inline'
  specificity: Specificity;
  order: number;
  seq: number; // global cascade sequence (tie-break inside a rule)
  value: string;
  inline: boolean;
}

export type ChainOutcome = 'applied' | 'lost-specificity' | 'lost-order' | 'invalid-var';

export interface ChainEntry {
  source: string;
  specificity: Specificity;
  order: number;
  value: string;
  outcome: ChainOutcome;
}

export interface VarStep {
  name: string;
  status: 'resolved' | 'fallback-taken' | 'invalid';
  value?: string;
  reason?: string; // invalidation condition, e.g. 'unset' or 'cyclic: --a -> --b -> --a'
  fallback?: string; // fallback expression that was used
}

export type ValueSource = 'declared' | 'inherited' | 'initial';

export interface PropertyTrace {
  property: string;
  value: string | null; // null => invalid (guaranteed-invalid custom property)
  source: ValueSource | 'invalid';
  note?: string; // e.g. 'invalid-at-computed-value-time'
  chain: ChainEntry[]; // cascade candidates, winner first
  vars: VarStep[]; // variable resolution / fallback path
}

export interface ElementStyle {
  computed: Map<string, string>;
  traces: Map<string, PropertyTrace>;
}

export interface Snapshot {
  version: number;
  styles: Map<number, ElementStyle>;
}

// --- property tables -------------------------------------------------------

export const INHERITED_PROPS = new Set([
  'color',
  'font-size',
  'font-family',
  'font-weight',
  'font-style',
  'line-height',
  'text-align',
  'visibility',
]);

export const INITIAL_VALUES: Record<string, string> = {
  color: 'black',
  'font-size': '16px',
  'font-family': 'serif',
  'font-weight': '400',
  'font-style': 'normal',
  'line-height': 'normal',
  'text-align': 'left',
  visibility: 'visible',
  display: 'block',
  width: 'auto',
  height: 'auto',
  margin: '0px',
  padding: '0px',
  'background-color': 'transparent',
  'border-width': '0px',
};

export function initialValue(prop: string): string {
  return INITIAL_VALUES[prop] ?? 'none';
}

export function isCustomProp(prop: string): boolean {
  return prop.startsWith('--');
}

// --- tree helpers ----------------------------------------------------------

let nextNodeId = 1;

export function resetNodeIds(): void {
  nextNodeId = 1;
}

export interface ElInit {
  name?: string;
  classes?: string[];
  states?: string[];
  text?: string;
  children?: ElNode[];
}

export function el(tag: string, init: ElInit = {}): ElNode {
  const node: ElNode = {
    id: nextNodeId++,
    tag,
    name: init.name,
    classes: init.classes ?? [],
    states: new Set(init.states ?? []),
    text: init.text,
    children: [],
    parent: null,
  };
  for (const child of init.children ?? []) {
    node.children.push(child);
    child.parent = node;
  }
  return node;
}

export function indexTree(root: ElNode): Map<number, ElNode> {
  const map = new Map<number, ElNode>();
  const walk = (n: ElNode) => {
    map.set(n.id, n);
    for (const c of n.children) walk(c);
  };
  walk(root);
  return map;
}

export function preorder(root: ElNode): ElNode[] {
  const out: ElNode[] = [];
  const walk = (n: ElNode) => {
    out.push(n);
    for (const c of n.children) walk(c);
  };
  walk(root);
  return out;
}
