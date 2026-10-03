import { Fraction, bigGcd, bigLcm } from './fraction.ts';

const PLAIN_VARS = ['x', 'y', 'z'];

export function varName(index: number): string {
  return index < PLAIN_VARS.length ? PLAIN_VARS[index] : `x${index + 1}`;
}

/**
 * Format  normal·x + constant = 0  as e.g. "2x - 3y + 5 = 0".
 * Coefficients are scaled to coprime integers and sign-normalized so the
 * first nonzero normal coefficient is positive.
 */
export function formatHyperplane(normal: Fraction[], constant: Fraction): string {
  const all = [...normal, constant];
  let lcm = 1n;
  for (const c of all) lcm = bigLcm(lcm, c.den);
  let ints = all.map((c) => c.num * (lcm / c.den));

  let g = 0n;
  for (const v of ints) g = bigGcd(g, v);
  if (g > 1n) ints = ints.map((v) => v / g);

  const firstNz = ints.findIndex((v) => v !== 0n);
  if (firstNz >= 0 && ints[firstNz] < 0n) ints = ints.map((v) => -v);

  const parts: { neg: boolean; text: string }[] = [];
  for (let i = 0; i < normal.length; i++) {
    const v = ints[i];
    if (v === 0n) continue;
    const name = varName(i);
    const mag = v < 0n ? -v : v;
    const text = mag === 1n ? name : `${mag}${name}`;
    parts.push({ neg: v < 0n, text });
  }
  const constInt = ints[normal.length];
  if (constInt !== 0n || parts.length === 0) {
    parts.push({ neg: constInt < 0n, text: (constInt < 0n ? -constInt : constInt).toString() });
  }

  let body = (parts[0].neg ? '-' : '') + parts[0].text;
  for (let i = 1; i < parts.length; i++) {
    body += parts[i].neg ? ` - ${parts[i].text}` : ` + ${parts[i].text}`;
  }
  return `${body} = 0`;
}
