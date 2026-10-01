/**
 * Replayable end-to-end sample: drives the main scene through a scripted
 * keyboard-event journey and prints a transcript of every step, the focus
 * trajectory and the screen-reader announcement log.
 *
 * Run with: npm run sample
 */
import { FocusController } from '../src/controller.ts';
import { keyEvent, replay, type ReplayStep } from '../src/replay.ts';
import { buildMainScene } from '../fixtures/scenes.ts';

const script: ReplayStep[] = [
  { type: 'settle' }, // initial recovery -> first focusable node
  { type: 'key', event: keyEvent('ArrowDown') },
  { type: 'key', event: keyEvent('ArrowDown') },
  { type: 'key', event: keyEvent('ArrowRight') },
  { type: 'key', event: keyEvent('ArrowDown') },
  { type: 'press', event: keyEvent('ArrowDown') }, // plans card-4: slow scroll
  { type: 'press', event: keyEvent('ArrowRight') }, // rapid second key: expires the first
  { type: 'settle' },
  { type: 'key', event: keyEvent('ArrowRight') }, // back into the card list
  { type: 'press', event: keyEvent('ArrowDown') },
  { type: 'settle' },
  { type: 'press', event: keyEvent('ArrowDown') },
  { type: 'settle' },
  { type: 'delete', id: 'card-4' }, // delete a node, then navigate past it
  { type: 'press', event: keyEvent('ArrowDown') }, // re-plans to the lazy card-5
  { type: 'settle' },
  { type: 'delete', id: 'card-5' }, // delete the focused node itself
  { type: 'settle' }, // deterministic recovery
  { type: 'collapse', id: 'dialog', collapsed: false }, // open the modal
  { type: 'key', event: keyEvent('ArrowRight') },
  { type: 'key', event: keyEvent('ArrowDown') },
  { type: 'key', event: keyEvent('ArrowLeft') }, // trapped inside the modal
  { type: 'key', event: keyEvent('ArrowRight') },
];

function describe(step: ReplayStep): string {
  switch (step.type) {
    case 'key':
    case 'press':
      return `${step.type === 'press' ? 'press' : 'key  '} ${step.event.key}`;
    case 'collapse':
      return `fold  ${step.id} ${step.collapsed === false ? '(open)' : '(close)'}`;
    case 'delete':
      return `del   ${step.id}`;
    default:
      return step.type;
  }
}

const tree = buildMainScene();
const controller = new FocusController(tree);
const result = await replay(tree, controller, script);

console.log('=== replay transcript ===');
for (const [index, record] of result.records.entries()) {
  const focus = record.focus ?? '(none)';
  const status = record.result ? ` [${record.result.status}]` : '';
  console.log(
    `${String(index).padStart(2)}  ${describe(record.step).padEnd(24)} -> ${focus}${status}`,
  );
}

console.log('\n=== screen-reader announcements ===');
for (const entry of controller.announcer.log) {
  console.log(`#${String(entry.seq).padStart(2)}  ${entry.id.padEnd(10)} (${entry.reason})`);
}

const lastAnnouncement = controller.announcer.log.at(-1);
const inSync = lastAnnouncement?.id === result.finalFocus;
const focusValid = result.finalFocus !== null && tree.isFocusableNow(result.finalFocus);
console.log('\n=== summary ===');
console.log(`final focus:            ${result.finalFocus}`);
console.log(`focus valid:            ${focusValid}`);
console.log(`announcer in sync:      ${inSync}`);
console.log(`pending async work:     ${tree.hasPending() || controller.busy()}`);
if (!focusValid || !inSync) {
  process.exitCode = 1;
}
