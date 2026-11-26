数据库连接池复用已编译计划时，若缓存键遗漏主体身份、视图展开链路或行级策略代次，撤权后的会话仍可能通过派生视图读取旧表。给Go数据库宿主实现可导入的安全计划缓存模型：编译输入含规范查询AST摘要、参数类型列表、调用主体ID、角色闭包摘要、展开后的完整依赖图（含嵌套视图的viewID与viewDefVersion及底层表的objectID与schemaVersion）、所需操作、每张引用表的RLS策略摘要与全局policyEpoch；输出计划包含完整不可变依赖清单与执行算子。参数值不进入计划身份但执行时仍走结构化绑定，严格禁止仅凭SQL文本或视图名复用计划。缓存命中后执行前须再次校验：主体仍活跃、角色闭包与policyEpoch未变、全部嵌套视图定义版本未降级或改写、底层对象ID/schemaVersion与权限仍满足；任一变化须判为未命中并触发重编译。事务固定初始权限快照，但带immediate标记的REVOKE或视图RLS策略收紧须使正在等待执行的计划立即失败并阻止任何算子回调执行；无immediate的撤权仅影响下一事务。连接切换主体必须立即失效全部会话局部句柄，GLOBAL缓存项仍可供新主体按自身全键匹配使用。另实现针对待审cacheKey构造器的有界差分搜索器：在主体、角色闭包、嵌套视图版本、对象版本、RLS摘要与epoch各有两态的网格上自动搜索弱键碰撞但授权结果相异的最短反例。通过可观察算子执行回调验证撤权、DROP后同名重建、嵌套视图RLS联动收紧、池化换用户与事务边界，确保未过复核的计划零算子执行。

## 实现

可导入包 `plancache`（及差分搜索子包 `plancache/diffsearch`），仅依赖标准库，Go 1.22+。

- `plancache.CompileInput` / `CacheKey`：AST 摘要、参数类型、主体 ID、角色闭包摘要、完整展开依赖图（嵌套视图 `ViewID`/`ViewDefVersion`、底表 `ObjectID`/`SchemaVersion`）、所需操作、每表 RLS 摘要与全局 `PolicyEpoch`；不含 SQL 文本与关系名，`CacheKey.Hash()` 为确定性 SHA-256。
- `plancache.Plan` / `Manifest`：完整不可变依赖清单 + 执行算子；参数值只在执行期经 `checkBindings` 做结构化、按位类型绑定，不进入键。
- `Executor.Prepare`：仅按全键查 SESSION/GLOBAL 缓存；命中后执行前在事务快照上复核主体活跃、角色闭包、epoch、视图身份/版本、对象身份/版本、RLS 摘要与权限，漂移即未命中并用新鲜输入重编译；immediate 事件在事务级硬熔断，阻止重编译与任何算子回调。
- `Snapshot` + `InvalidationBus`：事务 `Begin` 固定权限/目录快照；非 immediate 撤权只影响下一事务，immediate REVOKE / RLS 收紧 / DDL 通过单调 fence 穿透快照，使等待队列中的计划在首个算子前失败。
- `ConnectionPool` / `PooledConnection.SwitchPrincipal`：换主体立即失效全部会话句柄并清空 SESSION 缓存；GLOBAL 缓存保留，且新主体只能凭自身全键命中。
- `diffsearch.Search`：六维两态（主体、角色闭包、视图版本、对象版本、RLS 摘要、epoch）网格上按 Hamming 距离有界枚举，输出“键碰撞但授权结果相异”的最短反例；内置全键构造器（无反例）与三个弱构造器样例。
- `TraceObserver` 暴露编译、命中/未命中、复核失败、immediate 失败、句柄失效与算子回调事件；测试对撤权、DROP 同名重建、嵌套视图 RLS 联动收紧、池化换用户、事务边界逐一断言未过复核计划零算子执行。

## 验证

```sh
go vet ./...
go test -race -count=1 ./...
```
