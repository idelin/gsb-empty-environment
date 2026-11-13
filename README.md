# 安装脚本能力计划宿主

`netsec` 是一个可导入的 Go 包，用来在不启动真实进程的前提下验证包构建脚本的能力声明、宿主授权、路径边界、子进程继承和发布事务。

模块路径：`example.org/netsec/q0085`

## 模型

- 包图节点由内容摘要 `Digest`、依赖边和脚本声明组成。
- 脚本只能声明 `Read(path)`、`Write(path)`、`Exec(toolID)`、`Connect(endpointID)`、`Env(name)`。
- `Policy` 按包摘要授予精确动作集合；`Catalog` 登记精确工具 ID、端点 ID 和环境值。
- `Planner` 输出每个包的 `PackageManifest`，其中的 `Granted` 与 `Denied` 是声明、策略和注册表求交后的结果。
- `Execution` 是脚本回调唯一可见的能力面。每次实际动作都会写入 `Event`，拒绝时写入带包、脚本、子进程谱系和动作的 `Violation`。

`Exec` 和 `Connect` 仅验证宿主登记 ID，不创建进程或网络连接。环境值不进入计划、事件或错误，只在被授权的 `Env(name)` 调用中返回。

## 路径与子进程

- `Read` 只解析该包封存的源码根，`Write` 只解析该包独立的 staging 输出根。
- 路径必须相对、使用 `/`，不得包含绝对前缀、`.`、`..`、空段或 NUL。
- 解析逐段检查目录节点，任何 symlink 父项或最终 symlink 都拒绝；不会跟随目标。
- 写操作可在自己的输出根内创建缺失父目录，但不能越过根或命中 symlink。
- `Spawn` 派生的合成子进程只获得 `父级有效能力 ∩ 子级声明`，不能新增网络、工具或读路径。

## 执行与发布

```go
policy := netsec.NewPolicy()
_ = policy.Grant(pkgDigest, netsec.Read("src/input"), netsec.Write("bin/app"))

catalog := netsec.NewCatalog()
_ = catalog.RegisterTool("go", "Go toolchain")
_ = catalog.RegisterEnvironment("TOKEN", "value-injected-at-runtime")

host, _ := netsec.NewHost(netsec.HostConfig{
    Policy:  policy,
    Catalog: catalog,
    Files:   netsec.NewVirtualFS(),
    Cache:   netsec.NewCache(),
})

plan, _ := host.Plan(graph)
result, _ := host.Run(ctx, plan, netsec.Runners{
    {Package: pkgDigest, Name: "build"}: func(ctx context.Context, e *netsec.Execution) error {
        input, err := e.Read("src/input")
        if err != nil {
            return err
        }
        return e.Write("bin/app", input)
    },
})
```

依赖就绪的包并行运行，但各自写独立输出根。发布阶段按稳定的依赖优先顺序合并；同一路径或文件/目录祖先冲突会拒绝后发布的整个包。

任一越权、回调错误或 panic 都会使该包输出不可见并撤销其传递后继。冲突包和被撤销后继的缓存会被移除；无关成功包继续发布并保留不可变缓存。`Result` 同时给出：

- `Packages`：每个包的最终状态和可见性。
- `Events`：实际动作轨迹，包括被拒绝的动作。
- `Violations`：从包到脚本/子进程/违规动作的证据。
- `Revocations`：从失败根因到被撤销后继的依赖链。
- `Published`：最终可见文件及其所属包、写入脚本和动作。

## 恶意轨迹测试

`security_test.go` 覆盖：

- 绝对路径、点段和 symlink 父项读取宿主凭据；
- 绝对路径、点段、symlink 父项和最终 symlink 写出安装根；
- 工具、端点、环境变量别名不归一化；
- 子进程声明父级未持有的网络能力；
- 两个并行包写同一路径，以及文件/目录发布冲突；
- 回调错误后的整包回滚、后继撤销和无关缓存保留；
- 计划被修改后不能扩权，缓存快照不可变。

运行：

```sh
go test ./...
go test -race ./...
go vet ./...
```
