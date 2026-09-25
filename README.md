# restricted-path-engine

受限属性路径引擎（TypeScript，零依赖）。将路径表达式编译为**可暂停的执行计划**，
在严格的**步数预算**内求值，只读取**显式自有数据属性**，所有错误均以用户输入的
路径表达式（而非内部对象）呈现。

## 运行

需要 Node.js >= 22.6（直接以类型擦除方式运行 TypeScript，无需构建）：

```sh
node examples/demo.ts        # 运行样例
node --test test/*.test.ts   # 运行内置测试（node:test）
```

## 路径表达式语法

```
$                        根
$.a.b  $.'a b'           子属性（标识符或引号字符串，标识符支持 Unicode 字母）
$.a[0]                   非负整数下标
$.a[*]                   通配：数组现有下标 / 对象自有可枚举键
$.a[?( <条件> )]         过滤子节点
$..id                    递归下降（DFS，带祖先环防护）
```

条件表达式（只读策略：有限集合、整数条件、布尔运算，无外部身份服务）：

```
@.a.b == 1   @.a != 'x'   @.n < 10   @.n >= 2        整数/字符串比较
@.t in ['a', 'b']                                       有限集合成员
exists(@.m)  @.flag                                     存在性 / 真值
A && B   A || B   !A   (A)                              布尔运算
字面量: 整数 | '字符串' | true | false | undefined
```

## 安全与正确性策略

- **原型链不可见**：只读自有属性；`in`、原型访问一律不发生。
- **拒绝危险键**：`__proto__`、`constructor`、`prototype` 在**解析期**报错，
  运行时 `getOwn` 再做纵深防御；通配与递归下降枚举时也会跳过这些键。
- **不触发用户代码**：取值只经 `getOwnPropertyDescriptor` 的**数据描述符**，
  getter/setter 永不调用、视为不存在；Proxy 的 `get` 陷阱不会触发（描述符
  自省本身可能被 Proxy 观察到，但任何用户代码值都不会被调用）；函数值可以
  作为结果被匹配，但永不被调用、不被遍历。
- **深度解析期报错**：段数（默认 ≤32）、递归下降次数（≤4）、`in` 集合大小
  （≤64）、表达式长度（≤512）均在 `compile()` 时校验。
- **步数有界**：段应用、通配/过滤枚举、过滤表达式每个节点、每条输出都消耗
  同一预算；超过 `maxTotalSteps`（默认 100000）抛 `BudgetExceededError`。
- **可暂停计划**：`plan.start(root)` 返回 `Execution`，`resume(steps)` 按
  授予的步数切片推进，可随时暂停/续跑，结果与一次性运行一致。
- **错误面向用户**：所有错误消息只含用户表达式与字符位置，不含内部对象。
- **数组洞 ≠ 显式 undefined**：洞在通配/过滤中被跳过、按下标读取为缺失；
  显式 `undefined` 是存在的值，`@ == undefined` 仅匹配它；缺失键使一切
  比较（含 `!=`）为假，`exists(@.k)` 区分两者。
- **版本绑定缓存**：`StructureCache` 以结构版本缓存匹配结果与路径证据；
  局部变更后调用 `invalidate(root)` 提升版本，下次查询重新计算并修订结果。

## 对照测试

`test/differential.test.ts` 用独立的慢解释器（`src/interpreter.ts`，无预算、
直接递归求值、共享同一安全访问层与纯语义函数）对照编译计划：

- 400 个种子化随机用例：随机结构（深度 ≤5、稀疏数组洞、显式 `undefined`、
  `__proto__`/`constructor`/空串/Unicode 等恶意键名）× 随机表达式；
- 3000 层深树：两引擎结果一致，小预算下编译计划有界报错；
- 恶意属性名：任何引擎的结果路径中均不出现被拒绝的键；
- 切片续跑（每次 1 步）与慢解释器结果完全一致。

## 结构

```
src/lexer.ts        词法分析          src/semantics.ts   两引擎共享的纯过滤语义
src/parser.ts       语法+静态限制      src/compiler.ts    编译为可暂停生成器计划
src/safeAccess.ts   安全属性访问层     src/interpreter.ts 慢速对照解释器
src/errors.ts       用户路径错误       src/cache.ts       版本绑定缓存与路径证据
examples/demo.ts    可运行样例         test/*.test.ts     node:test 测试
```
