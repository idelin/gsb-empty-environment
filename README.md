# config-migrator

基于规则的配置迁移库（TypeScript）。支持键的一拆多、多合一，按前置条件与优先级执行，
实现可逆转换并记录损失项；重复迁移是稳定不动点；新旧键同时出现且矛盾时拒绝猜测；
输出每个新值的来源与未消费的旧字段。

## 快速开始

```ts
import { Migrator, rename, split, merge } from 'config-migrator';

const migrator = new Migrator([
  rename('user-name', 'userName', 'user.name'),                 // 1:1 改名（可逆）
  split('name', 'fullName', ['firstName', 'lastName'], ' '),    // 一拆多
  merge('endpoint', ['host', 'port'], 'endpoint', ':'),         // 多合一
]);

const result = migrator.migrate({
  userName: 'ada',
  fullName: 'Ada Lovelace',
  host: 'example.com',
  port: '8080',
  legacyFlag: true,
});

result.config;      // { 'user.name': 'ada', firstName: 'Ada', lastName: 'Lovelace', endpoint: 'example.com:8080' }
result.provenance;  // 每个新键 -> { rule, from } 来源
result.losses;      // 无法无损往返的信息（含原值，便于归档）
result.unconsumed;  // ['legacyFlag'] 未被任何规则消费的旧键
```

反向迁移（回滚）使用各规则的可逆部分：

```ts
const back = migrator.revert(result.config); // 还原旧 schema；不可逆部分记入 back.losses
```

## 规则

- `rename(id, from, to, opts?)` — 1:1 改名，完全可逆。
- `split(id, from, to[], separator, opts?)` — 一拆多。按前 `to.length - 1` 个分隔符切分，
  因此分隔符数量足够时可无损还原；片段不足时缺失部分被省略并记录损失项。
- `merge(id, from[], to, separator, opts?)` — 多合一。反向按同样规则切分；
  源键缺失时记录损失项。
- `transform(spec)` — 自定义规则；提供 `backward` 即获得可逆能力。

每条规则声明 `from`（消费的旧键）与 `to`（产生的新键），并可设置：

- `priority` — 数值大者先执行（默认 0，同优先级按注册顺序）。规则上下文提供
  `ctx.has(key)` / `ctx.get(key)`，低优先级规则可据此实现“仅在高优先级规则未产出时兜底”。
- `when(source)` — 前置条件，返回 false 时跳过该规则（仅正向迁移时求值）。

## 语义

- **冲突拒绝猜测**：输入同时含有旧键与新键时，若二者一致则正常消费旧键；
  若矛盾则抛出 `MigrationConflictError`（包含全部冲突明细），不做任何猜测。
- **幂等**：对已迁移的输出再次迁移，结果不变（`migrate(migrate(x)) === migrate(x)`）。
- **损失记录**：正向/反向转换中无法无损往返的信息通过 `ctx.addLoss` 记入
  `result.losses`（含规则 id、阶段、涉及键与丢失的原值）；无 `backward` 的规则
  在回滚时整体记为损失。
- **来源追踪**：`result.provenance` 记录每个新键由哪条规则、从哪些旧键产出；
  输入中已存在的新键记为 `{ rule: null, from: [key] }`。
- **未消费字段**：未被任何规则消费的输入键列入 `result.unconsumed`；
  传入 `{ carryUnknown: true }` 可将它们原样带入输出。
- 规则一旦触发即消费其存在的 `from` 键（即使选择不产出，如兜底规则主动退让）。

## 开发

```sh
npm install   # 安装 typescript（devDependency）
npm test      # 编译并运行测试
```
