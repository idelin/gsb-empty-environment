/** 运行样例：node examples/demo.ts （需 Node ≥ 22.6） */
import {
  apply,
  compose,
  invert,
  makePatch,
  relocate,
  transform,
  validateCommutation,
  ConflictError,
  DigestMismatchError,
} from '../src/patch.ts';

let n = 0;
const id = () => `demo-${++n}`;
const show = (label: string, v: unknown) => console.log(`${label}:`, JSON.stringify(v));

// 1) 构造并应用补丁（CRLF + emoji，UTF-16 偏移）
const base = 'hello\r\nbrave 😀 world\r\n';
const edited = 'hello\r\nBRAVE 😀 new world!\r\n';
const P = makePatch(base, edited, id);
show('base', base);
show('patch ops', P.ops);
show('applied', apply(base, P));

// 2) 求逆：P 再逆 P 恢复逐字节原文
const restored = apply(apply(base, P), invert(P, id));
show('inverted restores base (byte-exact)', Buffer.from(restored, 'utf8').equals(Buffer.from(base, 'utf8')));

// 3) 顺序组合：compose(P,Q) == 先 P 后 Q，且互逆操作消除
const Q = makePatch(edited, edited.replace('new world!', 'NEW WORLD'), id);
show('compose(P,Q) == apply(apply(P),Q)', apply(base, compose(P, Q)) === apply(apply(base, P), Q));
const identity = compose(P, invert(P, id));
show('compose(P, invert(P)) ops (全为 retain)', identity.ops);

// 4) 三方重定位 + 交换图
const alice = makePatch(base, base.replace('hello', 'HELLO'), id);
const bob = makePatch(base, base.replace('world', 'WORLD'), id);
validateCommutation(alice, bob, base);
const aliceAfterBob = relocate(alice, bob, base);
show('commutes', apply(apply(base, bob), aliceAfterBob) === apply(apply(base, alice), transform(bob, alice, base)));

// 5) 交叉删除 → 冲突
const delA = makePatch(base, base.slice(5), id); // 删 'hello'
const delB = makePatch(base, base.slice(0, 3) + base.slice(8), id); // 删 'lo\r\nb'
try {
  transform(delA, delB, base);
} catch (e) {
  show('overlapping deletes', e instanceof ConflictError ? 'ConflictError' : e);
}

// 6) 上下文校验：长度相同但内容不同的基线被拒绝，且无部分结果
const sameLength = 'hxllo\r\nbrave 😀 world\r\n';
try {
  apply(sameLength, P);
} catch (e) {
  show('same-length different base', e instanceof DigestMismatchError ? 'DigestMismatchError' : e);
}
