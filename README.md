# 跨站导航清空窗口名

window.name里的令牌在跨站导航后还在，新文档能读走。注册域或方案变化的导航必须清空window.name；同站同方案导航保留。同站路径变化后名字还在。可导入的TypeScript状态机接文档管线送来的状态。从https站点导航到另一注册域再读名字。

## 状态机

`windowNameGuard.ts` 导出可导入的 `WindowNameGuard` 类与纯函数版 `transition`，由文档管线在文档提交（`document-committed`）和名字写入（`name-set`）时送入事件：

```ts
import { WindowNameGuard } from './index.ts';

const guard = new WindowNameGuard();
guard.dispatch({
  type: 'document-committed',
  document: { scheme: 'https', registrableDomain: 'bank.example', path: '/' },
});
guard.dispatch({ type: 'name-set', value: 'secret-token' });

// 同站同方案、仅路径变化：名字保留
guard.dispatch({
  type: 'document-committed',
  document: { scheme: 'https', registrableDomain: 'bank.example', path: '/dashboard' },
});
guard.windowName; // 'secret-token'

// 导航到另一注册域（或方案变化）：名字清空
guard.dispatch({
  type: 'document-committed',
  document: { scheme: 'https', registrableDomain: 'attacker.example', path: '/' },
});
guard.windowName; // ''
```

## 规则

- 注册域或方案任一变化：新文档提交后 `window.name` 清空为空字符串。
- 同站同方案（含路径、端口等变化）：保留原名字。
- 站点身份比较对 scheme 与注册域大小写不敏感。
- 首个文档提交时同样以空名字起步。

## 测试

`npm test`（node 内置 test runner，共 10 个用例）。
