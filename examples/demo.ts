import { SkipList } from '../src/skiplist.ts';

// 1. 随机插入 1 万个整数键值对
const list = new SkipList<number, string>();
const keys = Array.from({ length: 10_000 }, (_, i) => i);
for (let i = keys.length - 1; i > 0; i -= 1) {
  const j = Math.floor(Math.random() * (i + 1));
  [keys[i], keys[j]] = [keys[j], keys[i]];
}
for (const key of keys) {
  list.insert(key, `value-${key}`);
}
console.log(`插入完成：size = ${list.size}`);

// 2. 范围扫描 [2500, 2510)
console.log('\n范围扫描 [2500, 2510)：');
for (const [k, v] of list.range({ start: 2500, end: 2510 })) {
  console.log(`  ${k} => ${v}`);
}

// 3. 快照隔离演示：先建迭代器，再持续写入
const snapshot = list.iterator();
const before = list.size;

// 遍历前 5 条
console.log('\n快照迭代器前 5 条：');
for (let i = 0; i < 5; i += 1) {
  const { value, done } = snapshot.next();
  if (done) break;
  console.log(`  ${value[0]} => ${value[1]}`);
}

// 遍历期间持续插入新数据 + 更新旧键
for (let k = 20_000; k < 20_100; k += 1) {
  list.insert(k, `late-${k}`);
}
for (let k = 0; k < 100; k += 1) {
  list.insert(k, `updated-${k}`);
}
console.log(`\n遍历期间写入 200 条（100 新键 + 100 更新）：size ${before} -> ${list.size}`);

// 继续遍历快照：仍然只能看到旧数据
const rest = snapshot.toArray();
console.log(`快照迭代器剩余条数：${rest.length}（期望 ${before - 5}）`);
console.log(`快照中 key=50 的值：${rest.find(([k]) => k === 50)?.[1]}（应为 value-50，而非 updated-50）`);
console.log(`快照中是否包含新键 20000：${rest.some(([k]) => k === 20_000)}（应为 false）`);

// 新建迭代器则能看到全部最新数据
const fresh = list.iterator().toArray();
console.log(`\n新建迭代器条数：${fresh.length}（期望 ${list.size}）`);
console.log(`新迭代器中 key=50 的值：${fresh.find(([k]) => k === 50)?.[1]}（应为 updated-50）`);
