# 未加引号的服务路径被插入了程序

Windows服务注册表里的可执行路径含空格却未加引号，攻击者在同名目录放`Program.exe`，服务启动时优先执行该程序。请实现供C++20服务管理代理调用的可导入服务配置校验组件。解析服务可执行文件与启动参数时要求路径加引号并与参数分离，路径不得位于用户可写目录、不得为相对路径或含未解析变量。服务账户、依赖、恢复动作与SID类型一并校验，安装与更新都走同一验证，失败拒绝写入。用未加引号路径、同名程序抢占、用户目录、变量展开、合法带引号路径和更新场景验证只启动声明的程序。

## 组件

`netsec` 是一个纯 C++20、无平台头文件依赖的静态库（`netsec.hpp` / `netsec.cpp`），
因此可在任意主机上编译并单测，同时按 Windows 服务控制管理器（SCM）的语义校验配置。

- `parseImageCommandLine`：把 `ImagePath` 拆成“可执行路径 + 启动参数”，要求可执行
  路径以双引号包裹并与参数分离；未加引号、引号未闭合、空路径都会被标记。
- 路径安全：`isAbsoluteWindowsPath` 拒绝相对路径与盘符相对路径；
  `containsUnresolvedVariable` 拒绝 `%NAME%`、悬空 `%`、空 `%%`；
  `normalizeWindowsPath` 统一大小写、正反斜杠并解析 `.`/`..`；
  `isPathWithinRoot` 结合 `defaultUserWritableRoots`（可用
  `ValidationOptions::additionalWritableRoots` 扩展）拒绝用户可写目录。
- `unquotedSearchCandidates` 与 `resolveLaunchedProgram`：纯函数模拟
  `CreateProcess` 对未加引号首 token 的前缀探测顺序，用文件存在性回调注入磁盘状态，
  可证明未加引号时 `Program.exe` 会先于声明程序被执行，而带引号时只会启动声明程序。
- `validateServiceConfig`：统一校验入口，覆盖镜像路径、服务账户（内置主体、
  `DOMAIN\user`、UPN/gMSA）、依赖（合法性与去重）、恢复动作（含 `RunCommand`
  命令行复用同一套校验）以及服务 SID 类型。
- `IServiceStore` / `MemoryServiceStore` / `ServiceManager`：持久化边界与门面。
  `install` 与 `update` 共用同一条 `validateAndWrite` 路径，校验失败时绝不调用
  `write`，并返回结构化的 `StoreStatus` 与 `Violation` 列表。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

`netsec_tests` 覆盖：未加引号路径拒绝、同名程序抢占、用户可写目录（含 `..` 逃逸与
可配置根）、未解析变量与相对路径、合法带引号路径（含无参数、UNC、正斜杠）、账户/
依赖/恢复/SID 校验，以及安装与更新共用校验、失败零写入、最终只启动声明程序。
