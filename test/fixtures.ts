export function makeSource(count: number, start = 1): string {
  const lines = ['id,name,quantity'];
  for (let i = start; i < start + count; i++) {
    lines.push(`item-${i},Item ${i},${i}`);
  }
  return lines.join('\n');
}
