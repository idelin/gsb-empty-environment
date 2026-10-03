/**
 * Replayable operation logs: a scenario is a plain-data op list that can be
 * replayed on any engine to produce a deterministic text transcript.
 */
import { WatershedEngine, type HeightEdit } from './engine.ts';

export type Op =
  | { op: 'comment'; text: string }
  | { op: 'addSeed'; id: string; x: number; y: number }
  | { op: 'moveSeed'; id: string; x: number; y: number }
  | { op: 'removeSeed'; id: string }
  | { op: 'setHeights'; edits: HeightEdit[] }
  | { op: 'pauseBudget'; budget: number }   // run next mutation with a budget
  | { op: 'resume' };                        // finish a paused job

export interface ReplayOptions {
  budgetFor?: (opIndex: number) => number | undefined;
}

export function replay(engine: WatershedEngine, ops: Op[], opts: ReplayOptions = {}): string[] {
  const lines: string[] = [];
  let pendingBudget: number | undefined;
  ops.forEach((o, i) => {
    const budget = opts.budgetFor?.(i) ?? pendingBudget;
    pendingBudget = undefined;
    switch (o.op) {
      case 'comment':
        lines.push(`# ${o.text}`);
        return;
      case 'pauseBudget':
        pendingBudget = o.budget;
        return;
      case 'resume':
        engine.step();
        lines.push(`resume -> v${engine.version}`);
        return;
      case 'addSeed':
        engine.addSeed(o.id, o.x, o.y, { budget });
        break;
      case 'moveSeed':
        engine.moveSeed(o.id, o.x, o.y, { budget });
        break;
      case 'removeSeed':
        engine.removeSeed(o.id, { budget });
        break;
      case 'setHeights':
        engine.setHeights(o.edits, { budget });
        break;
    }
    if (engine.paused) {
      lines.push(`${describe(o)} -> paused (still v${engine.version})`);
      return;
    }
    const v = engine.version;
    const patch = engine.getPatch(v);
    const events = engine.getEvents(v);
    const stats = engine.getStats(v);
    const ev = events.map(e => e.type === 'merge'
      ? `merge(${e.from}->[${e.into.join(',')}])`
      : `split(${e.from}->${e.into})`).join(' ');
    lines.push(`${describe(o)} -> v${v} patch=${patch.length}px pops=${stats.pops} tiles=${stats.regionTiles} exp=${stats.expansions}${ev ? ' ' + ev : ''}`);
  });
  const adj = engine.getAdjacency()
    .map(e => `${e.a}|${e.b}:${e.count}`)
    .join(' ');
  lines.push(`adjacency ${adj}`);
  lines.push(`labels ${[...engine.getLabels()].join(',')}`);
  return lines;
}

function describe(o: Op): string {
  switch (o.op) {
    case 'addSeed': return `addSeed(${o.id}@${o.x},${o.y})`;
    case 'moveSeed': return `moveSeed(${o.id}->${o.x},${o.y})`;
    case 'removeSeed': return `removeSeed(${o.id})`;
    case 'setHeights': return `setHeights(${o.edits.length}px)`;
    case 'comment': return `comment`;
    case 'pauseBudget': return `pauseBudget`;
    case 'resume': return `resume`;
  }
}
