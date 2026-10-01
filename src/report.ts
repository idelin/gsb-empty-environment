import type { BreakResult } from './breaker.ts';
import type { GreedyResult } from './greedy.ts';

export function formatResult(title: string, r: BreakResult): string {
  const out: string[] = [];
  out.push(`=== ${title} ===`);
  out.push(`status: ${r.status}`);
  if (r.status === 'budget-exhausted') {
    out.push(`  ${r.warnings[0]}`);
    return out.join('\n');
  }
  out.push(
    `optimal cost: ${r.optimalCost} | breaks: [${r.breaks.join(', ')}] | ` +
      `states: ${r.stats.states} | relaxations: ${r.stats.relaxations}`,
  );
  out.push('lines:');
  for (const l of r.lines) {
    out.push(
      `  [${l.index}] "${l.text}" width=${l.naturalWidth} remaining=${l.remaining}` +
        `${l.remaining < 0 ? ' (NEGATIVE' + (l.atomic ? ', unbreakable overflow' : ', shrunk') + ')' : ''}` +
        ` badness=${l.badness} hyphen=${l.hyphenCost} widthDiff=${l.widthDiffCost}` +
        ` overflow=${l.overflowCost} step=${l.total} cum=${l.cumulativeCost}`,
    );
  }
  if (r.warnings.length > 0) {
    out.push('warnings:');
    for (const w of r.warnings) out.push(`  ! ${w}`);
  }
  out.push('decision evidence (candidates from the state the optimal path passed through):');
  for (const e of r.evidence) {
    const s = e.stateBefore;
    out.push(
      `  line ${e.lineIndex} from break ${e.fromBreak} ` +
        `(state: cost=${s.cost} hyphens=${s.hyphens} prevHyphenated=${s.prevHyphenated} prevWidth=${s.prevWidth}):`,
    );
    for (const c of e.candidates) {
      const tag = c.chosen ? 'CHOSEN' : c.rejectedBy ? `rejected: ${c.rejectedBy}` : 'not chosen';
      out.push(
        `    -> break ${c.endBreak} "${c.text}" step=${c.incrementalCost} cum=${c.cumulativeIfChosen} [${tag}]`,
      );
    }
  }
  out.push('certificate:');
  for (const c of r.certificate) out.push(`  ${c}`);
  return out.join('\n');
}

export function formatGreedy(title: string, g: GreedyResult): string {
  const out: string[] = [];
  out.push(`=== ${title} ===`);
  out.push(`heuristic: ${g.heuristic}`);
  out.push(`total cost: ${g.totalCost} | breaks: [${g.breaks.join(', ')}]`);
  for (const l of g.lines) {
    out.push(`  [${l.index}] "${l.text}" width=${l.naturalWidth} remaining=${l.remaining} step=${l.total}`);
  }
  return out.join('\n');
}
