# diagnostic-version-vector

编辑器诊断管理库：接收连续文件版本流，后台诊断引擎异步（可能乱序）返回结果，
通过**多文件版本向量**校验决定诊断是否可以展示或复用。

## 核心概念

### FileStamp = (epoch, version)

单个 `version` 不足以标识文件状态：

- 文件关闭重开后，编辑器会从 1 重新编号；
- 文件删除后可能重建，版本号再次从 1 开始。

因此每个文件携带一个 `epoch`（代次），每次 `openFile` 时递增，且在
close/delete 之后仍然保留计数，保证同一文件不同"生命周期"的 stamp 永不冲突。

### VersionVector（版本向量）


一次诊断请求发出时，对目标文件的**完整传递依赖闭包**（含自身）做快照：
每个依赖文件记录 `(epoch, version)`；不存在（未打开/已删除）的依赖显式记录为
`null`——这样"依赖被删除"和"依赖后出现"都会破坏向量相等性。

### 展示条件（发布守卫）

诊断结果只有在以下**全部**满足时才发布并缓存：

1. 请求未被更新的请求取代（per-file 请求记录 + `superseded` 标记）。
   即使 provider 忽略 `AbortSignal`、取消失败后仍然 resolve（"僵尸请求"），
   结果也会被丢弃；
2. 请求时快照的版本向量与当前状态**逐项相等**——目标文件自身版本不变
   并不够，闭包内任何依赖的编辑、关闭重开（epoch 变化）、删除或新建都会
   使结果失效。

### 复用条件（精确）

缓存的诊断可复用，当且仅当：缓存时记录的版本向量与**当前依赖闭包**重新
快照的向量完全相等。这意味着：

- 闭包外文件的任意改动不影响复用（不会过度失效）；
- 闭包内任何变化（包括依赖图结构变化导致闭包成员增减）都精确失效；
- 目标文件已关闭时不可复用。

### 连续版本

`updateFile` 要求版本严格递增（连续版本流）；过期或重复的版本直接抛错拒绝。

## API 概览

- `openFile(uri, initialVersion)` / `closeFile(uri)` / `deleteFile(uri)` / `updateFile(uri, version)`
- `requestDiagnostics(uri): Promise<Diagnostic[] | undefined>` — 命中缓存时直接返回；
  否则取消旧请求并发起新请求；结果失效时 resolve 为 `undefined`
- `getReusableDiagnostics(uri)` / `getPublishedDiagnostics(uri)` — 按精确复用条件查询
- `snapshotVector(uri)` / `isVectorCurrent(vector)` — 向量快照与校验
- `onDiagnosticsPublished(listener)` — 订阅发布事件

## 运行测试

需要 Node.js >= 23.6（内置 TypeScript 类型擦除）：

```sh
npm test
```

测试覆盖：乱序返回、依赖编辑失效（单文件版本相同不够）、传递依赖、
关闭重开（epoch 隔离）、依赖删除/新建、取消失败的僵尸请求、精确复用条件、
非连续版本拒绝。
