/**
 * 运行样例：两个副本并发修改同一列表项的不同层级属性，然后合并。
 * 运行：node --experimental-strip-types examples/demo.ts  (Node >= 22.6)
 */
import { JsonCRDT } from '../src/document.ts';

const a = JsonCRDT.create('replica-A', {
  list: [{ title: 'hello', tags: ['ts'] }],
});
const b = a.fork('replica-B');

// 并发修改（无通信）
a.set(['list', 0, 'title'], 'hello v2'); // A 改标题
b.push(['list', 0, 'tags'], 'go'); // B 推标签

const itemRef = a.nodeAt(['list', 0]); // 合并前 A 侧列表项引用

a.merge(b); // 三向合并（原地）

console.log('A.title      =', a.get(['list', 0, 'title']));
console.log('A.tags       =', JSON.stringify(a.get(['list', 0, 'tags'])));
console.log('引用保持不变 =', a.nodeAt(['list', 0]) === itemRef);

b.merge(a);
console.log('双向收敛     =', JSON.stringify(a.value()) === JSON.stringify(b.value()));
console.log('最终文档     =', JSON.stringify(a.value(), null, 2));
