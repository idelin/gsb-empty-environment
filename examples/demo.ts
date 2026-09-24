import { TemplateKernel, CycleError } from '../src/kernel.ts';

const line = (label: string, value: unknown) => console.log(`${label.padEnd(14)}`, value);

// --- 1. 基本镜像 + 纯函数变换 ------------------------------------------------
const tpl = 'Hello ?. Shout: ?. Bye ?.';
const at = (n: number) => tpl.indexOf('?', n);
const p1 = at(0), p2 = at(p1 + 1), p3 = at(p2 + 1);

const k = new TemplateKernel(tpl);
k.addPlaceholder('name', p1, p1 + 1);
k.addMirror('shout', p2, p2 + 1, 'name', (s) => s.toUpperCase(), 'shout');
k.addMirror('again', p3, p3 + 1, 'name');

line('initial:', k.text);
k.edit('name', 'xy'); // 主占位改为 xy：两个镜像同步
line('edit -> xy:', k.text);
line('shout range:', k.placeholderRange('shout'));
line('again text:', k.placeholderText('again'));

k.undo();
line('undo:', k.text);
k.edit('name', 'Ada');
line('finalize:', k.finalize()); // 退出占位模式，保留纯文本

// --- 2. 嵌套占位：父编辑覆盖子占位，撤销恢复原身份 ---------------------------
const n = new TemplateKernel('greeting: [hello world]');
n.addPlaceholder('outer', 10, 23);
n.addPlaceholder('inner', 11, 22, 'outer');
n.edit('outer', '...');
line('covered:', `${n.text} (inner valid = ${n.isValid('inner')})`);
n.undo();
line('restored:', `${n.text} (inner = "${n.placeholderText('inner')}")`);

// --- 3. 循环引用：编译期拒绝并报出环链 ---------------------------------------
const c = new TemplateKernel('. .');
c.addMirror('A', 0, 1, 'B');
c.addMirror('B', 2, 3, 'A');
try {
  c.compile();
} catch (err) {
  line('cycle:', (err as CycleError).message);
}

// --- 4. 变换抛错：整体回滚 ----------------------------------------------------
const r = new TemplateKernel('ok / ok');
r.addPlaceholder('P', 0, 2);
r.addMirror('M', 5, 7, 'P', (s) => {
  if (s === 'bad') throw new Error('transform exploded');
  return s;
});
try {
  r.edit('P', 'bad');
} catch (err) {
  line('rollback:', `"${r.text}" after ${(err as Error).message}`);
}
