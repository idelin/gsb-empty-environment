import { CepEngine, type Notification } from '../src/engine.ts';

const engine = new CepEngine();
engine.registerPattern({
  name: 'A->B-without-C',
  sequence: ['A', 'B'],
  negation: ['C'],
  windowMs: 10_000,
});

engine.onNotification((n: Notification) => {
  if (n.kind === 'match') {
    const [a, b] = n.events;
    console.log(
      `>>> HIT    ${n.matchId} [${n.pattern}] ${a.type}@${a.timestamp} -> ${b.type}@${b.timestamp}` +
        (n.compensated ? '  (compensated, out-of-order)' : ''),
    );
  } else {
    console.log(`>>> REVOKE ${n.matchId} [${n.pattern}] reason: ${n.reason}`);
  }
});

console.log('--- t=0    A arrives');
engine.process({ id: 'a1', type: 'A', timestamp: 0 });
console.log('--- t=5000 B arrives (within 10s window, no C in between)');
engine.process({ id: 'b1', type: 'B', timestamp: 5_000 });
console.log('--- late out-of-order event: C@2000 arrives now');
engine.process({ id: 'c1', type: 'C', timestamp: 2_000 });

console.log('\n--- engine log ---');
for (const entry of engine.log) {
  console.log(
    `[wm=${String(entry.watermark).padStart(5)}] ${entry.action.padEnd(22)} ${entry.detail}`,
  );
}
