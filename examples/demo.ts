/**
 * 运行样例：两个副本并发修改同一列表项的不同层级属性，然后三向合并。
 * 运行：node examples/demo.ts   （需要 Node >= 23.6，原生支持 TypeScript）
 */
import { Doc } from '../src/index.ts';

// 1. 共同祖先：基础文档
const seed = new Doc('seed');
seed.root.list = [{ title: 'draft', tags: ['js'] }];

// 2. 派生两个副本（等价于网络同步后的 alice / bob）
const alice = seed.fork('alice');
const bob = seed.fork('bob');

console.log('== 合并前（共同祖先） ==');
console.log('alice:', JSON.stringify(alice.toJSON()));
console.log('bob  :', JSON.stringify(bob.toJSON()));

// 3. 各自持有列表项的内存引用
const aliceItem = alice.root.list[0];
const bobItem = bob.root.list[0];

// 4. 并发变更（离线进行，互不可见）
alice.root.list[0].title = 'published'; // alice 改 Register
bob.root.list[0].tags.push('go'); //       bob 改嵌套 List

console.log('\n== 并发变更后、合并前 ==');
console.log('alice:', JSON.stringify(alice.toJSON()));
console.log('bob  :', JSON.stringify(bob.toJSON()));

// 5. 三向合并：共同祖先 + 两个并发分支
Doc.merge(alice, bob);

console.log('\n== 合并后 ==');
console.log('alice:', JSON.stringify(alice.toJSON()));
console.log('bob  :', JSON.stringify(bob.toJSON()));

// 6. 验收点
console.log('\n== 验收 ==');
console.log('title 双方一致      :', alice.root.list[0].title === 'published' && bob.root.list[0].title === 'published');
console.log('tags 双方一致       :', JSON.stringify(alice.root.list[0].tags.toArray()) === '["js","go"]' && JSON.stringify(bob.root.list[0].tags.toArray()) === '["js","go"]');
console.log('alice 引用未被覆盖  :', alice.root.list[0] === aliceItem);
console.log('bob   引用未被覆盖  :', bob.root.list[0] === bobItem);
console.log('双方状态完全收敛    :', JSON.stringify(alice.toJSON()) === JSON.stringify(bob.toJSON()));
