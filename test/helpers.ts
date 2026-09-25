import type { LayoutResult } from '../src/types.ts';

export function lineText(result: LayoutResult, line: number): string {
  return result.lines[line].map((it) => it.text).join('');
}

export function allText(result: LayoutResult): string {
  return result.lines.map((row) => row.map((it) => it.text).join('')).join('\n');
}

export function lineWidth(result: LayoutResult, line: number): number {
  return result.lines[line].reduce((sum, it) => sum + it.width, 0);
}

export function visibleClusters(result: LayoutResult): Array<{ text: string; srcStart: number; srcEnd: number }> {
  const out: Array<{ text: string; srcStart: number; srcEnd: number }> = [];
  for (const row of result.lines) {
    for (const it of row) {
      if (it.kind === 'cluster') out.push({ text: it.text, srcStart: it.srcStart, srcEnd: it.srcEnd });
    }
  }
  return out;
}
