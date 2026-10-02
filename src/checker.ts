import { GestureRecognizer } from './recognizer.ts';
import type { Trace } from './tracegen.ts';

const EPS = 1e-9;

interface PreviewAcc {
  dx: number;
  dy: number;
  scale: number;
  count: number;
}

interface GenStat {
  commits: number;
  previews: Map<string, PreviewAcc>;
  closed: Set<string>;
  compensated: Set<string>;
  previewedGestures: Set<string>;
}

/**
 * Independent checker. It knows nothing about recognizer internals; it only
 * consumes the public output stream and the public phase query, and verifies
 * the spec invariants:
 *
 *  1. One input event never produces more than one commit.
 *  2. Per generation at most one commit (mutual exclusion), and generations
 *     never go backwards.
 *  3. A click commit never coexists with drag/pinch previews in its gen.
 *  4. Every compensate exactly cancels the previews emitted for that gesture
 *     (dx/dy sum to 0, scale product is 1) and requires prior previews.
 *  5. A cancelled gesture that emitted previews must be compensated first.
 *  6. A committed gesture's totals equal the sum of its previews.
 *  7. No gesture emits anything after it was committed or cancelled.
 *  8. When the trace ends, every pointer is gone and the recognizer is idle
 *     (a lost pointer can never keep a gesture active forever).
 */
export function checkTrace(trace: Trace, rec: GestureRecognizer): string[] {
  const violations: string[] = [];
  const bad = (msg: string) => violations.push(`${trace.name}: ${msg}`);
  const gens = new Map<number, GenStat>();
  const statFor = (g: number): GenStat => {
    let s = gens.get(g);
    if (!s) {
      s = { commits: 0, previews: new Map(), closed: new Set(), compensated: new Set(), previewedGestures: new Set() };
      gens.set(g, s);
    }
    return s;
  };
  let lastGen = 0;

  for (const ev of trace.events) {
    const outs = rec.handle(ev);
    const commitCount = outs.filter((o) => o.kind === 'commit').length;
    if (commitCount > 1) bad(`single input produced ${commitCount} commits`);

    for (const o of outs) {
      if (o.kind === 'ignored') continue;
      if (o.gen < 1) bad(`output with invalid gen ${o.gen}`);
      if (o.gen < lastGen) bad(`generation went backwards (${o.gen} < ${lastGen})`);
      lastGen = Math.max(lastGen, o.gen);
      const s = statFor(o.gen);

      switch (o.kind) {
        case 'preview': {
          if (s.closed.has(o.gesture)) bad(`preview for already-closed ${o.gesture}`);
          const acc = s.previews.get(o.gesture) ?? { dx: 0, dy: 0, scale: 1, count: 0 };
          acc.dx += o.dx;
          acc.dy += o.dy;
          acc.scale *= o.scale;
          acc.count += 1;
          s.previews.set(o.gesture, acc);
          s.previewedGestures.add(o.gesture);
          break;
        }
        case 'compensate': {
          const acc = s.previews.get(o.gesture);
          if (!acc || acc.count === 0) {
            bad(`compensate for ${o.gesture} without previews`);
          } else {
            if (Math.abs(acc.dx + o.dx) > EPS || Math.abs(acc.dy + o.dy) > EPS) {
              bad(`compensate does not balance ${o.gesture} previews (dx ${acc.dx}+${o.dx}, dy ${acc.dy}+${o.dy})`);
            }
            if (Math.abs(acc.scale * o.scale - 1) > EPS) {
              bad(`compensate scale does not invert ${o.gesture} previews (${acc.scale}*${o.scale})`);
            }
          }
          if (s.compensated.has(o.gesture)) bad(`double compensate for ${o.gesture}`);
          s.compensated.add(o.gesture);
          break;
        }
        case 'commit': {
          s.commits += 1;
          if (s.commits > 1) bad(`generation ${o.gen} committed ${s.commits} times`);
          if (s.closed.has(o.gesture)) bad(`commit for already-closed ${o.gesture}`);
          if (o.gesture === 'click' && s.previewedGestures.size > 0) {
            bad('click commit in a generation that emitted previews');
          }
          if (o.gesture !== 'click') {
            const acc = s.previews.get(o.gesture);
            if (acc && acc.count > 0) {
              if (Math.abs(acc.dx - o.dx) > EPS || Math.abs(acc.dy - o.dy) > EPS) {
                bad(`${o.gesture} commit totals (${o.dx},${o.dy}) != preview sum (${acc.dx},${acc.dy})`);
              }
              if (Math.abs(acc.scale - o.scale) > EPS) {
                bad(`${o.gesture} commit scale ${o.scale} != preview product ${acc.scale}`);
              }
            }
          }
          s.closed.add(o.gesture);
          break;
        }
        case 'cancel': {
          if (s.closed.has(o.gesture)) bad(`double close of ${o.gesture}`);
          const acc = s.previews.get(o.gesture);
          if (acc && acc.count > 0 && !s.compensated.has(o.gesture)) {
            bad(`cancel of ${o.gesture} with uncompensated previews`);
          }
          s.closed.add(o.gesture);
          break;
        }
      }
    }
  }

  if (!rec.isIdle()) {
    bad(`not idle at end of trace (phase=${rec.getPhase()}, activePointers=${rec.activePointers()})`);
  }
  return violations;
}
