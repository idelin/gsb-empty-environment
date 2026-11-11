# 嵌套文档沙箱继承

浏览器测试宿主需要判断嵌套文档是否仍处于预期隔离域。写可导入的TypeScript导航状态包，父文档限制与本层声明取并，限制分为脚本、弹窗和保留来源三项；修改声明仅在下一次导航生效，禁止保留来源时分配不可与其他文档相等的不透明来源。派生弹窗继承创建时限制，解除挂载使旧导航回调失效。用事件模型测试嵌套更新、空文档继承、延迟导航和文档号复用。

## 用法

```ts
import { NavigationHost } from './index.ts';

const host = new NavigationHost();
const root = host.createRoot('https://a.example/');
root.declareRestrictions({ scripts: true }); // 仅在下一次导航生效
root.navigate();

const frame = root.createChild();            // 空文档继承父层生效限制与来源
const popup = root.openPopup();              // 弹窗快照创建时限制
frame.navigate(undefined, () => {});          // 回调经 host.drain() 投递
frame.unmount();                              // 旧导航回调随之失效
host.drain();
console.log(host.events);                     // 事件流：created/declared/navigated/...
```

运行测试：`npm test`（需要 Node >= 23.9）。
