import type { PointerInput, PointerEventType } from './types.ts';

export interface Trace {
  name: string;
  events: PointerInput[];
}

const P = (type: PointerEventType, pointerId: number, x: number, y: number, t: number, epoch = 0): PointerInput =>
  ({ type, pointerId, x, y, t, epoch });

/**
 * Finite trace generator. Enumerates event sequences around the recognition
 * thresholds (slop-1 / slop / slop+1, timeout-1 / timeout / timeout+1) and
 * interleaves second-pointer joins, capture loss, delayed events and
 * coordinate-space changes. Fully deterministic: no device timing involved.
 */
export function generateTraces(slop = 10, timeout = 300): Trace[] {
  const traces: Trace[] = [];
  const nearDist = [slop - 1, slop, slop + 1];
  const nearTime = [timeout - 1, timeout, timeout + 1];

  // --- single pointer: tap duration around the timeout boundary ---
  for (const d of nearTime) {
    traces.push({ name: `tap/dur=${d}`, events: [P('down', 1, 50, 50, 0), P('up', 1, 50, 50, d)] });
  }

  // --- single pointer: move distance around the slop boundary, then up ---
  for (const m of nearDist) {
    traces.push({
      name: `move-then-up/dist=${m}`,
      events: [P('down', 1, 50, 50, 0), P('move', 1, 50 + m, 50, 100), P('up', 1, 50 + m, 50, 200)],
    });
  }

  // --- single pointer: move then cancel / lostcapture ---
  for (const m of nearDist) {
    traces.push({
      name: `move-then-cancel/dist=${m}`,
      events: [P('down', 1, 50, 50, 0), P('move', 1, 50 + m, 50, 100), P('cancel', 1, 50 + m, 50, 150)],
    });
    traces.push({
      name: `move-then-lostcapture/dist=${m}`,
      events: [P('down', 1, 50, 50, 0), P('move', 1, 50 + m, 50, 100), P('lostcapture', 1, 50 + m, 50, 150)],
    });
  }

  // --- second pointer joins, before/after the drag threshold, with/without pinch movement ---
  for (const pre of [0, slop + 2]) {
    for (const pinchMove of [0, 6, 30]) {
      for (const upOrder of ['up12', 'up21'] as const) {
        const ups = upOrder === 'up12'
          ? [P('up', 1, 50 + pre, 50, 200), P('up', 2, 150 - pinchMove, 50, 250)]
          : [P('up', 2, 150 - pinchMove, 50, 200), P('up', 1, 50 + pre, 50, 250)];
        traces.push({
          name: `join/pre=${pre}/pinchmove=${pinchMove}/${upOrder}`,
          events: [
            P('down', 1, 50, 50, 0),
            ...(pre > 0 ? [P('move', 1, 50 + pre, 50, 50)] : []),
            P('down', 2, 150, 50, 100),
            ...(pinchMove > 0 ? [P('move', 2, 150 - pinchMove, 50, 150)] : []),
            ...ups,
          ],
        });
      }
    }
  }

  // --- capture loss / cancel inside a pinch ---
  traces.push({
    name: 'pinch/lostcapture-first',
    events: [P('down', 1, 50, 50, 0), P('down', 2, 150, 50, 50), P('move', 2, 130, 50, 100), P('lostcapture', 1, 50, 50, 150), P('up', 2, 130, 50, 200)],
  });
  traces.push({
    name: 'pinch/cancel-second',
    events: [P('down', 1, 50, 50, 0), P('down', 2, 150, 50, 50), P('move', 2, 130, 50, 100), P('cancel', 2, 130, 50, 150), P('up', 1, 50, 50, 200)],
  });
  traces.push({
    name: 'pinch/both-lost',
    events: [P('down', 1, 50, 50, 0), P('down', 2, 150, 50, 50), P('move', 2, 130, 50, 100), P('lostcapture', 1, 50, 50, 150), P('lostcapture', 2, 130, 50, 160)],
  });

  // --- coordinate-space change mid-gesture (epoch bump) ---
  traces.push({
    name: 'epoch/drag-rebaseline',
    events: [
      P('down', 1, 50, 50, 0),
      P('move', 1, 60, 50, 50),               // dist == slop: still a click candidate
      P('move', 1, 1000, 1000, 100, 1),       // coordinate jump: re-baseline, no delta
      P('move', 1, 1015, 1000, 150, 1),       // 15px in the new space -> drag
      P('up', 1, 1015, 1000, 200, 1),
    ],
  });
  traces.push({
    name: 'epoch/pinch-rebaseline',
    events: [
      P('down', 1, 50, 50, 0),
      P('down', 2, 150, 50, 50),
      P('move', 2, 140, 50, 100),             // pinch preview in epoch 0
      P('move', 1, 300, 300, 150, 1),         // pointer 1 switches space
      P('move', 2, 400, 300, 200, 1),         // pointer 2 follows -> pair re-baselines
      P('move', 1, 310, 300, 250, 1),         // valid pinch delta again
      P('up', 1, 310, 300, 300, 1),
      P('up', 2, 400, 300, 350, 1),
    ],
  });

  // --- delayed / stray / duplicate events ---
  traces.push({ name: 'stray/move-after-up', events: [P('down', 1, 50, 50, 0), P('up', 1, 50, 50, 100), P('move', 1, 80, 50, 150)] });
  traces.push({ name: 'stray/up-after-cancel', events: [P('down', 1, 50, 50, 0), P('cancel', 1, 50, 50, 100), P('up', 1, 50, 50, 150)] });
  traces.push({ name: 'stray/out-of-order-move', events: [P('down', 1, 50, 50, 0), P('move', 1, 60, 50, 100), P('move', 1, 55, 50, 90), P('up', 1, 60, 50, 200)] });
  traces.push({ name: 'stray/duplicate-down', events: [P('down', 1, 50, 50, 0), P('down', 1, 50, 50, 50), P('up', 1, 50, 50, 100)] });
  traces.push({
    name: 'stray/down-while-settled',
    events: [P('down', 1, 50, 50, 0), P('down', 2, 150, 50, 50), P('move', 2, 130, 50, 100), P('up', 1, 50, 50, 150), P('down', 3, 10, 10, 160), P('up', 2, 130, 50, 200), P('up', 3, 10, 10, 250)],
  });

  // --- long press: timeout gates click, but a later slop breach still drags ---
  traces.push({
    name: 'timeout/long-press-then-drag',
    events: [P('down', 1, 50, 50, 0), P('move', 1, 50 + slop + 5, 50, timeout + 10), P('up', 1, 50 + slop + 5, 50, timeout + 50)],
  });

  // --- extra single-pointer shapes: diagonal, vertical, sub-slop wobble ---
  traces.push({
    name: 'move/diagonal-just-over',
    events: [P('down', 1, 50, 50, 0), P('move', 1, 50 + slop - 2, 50 + slop - 2, 100), P('up', 1, 50 + slop - 2, 50 + slop - 2, 200)],
  });
  traces.push({
    name: 'move/vertical-drag',
    events: [P('down', 1, 50, 50, 0), P('move', 1, 50, 50 + slop + 8, 100), P('move', 1, 50, 50 + slop + 20, 150), P('up', 1, 50, 50 + slop + 20, 200)],
  });
  traces.push({
    name: 'move/wobble-within-slop',
    events: [P('down', 1, 50, 50, 0), P('move', 1, 50 + slop - 3, 50, 60), P('move', 1, 50 - 2, 50, 120), P('move', 1, 50 + 3, 50, 180), P('up', 1, 50 + 3, 50, 240)],
  });

  // --- epoch change while still a click candidate, then release in new space ---
  traces.push({
    name: 'epoch/possible-then-click',
    events: [P('down', 1, 50, 50, 0), P('move', 1, 500, 500, 60, 1), P('move', 1, 503, 500, 120, 1), P('up', 1, 503, 500, 180, 1)],
  });

  // --- both pinch fingers move, interleaved, then release in reverse order ---
  traces.push({
    name: 'pinch/both-move-interleaved',
    events: [
      P('down', 1, 50, 50, 0),
      P('down', 2, 150, 50, 40),
      P('move', 1, 40, 50, 80),
      P('move', 2, 160, 50, 120),
      P('move', 1, 30, 50, 160),
      P('up', 2, 160, 50, 200),
      P('up', 1, 30, 50, 240),
    ],
  });

  // --- second pointer joins after a long press (timeout already exceeded) ---
  traces.push({
    name: 'join/after-timeout',
    events: [P('down', 1, 50, 50, 0), P('down', 2, 150, 50, timeout + 20), P('move', 2, 140, 50, timeout + 60), P('up', 1, 50, 50, timeout + 100), P('up', 2, 140, 50, timeout + 140)],
  });

  // --- lostcapture for a pointer that was never tracked ---
  traces.push({
    name: 'stray/lostcapture-unknown',
    events: [P('down', 1, 50, 50, 0), P('lostcapture', 7, 0, 0, 60), P('up', 1, 50, 50, 120)],
  });

  // --- third pointer during pinch is refused; session still ends cleanly ---
  traces.push({
    name: 'pinch/third-pointer-refused',
    events: [P('down', 1, 50, 50, 0), P('down', 2, 150, 50, 40), P('down', 3, 90, 90, 80), P('move', 2, 140, 50, 120), P('up', 1, 50, 50, 160), P('up', 2, 140, 50, 200), P('up', 3, 90, 90, 240)],
  });

  return traces;
}
