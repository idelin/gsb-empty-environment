// Seeded random fixtures: trees, stylesheets and mutations for differential testing.

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface NodeSpec {
  tag: string;
  id?: string;
  classes?: string[];
  states?: string[];
  text?: string;
  children?: NodeSpec[];
}

const TAGS = ['div', 'span', 'p', 'section', 'li'];
const CLASSES = ['a', 'b', 'c', 'd'];
const STATES = ['hover', 'active'];
const IDS = ['x1', 'x2'];

export function randomTree(rng: () => number, depth = 0): NodeSpec {
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rng() * arr.length)];
  const spec: NodeSpec = { tag: pick(TAGS) };
  if (rng() < 0.6) spec.classes = CLASSES.filter(() => rng() < 0.3);
  if (rng() < 0.15) spec.id = pick(IDS);
  if (rng() < 0.2) spec.states = STATES.filter(() => rng() < 0.5);
  if (depth < 3 && rng() < 0.75) {
    const count = 1 + Math.floor(rng() * 3);
    spec.children = Array.from({ length: count }, () => randomTree(rng, depth + 1));
  } else {
    spec.text = `t${Math.floor(rng() * 100)}`;
  }
  return spec;
}

const REGULAR_VALUES: Record<string, string[]> = {
  color: ['red', 'green', 'blue', 'var(--v0)', 'var(--v1, pink)', 'var(--v2, var(--v0, gray))'],
  'font-size': ['12', '16', '20', 'var(--v2, 14)'],
  'line-height': ['1', '1.5'],
  margin: ['0', '4', 'var(--v3, 2)'],
  padding: ['0', '8'],
  background: ['white', 'var(--v0, black)'],
  display: ['block', 'none'],
  width: ['auto', '100'],
};
const CUSTOM_VARS = ['--v0', '--v1', '--v2', '--v3'];
const CUSTOM_VALUES = ['5', 'red', 'var(--v0)', 'var(--v1)', 'var(--v2, 9)', 'var(--v3)', '10'];

function randomCompound(rng: () => number): string {
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rng() * arr.length)];
  let out = rng() < 0.5 ? pick(TAGS) : '';
  if (rng() < 0.5) out += `.${pick(CLASSES)}`;
  if (rng() < 0.1) out += `#${pick(IDS)}`;
  if (rng() < 0.15) out += `:${pick(STATES)}`;
  if (rng() < 0.1) out += ':first-child';
  return out || '*';
}

export function randomSelector(rng: () => number): string {
  const parts = [randomCompound(rng)];
  const extra = Math.floor(rng() * 3);
  for (let i = 0; i < extra; i++) {
    parts.push(rng() < 0.4 ? '>' : ' ', randomCompound(rng));
  }
  return parts.join(' ').replace(/ > /g, '>');
}

export function randomRule(rng: () => number): string {
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rng() * arr.length)];
  const decls: string[] = [];
  const count = 1 + Math.floor(rng() * 3);
  for (let i = 0; i < count; i++) {
    if (rng() < 0.45) {
      decls.push(`${pick(CUSTOM_VARS)}: ${pick(CUSTOM_VALUES)}`);
    } else {
      const prop = pick(Object.keys(REGULAR_VALUES));
      decls.push(`${prop}: ${pick(REGULAR_VALUES[prop])}`);
    }
  }
  return `${randomSelector(rng)} { ${decls.join('; ')}; }`;
}

export function randomStylesheet(rng: () => number): string[] {
  const count = 4 + Math.floor(rng() * 8);
  const rules = Array.from({ length: count }, () => randomRule(rng));
  // Frequently plant a direct variable cycle to stress cycle handling.
  if (rng() < 0.7) {
    rules.push(`.a { --v0: var(--v1); --v1: var(--v0${rng() < 0.5 ? ', 7' : ''}); }`);
  }
  return rules;
}
