# 浏览器消息端口能力转移

网页沙箱把消息端口转给另一个隔离域后，原持有者不能继续发送。用TypeScript写供沙箱宿主调用的端口能力组件，发送消息可附带若干端口，转移与消息入队原子完成；失败保持原归属，成功使旧句柄失效，重复转移同一端口拒绝。允许端口形成环，关闭域时回收不可达端口。用可暂停队列测试转移与关闭交错、迟到回复及批次最后一个端口无效。

## 用法

组件位于 `src/port-capability.ts`,由沙箱宿主持有 `PortHost` 并代为操作:

```ts
import { PortHost } from './src/port-capability.ts';

const host = new PortHost();
const x = host.createDomain('X');
const y = host.createDomain('Y');
y.onmessage = (msg) => {
  // msg.transfer 中的端口已归 Y 所有;msg.reply(...) 可回发
};

const { port1: a, port2: b } = host.createChannel('X', 'Y'); // a 归 X,b 归 Y
const { port1: c } = host.createChannel('X', 'Y');

host.postMessage(x, a, 'take-c', [c]); // 转移 c 与消息入队原子完成
host.pump('Y');                        // 投递给 Y 的 onmessage
host.closeDomain('X');                 // 关闭并回收不可达端口
```

语义要点:

- `postMessage` 先校验后提交:任何失败(域已关闭、端口已回收、不持有端口、
  批次内重复、经端口转移自身)都抛 `PortCapabilityError` 且无副作用;
  成功后旧句柄立即失效(`NOT_OWNER`)。
- 转移目标域 = 对端端口当前的持有域;对端若无主(悬空),消息滞留在其邮箱。
- 邮箱是 `PausableQueue`:`port.mailbox.pause()` 后消息只缓冲不投递,
  `resume()` 后由 `pump()` 按 FIFO 派发,用于测试交错时序。
- 关闭域时做标记-清除回收:从开放域持有的端口出发,沿纠缠对端边与在途
  转移边标记;不可达端口连同排队消息销毁。纠缠端点同生共死,端口环在
  失去外部可达性后被整体回收。

运行测试:`npm test`(Node >= 23.9,使用 `--experimental-strip-types`)。
