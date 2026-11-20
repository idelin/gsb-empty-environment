export interface Dot { actor: string; sequence: bigint }
const max = (1n << 64n) - 1n;
export function parseDot(value: string): Dot {
  const m = /^(0|[1-9][0-9]*)@([A-Za-z0-9_-]{1,32})$/.exec(value);
  if (!m || m[0] !== value) throw new Error("invalid dot");
  const sequence = BigInt(m[1]);
  if (sequence > max) throw new Error("sequence overflow");
  return { actor: m[2], sequence };
}
export function formatDot(dot: Dot): string {
  const value = dot.sequence.toString(10) + "@" + dot.actor;
  parseDot(value);
  return value;
}
// This stable storage order is not a causal order.
export function compareDots(left: Dot, right: Dot): number {
  if (left.actor !== right.actor) return left.actor < right.actor ? -1 : 1;
  return left.sequence < right.sequence ? -1 : left.sequence > right.sequence ? 1 : 0;
}
