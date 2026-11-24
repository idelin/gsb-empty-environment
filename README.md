# 移动存储保护类验证包

这是一个可直接导入的 TypeScript 验证库，用于检查“备份服务恢复路径后，旧进程仍持有原文件句柄”这类移动存储场景。库把**路径**、**句柄**和**文件对象**建模为三个不同身份：

- `path`：固定的路径名字符串。
- `handle`：固定句柄名字符串，打开后始终指向打开时的文件对象。
- `object`：创建事件产生的 inode 式对象身份。

因此 `unlink`、`rename`、`replace`、`restore` 都不会改变已经打开句柄的指向，也不会撤销句柄。

## 快速使用

```ts
import { analyze, verifySafetyProof, verifyWitness } from "./src/index.ts";

const graph = {
  initial: [
    { path: "/mnt/backup/a", protectionClass: "public", contentLabel: "public" },
  ],
  events: [
    { id: "01-open-old", op: { kind: "open", path: "/mnt/backup/a", handle: "old" } },
    {
      id: "02-restore",
      op: {
        kind: "restore",
        path: "/mnt/backup/a",
        protectionClass: "public",
        contentLabel: "complete",
      },
      before: ["01-open-old"],
    },
    {
      id: "03-open-new",
      op: { kind: "open", path: "/mnt/backup/a", handle: "fresh" },
      before: ["02-restore"],
    },
    { id: "04-lock", op: { kind: "lock" }, before: ["03-open-new"] },
    { id: "05-read-new", op: { kind: "read", handle: "fresh" }, before: ["04-lock"] },
  ],
};

const result = analyze(graph);
if (result.status === "unsafe") {
  console.log(result.prefix);
  console.log(result.violation);
  console.log(verifyWitness(graph, result.witness).ok);
} else if (result.status === "safe") {
  console.log(verifySafetyProof(graph, result.proof).ok);
} else if (result.status === "unknown") {
  console.log("budget exhausted", result.budget);
}
```

## 事件模型

事件形如：

```ts
interface EventSpec {
  id: string;
  op: Operation;
  before?: string[]; // 必须先于本事件
  deps?: string[];   // before 的别名
  after?: string[];  // 本事件必须先于这些事件
}
```

事件最多 24 个。偏序图必须是 DAG；未知事件、重复事件、循环或超过上限会返回 `{ status: "invalid" }`。

支持的操作：

| `kind` | 语义 |
| --- | --- |
| `create` | 在不存在路径创建新对象。 |
| `restore` | 备份恢复：路径处生成新对象；旧对象只保留在旧句柄中。 |
| `replace` | 替换已存在路径为新对象；旧句柄不变。 |
| `open` | 打开路径当前对象，记录本次锁屏时点。 |
| `close` | 关闭句柄，使其失效。 |
| `read` | 经句柄读。可选 `path` 仅用于报告，不参与前置条件；取消/替换路径不影响句柄。 |
| `readPath` | 经路径读，等价于“此刻新开一个临时句柄”。 |
| `unlink` | 删除路径映射；不撤销句柄。 |
| `rename` | 移动路径映射；不撤销句柄；目标已存在时覆盖目标映射。 |
| `link` | 硬链接：让两个路径指向同一对象；不改变保护类。 |
| `setClass` | 修改对象保护类；对象所有别名立即看到变化。 |
| `copy` | 经源句柄读源，在目标路径创建新对象。 |
| `lock` / `unlock` | 锁屏 / 解锁。重复锁屏或重复解锁记为拒绝。 |

卷配置：

```ts
const graph = {
  volumes: {
    internal: "complete",
    removable: { defaultClass: "public" },
  },
  pathVolumes: {
    "/mnt/usb/a": "removable",
  },
  defaultVolume: "internal",
  defaultClass: "public",
  // ...
};
```

`create`、`restore`、`replace`、`copy` 的目标对象保护类默认取目标路径所属卷的默认类。

## 保护语义

每个对象有两个独立字段：

- `protectionClass`：对象当前保护类，决定读权限。
- `contentLabel`：内容保密标签，复制时保留；用于判定是否在锁屏后读出了标为 `complete` 的内容。

读授权（经句柄或路径）：

- `public`：始终允许。
- `untilLock`：未锁屏时允许；锁屏时只允许“本次锁屏之前已打开且仍有效”的句柄。
- `complete`：锁屏时禁止一切读；未锁屏时允许。

锁屏时点用递增的 `lockSerial` 表示。句柄打开时记录 `openedAtLockSerial`。因此：

1. 解锁后再次锁屏会形成新的锁屏时点；
2. 第二次锁屏前打开的句柄仍然合格；
3. 第二次锁屏后才打开的句柄不合格。

违规判定独立于读授权：只要某个**成功**的 `read`、`readPath` 或 `copy` 源读取发生在锁屏期间，且读到对象的 `contentLabel === "complete"`，就是违规。这样能捕获：

- `complete` 对象直接读：会被授权拒绝，不算成功读；
- 备份恢复/复制把 `complete` 内容放到 `public` 或 `untilLock` 类的新对象上：授权可能通过，但内容标签仍是 `complete`，会被判违规。

复制规则：

- 必须经源句柄读取，读授权不通过时整个复制事件拒绝，目标路径零副作用；
- 目标对象保护类取目标卷默认类（或显式 `protectionClass`）；
- `contentLabel` 始终继承源内容标签，显式传入更低标签会被拒绝；
- 硬链接是同一对象，不创建新标签；通过当前存在多路径别名的对象降类会被拒绝（`alias-downgrade`）。

`setClass` 修改保护类但不改内容标签。对象只有一个路径时允许显式升降类；对象有多个路径别名时拒绝降类，防止通过硬链接或临时别名降低原对象保护类。

## 结果

```ts
type AnalysisResult =
  | { status: "unsafe"; prefix: string[]; violation: ViolationDetail; witness: ... }
  | { status: "safe"; proof: SafetyProof; explored: ... }
  | { status: "unknown"; reason: "budget"; budget: ...; explored: ... }
  | { status: "invalid"; reason: string };
```

分析器检查每个满足偏序的执行前缀（包括被拒绝事件构成的前缀）：

- 返回**最短**违规前缀；
- 同长度按事件 ID 字典序取最小；
- 预算耗尽返回 `unknown`，不谎报安全。

事件前置条件失败会生成 `rejected` 记录，只把事件记为已执行，不改变路径、对象、句柄、锁状态；后续事件继续分析。

## 状态闭包证明

`safe` 结果带一个纯数据证明：

- 按资源冲突和偏序边拆成独立组件；
- 每个组件记录完整可达状态闭包和所有已启用事件的有向边；
- 安全证明中的每条边都可以被检查器重新执行。

`verifySafetyProof(graph, proof)` 会独立验收：

1. 图摘要是否匹配；
2. 组件划分是否覆盖所有事件且没有遗漏冲突；
3. 每个状态是否可达；
4. 每条边的事件是否已启用、是否能重放出目标状态；
5. 每个已启用事件是否都有对应边；
6. 是否存在任何违规边。

所以从证明中删掉任意一条可行边，检查器都会因“缺少已启用事件的边”拒绝；给输入图删掉一条真实依赖边后复用旧证明，也会因摘要或状态重放失败被拒绝。

## 预算

```ts
analyze(graph, {
  maxStates: 200_000,
  maxTransitions: 1_000_000,
  maxDepth: 24,
});
```

独立文件的事件会被拆成独立组件，因此 24 个互不相干的操作不会产生 `24!` 级别的状态爆炸。

## 测试

```bash
npm test
```

测试覆盖：

- 小图全排列与精确分析结果对拍；
- 24 个独立文件操作；
- 同一路径替换成不同对象、旧句柄不变；
- 二次锁屏与新锁屏时点；
- 同一对象跨路径改类；
- 复制/恢复后 `complete` 标签泄漏；
- 硬链接降类拒绝；
- 删掉证明中一条可行边的伪证明；
- 删掉输入图依赖边后复用旧证明；
- 预算耗尽返回 `unknown`。
