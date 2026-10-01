import { signal, h, t, attr, mount, applyPatches, renderToString } from '../src/index.ts';

// ---- 响应式状态 ----
const user = { name: signal('Alice') };
const theme = signal('light');

// ---- 模板：动态部分以绑定声明，静态部分原样保留 ----
const view = mount(
  h('div', { class: 'app', 'data-theme': attr(() => theme()) },
    h('header', null, h('h1', null, '用户面板')),
    h('main', null,
      h('p', { class: 'greeting' }, t(() => '欢迎，' + user.name())),
      h('p', { class: 'tip' }, '这是一段静态文本'),
    ),
  ),
);

console.log('== 初始渲染 ==');
console.log(renderToString(view.root));

// ---- Signal 变更：引擎直接产出最小补丁，无全树 Diff ----
view.patches.length = 0;
console.log('\n== user.name.set("Bob") ==');
user.name.set('Bob');
console.log('产出补丁:', JSON.stringify(view.patches, null, 2));
applyPatches(view.index, view.patches);
console.log('对齐后:', renderToString(view.root));

view.patches.length = 0;
console.log('\n== theme.set("dark") ==');
theme.set('dark');
console.log('产出补丁:', JSON.stringify(view.patches, null, 2));
applyPatches(view.index, view.patches);
console.log('对齐后:', renderToString(view.root));
