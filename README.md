# SFrame 帧头准入（RFC 9605）

用于 WebRTC `TransformStream` / Insertable Streams 的 SFrame 帧头准入包。
它不实现密码学：帧头解析、KID 发送者绑定、重放检查在本包内完成，认证与
解密由调用方注入。

## 安全约束

- 严格按 RFC 9605 解析 `X`、`K`、`Y`、`C` 位和变长 KID/CTR。
- 拒绝非最短编码：`0..7` 必须内联在 `K`/`C`；更大的值必须使用最少字节数，
  前导零字节或过长编码都会被拒绝。
- KID 以 `(trackId, kid) -> senderId` 登记；未登记或属于其他发送者的 KID
  不会进入解密回调。
- 同一 KID 的 CTR 必须严格大于上一次已接受的 CTR。
- CTR 只在注入的认证解密成功后提交。伪造的未来 CTR 不会污染重放窗口；
  认证失败的帧不会推进状态。

## 用法

```ts
import {
  SFrameFrameAdmission,
  SFrameKIDRegistry,
  SFrameReplayGuard,
  createSFrameAdmissionTransform,
} from "./index.ts";

const registry = new SFrameKIDRegistry();
registry.register({ trackId: "audio", kid: 0x123, senderId: "alice" });

const admission = new SFrameFrameAdmission({
  registry,
  trackId: "audio",
  senderId: "alice",
  // 调用方注入认证解密。抛错或返回 false 表示拒绝。
  decrypt: async (context, payload) => {
    // context.headerBytes 是 RFC 9605 AEAD 的 AAD。
    // 使用 context.kidBigInt / context.ctrBigInt 选择密钥和 nonce，
    // 对 payload 做认证解密；失败时抛错或返回 false。
    await authenticateAndDecrypt(
      context.senderId,
      context.kidBigInt,
      context.ctrBigInt,
      context.headerBytes,
      payload,
    );
    // 返回 undefined/true 表示原帧放行。
    // 返回 Uint8Array 或 { data } 可替换 frame.data。
  },
});

const transform = createSFrameAdmissionTransform(admission);
// readable.pipeThrough(transform).pipeTo(...)
```

`SFrameFrameAdmission#admit(frame)` 直接返回放行的帧或抛出 `SFrameError`
子类。`createTransform()` 默认丢弃被拒绝的帧，避免恶意帧导致整条媒体流
中断；可设置 `rejectBehavior: "throw"` 让流报错。

## 帧头 API

```ts
const header = parseSFrameHeader(frameBytes);
header.kidBigInt;    // 始终为完整 64 位 bigint
header.ctrBigInt;
header.kid;          // 安全整数范围内为 number，否则为 bigint
header.headerBytes;  // AAD
header.config;       // X(1) K(3) Y(1) C(3)
header.headerLength;
header.payloadOffset;

encodeSFrameHeader(kid, ctr); // 生成最短编码
```

`parseSFrameHeader` 接受 `ArrayBuffer`、`ArrayBufferView`、或带二进制
`data` 属性的帧对象；可用 `{ byteOffset }` 指定帧头在数据中的偏移。
`kid`/`ctr` 也可以用十进制字符串或 `0x` 十六进制字符串传入。

## 注册与重放

```ts
registry.register({ trackId: "video", kid: 7, senderId: "bob" });
registry.assertOwnedBy("video", 7, "bob");

// 两参数形式使用 "*" 作为全局回退；显式的轨道登记优先。
registry.register(9, "carol");

const replayGuard = new SFrameReplayGuard();
replayGuard.check("video", 7, 10);   // 只检查，不推进
replayGuard.commit("video", 7, 10);  // 认证成功后调用
```

## 错误码

`SFrameError#code` 提供稳定分类，包括：

`NON_MINIMAL_ENCODING`、`TRUNCATED_HEADER`、`KID_NOT_REGISTERED`、
`KID_SENDER_MISMATCH`、`KID_ALREADY_REGISTERED`、`REPLAY_DETECTED`、
`AUTHENTICATION_FAILED`。

## 测试

```sh
npm test
```
