/**
 * Runnable sample: a deterministic walkthrough of the status queue using the
 * manual clock and the fixture-based layout (no browser involved).
 *
 *   node examples/demo.ts
 */
import { ManualClock } from '../src/clock.ts';
import { StatusAnnouncer } from '../src/announcer.ts';
import { layoutAnnouncement, measureText } from '../src/layout.ts';
import { glyphMetrics, nodeTree } from '../fixtures/index.ts';
import type { StatusMessage } from '../src/types.ts';

const clock = new ManualClock();
const transcript: string[] = [];
const log = (line: string) => transcript.push(`[t=${String(clock.now()).padStart(4)}ms] ${line}`);

// Speech duration is derived from the fixture layout: wider/wrapped text
// takes longer to announce. Pure fixture math — no browser layout engine.
const estimateDurationMs = (m: StatusMessage): number => {
  const layout = layoutAnnouncement(nodeTree, m.text, glyphMetrics);
  return 40 * layout.lines.length + Math.ceil(layout.textWidth / 8);
};

const announcer = new StatusAnnouncer({
  clock,
  estimateDurationMs,
  urgentStreakLimit: 2,
  retryDelayMs: 60,
  hooks: {
    speak: (m) => log(`SPEAK   (${m.priority.padEnd(6)}) "${m.text}"`),
    onPause: (m) => log(`PAUSE   "${m.text}"`),
    onResume: (m) => log(`RESUME  "${m.text}"`),
    onComplete: (m) => log(`DONE    "${m.text}"`),
    onAck: (ackId) => log(`ACK     ${ackId}`),
    onDrop: (m, reason) => log(`DROP    "${m.text}" (${reason})`),
  },
});

let seq = 0;
const msg = (partial: Partial<StatusMessage> & Pick<StatusMessage, 'entityId' | 'text'>): StatusMessage => {
  seq += 1;
  return {
    id: `m${seq}`,
    version: 0,
    ackId: `ack-${seq}`,
    priority: 'polite',
    ttlMs: 1_000,
    ...partial,
  };
};

log('--- polite progress announcement starts ---');
announcer.enqueue(msg({ entityId: 'upload', text: 'Uploading photos: 10 percent', mergeKey: 'up' }));
clock.advance(30);

log('--- urgent message interrupts; polite is paused ---');
announcer.enqueue(msg({ entityId: 'network', text: 'Connection lost', priority: 'urgent' }));

log('--- while urgent speaks: same-mergeKey updates coalesce (newest wins) ---');
announcer.enqueue(msg({ entityId: 'upload', text: 'Uploading photos: 20 percent', mergeKey: 'up' }));
announcer.enqueue(msg({ entityId: 'upload', text: 'Uploading photos: 40 percent', mergeKey: 'up' }));

log('--- ...and a short-lived update expires before its turn ---');
announcer.enqueue(msg({ entityId: 'upload', text: 'Uploading photos: 41 percent', ttlMs: 40 }));

clock.advance(300);

log('--- entity retraction invalidates in-flight and queued messages ---');
announcer.enqueue(msg({ entityId: 'sync', text: 'Sync will start soon' }));
announcer.enqueue(msg({ entityId: 'sync', text: 'Sync details follow' }));
announcer.retractEntity('sync');

log('--- version correction invalidates older messages ---');
announcer.enqueue(msg({ entityId: 'upload', text: 'Uploading photos: 60 percent', version: 1 }));
announcer.correctEntityVersion('upload', 2);
announcer.enqueue(msg({ entityId: 'upload', text: 'Uploading photos: 70 percent', version: 2 }));

clock.runUntilIdle();
log('--- queue drained ---');

console.log(transcript.join('\n'));
console.log();
const sample = layoutAnnouncement(nodeTree, 'Uploading photos: 70 percent', glyphMetrics);
console.log(
  `layout check: "${sample.lines.join(' / ')}" fits ${sample.region.id} ` +
    `(width ${sample.region.width}px, text ${measureText('Uploading photos: 70 percent', glyphMetrics)}px)`,
);
