# File System Access 权限收缩

这是一个无依赖、可导入的 TypeScript 浏览器宿主权限模型，用于阻止已授权目录句柄向父目录或其它来源扩张权限。

## 安全约束

- 子句柄只能继承父句柄的有效权限；`requestPermission` 也不能越过父句柄授权。
- 撤销父句柄权限时，所有已创建的后代句柄同步失效并返回 `prompt`。父句柄之后重新授权不会静默恢复这些旧后代。
- `getFileHandle` / `getDirectoryHandle` 拒绝空名称、`/`、`\`、NUL 和包含 `..` 的名称。
- 句柄绑定创建时的来源；`cloneForOrigin`、`cloneForHost` 以及带 `origin` 的恢复操作都会拒绝跨来源使用。
- `persist()` 只序列化读授权。`FileSystemAccessHost.restore()` 恢复后读权限为 `granted`，所有层级写权限从 `prompt` 开始，必须逐级调用 `requestPermission({ mode: "readwrite" })` 并由宿主确认。

## 用法

```ts
import { FileSystemAccessHost } from "./index.ts";

const host = new FileSystemAccessHost({
  origin: "https://app.example",
  confirmPermission: async (request) => {
    // 接入真实的浏览器权限确认 UI。
    return request.mode === "readwrite" && userConfirmedWrite;
  },
});

const root = host.createRootDirectory("project", { mode: "readwrite" });
const notes = await root.getDirectoryHandle("notes");
const file = await notes.getFileHandle("today.txt");

const snapshot = host.persist();
const restarted = FileSystemAccessHost.restore(snapshot, {
  origin: location.origin,
});
const restoredRoot = restarted.getHandle(root.id);
const restoredNotes = restarted.getHandle(notes.id);
const restoredFile = restarted.getHandle(file.id);

await restoredRoot?.queryPermission(); // granted
await restoredFile?.queryPermission({ mode: "readwrite" }); // prompt

await restoredRoot?.requestPermission({ mode: "readwrite" }); // 重新确认
await restoredNotes?.requestPermission({ mode: "readwrite" });
await restoredFile?.requestPermission({ mode: "readwrite" });

// 撤销父权限后，原 notes 和 file 都变为 prompt。
host.revokePermission(root, "read");
```

## API 摘要

- `new FileSystemAccessHost({ origin, confirmPermission? })`：创建某个来源的宿主。
- `host.createRootDirectory(name, { mode })`：模拟目录选择器返回根句柄，默认仅读。
- `handle.queryPermission(descriptor)` / `handle.requestPermission(descriptor)`：查询或请求 `read` / `readwrite` 权限。
- `handle.getFileHandle(name)` / `handle.getDirectoryHandle(name)`：只接受单个安全子项名称。
- `host.revokePermission(handle, mode)`：降低权限，并让已派生的后代变为 `prompt`。
- `host.persist()`：生成 JSON 安全快照，不含写授权。
- `FileSystemAccessHost.restore(snapshot, { origin?, confirmPermission? })`：恢复只读授权；传入当前 `origin` 时会拒绝来源不符的快照。
- `handle.cloneForOrigin(origin)` / `cloneForHost(host)`：仅允许同来源包装。
- `restorePermissionState(snapshot, options)`：`restore` 的便捷导出。

`PermissionedFileSystemHost` 是同一个宿主类的原名，`FileSystemAccessHost` 为别名。

## 测试

```sh
npm test
```

测试覆盖父权限撤销、写权限降级、`..`/路径分隔符名称、跨来源克隆、重启后只恢复读权限以及逐级重新确认写权限。
