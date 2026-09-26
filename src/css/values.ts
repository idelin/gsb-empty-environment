// Value tokenization: literal text interleaved with var() references.
// Fallbacks may nest var() calls arbitrarily deep.

export type ValueToken =
  | { kind: 'text'; text: string }
  | { kind: 'var'; name: string; fallback: ValueToken[] | null };

export function parseValueTokens(input: string): ValueToken[] {
  const [tokens] = parseTokens(input, 0, false);
  return tokens;
}

function parseTokens(s: string, start: number, nested: boolean): [ValueToken[], number] {
  const tokens: ValueToken[] = [];
  let buf = '';
  let depth = 0;
  let i = start;
  const flush = (): void => {
    if (buf.length > 0) {
      tokens.push({ kind: 'text', text: buf });
      buf = '';
    }
  };
  while (i < s.length) {
    if (s[i] === 'v' && s.slice(i, i + 4).toLowerCase() === 'var(') {
      flush();
      const [v, next] = parseVar(s, i + 4);
      tokens.push(v);
      i = next;
      continue;
    }
    const ch = s[i];
    if (ch === '(') {
      depth++;
      buf += ch;
      i++;
      continue;
    }
    if (ch === ')') {
      if (depth === 0 && nested) {
        flush();
        return [tokens, i + 1];
      }
      if (depth > 0) depth--;
      buf += ch;
      i++;
      continue;
    }
    buf += ch;
    i++;
  }
  flush();
  return [tokens, i];
}

function parseVar(s: string, i: number): [Extract<ValueToken, { kind: 'var' }>, number] {
  while (i < s.length && /\s/.test(s[i])) i++;
  const nameStart = i;
  while (i < s.length && !/[\s,)]/.test(s[i])) i++;
  const name = s.slice(nameStart, i);
  if (!name.startsWith('--')) throw new Error(`invalid var() reference: "${name}"`);
  while (i < s.length && /\s/.test(s[i])) i++;
  if (s[i] === ')') return [{ kind: 'var', name, fallback: null }, i + 1];
  if (s[i] !== ',') throw new Error(`invalid var() syntax near "${s.slice(i, i + 12)}"`);
  const [fallback, next] = parseTokens(s, i + 1, true);
  return [{ kind: 'var', name, fallback }, next];
}

export function tokensToString(tokens: ValueToken[]): string {
  let out = '';
  for (const t of tokens) {
    if (t.kind === 'text') out += t.text;
    else out += t.fallback ? `var(${t.name},${tokensToString(t.fallback)})` : `var(${t.name})`;
  }
  return out;
}
