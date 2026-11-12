# 媒体轨替换守卫

攻击者在用户同意共享窗口后，用replaceTrack换成整屏或摄像头轨道。为TypeScript会议应用实现可导入的发送轨道守卫：同意记录绑定kind、deviceId和displaySurface，replaceTrack只许同一记录内轨道，设置变化或readyState异常时暂停发送。用伪造MediaStreamTrack测试窗口换整屏。

## 使用方式

```ts
import {
  createSendTrackGuard,
  createShareConsent,
} from "./index.ts";

// 用户同意共享某个窗口时，记录本次授权。
const consent = createShareConsent(windowTrack, { id: shareRequestId });
const guard = createSendTrackGuard(sender, consent);

// 后续轮换轨道必须仍处于同一授权记录内。
await guard.replaceTrack(nextWindowTrack);
```

`createShareConsent` 从 track 的 `getSettings()` 读取并冻结
`kind`、`deviceId`、`displaySurface`。若浏览器不为显示源提供
`deviceId`，调用方应显式传入用户同意时记录的源标识。

## 守卫语义

- `replaceTrack` 只接受 `kind`、`deviceId`、`displaySurface` 与授权记录完全一致且
  `readyState === "live"` 的轨道；`null` 表示主动停发。
- 默认会替换 sender 实例上的 `replaceTrack` 方法，因此直接持有 sender 的调用方也
  无法绕过校验。
- 任意 `settingschange` 事件都会立即停发并等待人工复核；若变化同时导致授权元组
  不匹配，则 `resume()` 会继续失败。
- `ended`、异常的 `readyState` 会立即停发。
- 停发动作是 `track.enabled = false` 加 `sender.replaceTrack(null)`；被拒绝的轨道
  不会传给原生 sender。
- 发生违规后守卫保持暂停，只有 `resume()` 重新校验通过才会恢复。

注意：实例方法补丁属于纵深防御。拥有任意 JavaScript 执行能力的攻击者仍可直接
调用原型方法；应用不应把该守卫当作 XSS 隔离边界。媒体授权记录本身应由可信的
用户同意流程创建，且不能由待分享的轨道自行生成。

## 测试

```sh
npm test
```

测试使用伪造的 `MediaStreamTrack` 和 `RTCRtpSender`，覆盖窗口轨道被替换为同
`deviceId` 的整屏轨道、摄像头轨道、设置漂移、`readyState` 结束以及直接调用
`sender.replaceTrack` 等路径。
