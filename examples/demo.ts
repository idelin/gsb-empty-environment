import { setTimeout as sleep } from 'node:timers/promises';
import { TieredTtlLruCache } from '../src/tiered-ttl-lru.ts';

const cache = new TieredTtlLruCache<string, { id: number; payload: string }>({
  maxSize: 1000,
});

console.log('== insert 100 objects, TTL staggered 10ms..100ms ==');
for (let i = 1; i <= 100; i++) {
  cache.set(
    `obj-${i}`,
    { id: i, payload: `payload-${i}` },
    { ttlMs: 10 + (i - 1) * (90 / 99) },
  );
}
console.log('size after insert:', cache.size);
console.log('next expiry in (ms):', cache.nextExpiryIn().toFixed(1));

console.log('\n== hot loop: rescue the 5 soonest-to-die objects via sliding TTD ==');
for (let round = 0; round < 4; round++) {
  for (let i = 1; i <= 5; i++) {
    const key = `obj-${i}`;
    if (cache.get(key)) {
      cache.set(key, { id: i, payload: `hot-${i}` }, { slidingMs: 150 });
    }
  }
  await sleep(15);
}

await sleep(50); // t ~= 110ms: every absolute TTL has now passed
console.log('read expired obj-50 ->', cache.get('obj-50')); // undefined
console.log('read rescued obj-1  ->', cache.get('obj-1')); // still alive

console.log('\n== one explicit sweep batch-reaps everything left ==');
await sleep(160);
const reaped = cache.sweep();
console.log('batch-reaped dead objects:', reaped);
console.log('size after sweep:', cache.size);
console.log('stats:', cache.stats);
console.log('integrity ok:', cache.verifyIntegrity());

cache.dispose();
console.log('after dispose -> size:', cache.size, '| keys:', cache.keys());
