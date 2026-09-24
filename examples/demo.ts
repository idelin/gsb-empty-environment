import { LineDiffSession, type ViewItem } from '../src/index.ts';

const left = [
  '# Release Notes',
  '',
  '',
  '',
  '## Highlights',
  '- Fast renderer 🚀',
  '- Stable API',
  '',
  '## Fixes',
  '- Fix crash on exit',
  '- Improve logging',
  '',
].join('\r\n'); // left side uses CRLF

const right = [
  '# Release Notes',
  '',
  '',
  '## Highlights',
  '- Fast renderer 🚀',
  '- Stable API v2',
  '- New diff engine',
  '',
  '## Fixes',
  '- Improve logging',
  '',
].join('\n'); // right side uses LF

const s = new LineDiffSession(left, right);

console.log('=== diff blocks (left CRLF vs right LF) ===');
for (const b of s.getBlocks()) {
  console.log(
    `${b.kind.padEnd(7)} left[${b.left.start},${b.left.end})  right[${b.right.start},${b.right.end})`,
  );
}

console.log('\n=== folded view (context = 1) ===');
for (const item of s.getView(1)) {
  if (item.type === 'fold') {
    console.log(`  ⋮ ${item.lineCount} equal lines folded (left ${item.left.start}..${item.left.end - 1})`);
  } else if (item.type === 'equal') {
    for (let i = item.left.start; i < item.left.end; i++) {
      console.log(`  L${i} = R${s.mapLine('left', i)}  ${JSON.stringify(s.getLine('left', i).text)}`);
    }
  } else {
    const b = item.block;
    for (let i = b.left.start; i < b.left.end; i++) {
      console.log(`- L${i} -> ${s.mapLine('left', i)}  ${JSON.stringify(s.getLine('left', i).text)}`);
    }
    for (let i = b.right.start; i < b.right.end; i++) {
      console.log(`+ R${i} -> ${s.mapLine('right', i)}  ${JSON.stringify(s.getLine('right', i).text)}`);
    }
  }
}

console.log('\n=== bidirectional line mapping ===');
for (const [side, line] of [['left', 5], ['left', 6], ['right', 6], ['right', 8]] as const) {
  console.log(`mapLine(${side}, ${line}) = ${s.mapLine(side, line)}`);
}

console.log('\n=== UTF-16 offsets (surrogate safe) ===');
const emojiLine = 5; // '- Fast renderer 🚀'
const rocketCol = s.getLine('left', emojiLine).text.indexOf('🚀');
console.log(`'🚀' starts at col ${rocketCol} (UTF-16 units), length ${'🚀'.length}`);
console.log(`lineColToOffset(left, ${emojiLine}, ${rocketCol + 1}) snaps to pair start:`,
  s.lineColToOffset('left', emojiLine, rocketCol + 1));
console.log('offsetToLineCol of that offset:', s.offsetToLineCol('left', s.lineColToOffset('left', emojiLine, rocketCol)));

console.log('\n=== local edit on the right side, then revert ===');
const at = s.lineColToOffset('right', 0, 0);
s.applyEdit('right', at, at, 'Draft v3\r\n');
console.log('after insert, blocks:');
for (const b of s.getBlocks()) {
  console.log(`  ${b.kind} left[${b.left.start},${b.left.end}) right[${b.right.start},${b.right.end})`);
}
console.log('mapLine(left, 0) =', s.mapLine('left', 0));

s.applyEdit('right', at, at + 'Draft v3\r\n'.length, '');
console.log('after revert, blocks:', s.getBlocks());
const view: ViewItem[] = s.getView(1);
console.log('view items:', view.map((v) => v.type).join(', '));
console.log('mapLine(left, 10) =', s.mapLine('left', 10));
