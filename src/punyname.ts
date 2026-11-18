import { domainToASCII } from 'node:url';

export function canonicalLabels(name: string): string[] | null {
  if (typeof name !== 'string') return null;
  let trimmed = name.trim();
  if (trimmed.endsWith('.')) trimmed = trimmed.slice(0, -1);
  if (trimmed === '') return null;
  const labels = trimmed.split('.');
  const out: string[] = [];
  for (const raw of labels) {
    const ascii = domainToASCII(raw.toLowerCase());
    if (ascii === '') return null;
    out.push(ascii);
  }
  return out;
}

export function canonicalName(name: string): string | null {
  const labels = canonicalLabels(name);
  return labels ? labels.join('.') : null;
}

export function namesEqual(a: string, b: string): boolean {
  const la = canonicalLabels(a);
  const lb = canonicalLabels(b);
  if (!la || !lb || la.length !== lb.length) return false;
  for (let i = 0; i < la.length; i++) {
    if (la[i] !== lb[i]) return false;
  }
  return true;
}
