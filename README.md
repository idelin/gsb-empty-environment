# 共享缓冲区读取一致性

Worker 通过 SharedArrayBuffer 向主线程传入图像块，攻击者可在主线程校验长度和内容后继续改写，造成校验后读取不一致。为 TypeScript 主线程写可导入的缓冲读取包：每次只复制调用方登记范围的快照，范围用当时的 byteLength 判断，不接受越界的视图；复制后再对快照校验，业务只使用快照。用另一线程在校验与复制之间改变长度和内容、零长度、偏移溢出验证结果仍与校验对象一致。

## 用法

```ts
import { readAndValidate, registerRange, registerView } from "./index.ts";

// 1. 登记范围：用当时的 byteLength 判断，越界（含零长度偏移超界、偏移溢出）抛 RangeError
const range = registerRange(sharedBuffer, 0, 64 * 1024);
// 或从已有视图登记：registerView(new Uint8Array(sharedBuffer, 0, 4096))

// 2. 每次读取：先复制快照到私有 ArrayBuffer，再对快照校验，业务只用快照
const snapshot = readAndValidate(range, (s) => {
  return s.byteLength >= 16 && s[0] === 0x89; // 校验失败抛 SnapshotValidationError
});
// snapshot 与共享缓冲区无关联，之后攻击者的改写不可见
```

防御要点：

- `readSnapshot` 只在操作开始时读一次 `buffer.byteLength`，长度与偏移随后固定；
- 边界判断用 `byteOffset > bufferByteLength - byteLength` 的减法形式，偏移溢出无法绕过；
- 越界视图（底层可缩放缓冲区缩小后）在登记时即被拒绝；
- 校验与业务读取作用于同一个私有快照，不存在校验后读取不一致。

## 测试

```sh
npm test
```
