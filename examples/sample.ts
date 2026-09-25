import { ScrollEngine } from '../src/engine.ts';
import type { ScrollTransaction } from '../src/engine.ts';
import { loadScene } from '../test/helpers/reference.ts';

function fmt(n: number): string {
  return (Object.is(n, -0) ? 0 : n).toFixed(2).padStart(8);
}

function printTransaction(tx: ScrollTransaction): void {
  console.log(
    `\ntx#${tx.id} [${tx.kind}] source=${tx.source}` +
      (tx.originId !== null ? ` origin=tx#${tx.originId}` : ''),
  );
  console.log('  layer     axis   received  consumed    passed  residual  transform');
  for (const layer of tx.layers) {
    for (const [name, a] of [
      ['x', layer.x],
      ['y', layer.y],
    ] as const) {
      if (a.received === 0 && a.consumed === 0 && a.passed === 0 && a.residualBefore === 0) continue;
      console.log(
        `  ${layer.nodeId.padEnd(9)} ${name} ${fmt(a.received)} ${fmt(a.consumed)} ${fmt(a.passed)} ${fmt(a.residualAfter)}   ${layer.transformApplied ?? '-'}`,
      );
    }
  }
  console.log(`  discarded: x=${fmt(tx.discarded.x)} y=${fmt(tx.discarded.y)}`);
}

const engine = new ScrollEngine(loadScene());

console.log('== 1. wheel over nested scroller: inner bottoms out, remainder flows outward ==');
printTransaction(engine.dispatchWheel({ x: 50, y: 50 }, { x: 0, y: 500 }));

console.log('\n== 2. wheel over the rotated box: vertical delta becomes horizontal scroll ==');
printTransaction(engine.dispatchWheel({ x: 310, y: 50 }, { x: 0, y: 100 }));

console.log('\n== 3. wheel over the horizontal strip: y is untouched, x is consumed ==');
printTransaction(engine.dispatchWheel({ x: 50, y: 250 }, { x: 120, y: 40 }));

console.log('\n== 4. programmatic scroll with a source token; the mirror echo is blocked ==');
engine.onScroll((event) => {
  console.log(`  listener sees scroll on ${event.nodeId} (source=${event.source}, dy=${event.delta.y})`);
  engine.scrollTo('inner', { x: 0, y: event.offset.y + 10 }, { source: event.source });
});
printTransaction(engine.scrollTo('inner', { x: 0, y: 200 }, { source: 'panel' })!);
console.log(`  blocked feedback attempts: ${engine.blockedFeedback}`);

console.log('\n== 5. momentum tail on the inner scroller (linked transactions) ==');
for (const tx of engine.startInertia({ x: 50, y: 50 }, { x: 0, y: 16 }, { decay: 0.5 })) {
  printTransaction(tx);
}

console.log('\n== final offsets ==');
for (const [id, node] of engine.nodes) {
  console.log(
    `  ${id.padEnd(8)} offset=(${node.offset.x}, ${node.offset.y}) residual=(${node.residual.x.toFixed(4)}, ${node.residual.y.toFixed(4)})`,
  );
}

console.log('\n== replay check ==');
const replayed = ScrollEngine.replay(loadScene(), engine.log);
const identical = JSON.stringify(replayed.log) === JSON.stringify(engine.log);
console.log(`  replayed ${engine.log.length} log entries -> identical ledgers: ${identical}`);
