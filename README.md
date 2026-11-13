# 双构建产物的安全等价核对

## `artifactcmp` 包

导入路径 `example.org/netsec/q0084/artifactcmp`，入口为 `Compare(left, right Tree, sourceCommit string, rules Rules, closure ExecutableClosure)`。

- 规范化：`ValidateRelPath` 拒绝绝对路径、`.`/`..` 点段、空段与非规范分隔符；`normalize` 保证路径唯一、父项存在且为目录，任何节点不得穿过 symlink 父项。
- 可忽略项仅限 `IgnoreModTime`、`IgnoreOwner`（uid/gid）与 `IgnoredContentPaths`；后者必须是调用方逐字列明、两侧都存在的普通非可执行文件，glob、目录、可执行文件一律拒绝。
- 比较：先要求路径与类型全等；模式（含执行位）始终整体比较；普通文件比 SHA-256 摘要；symlink 目标按字节摘要比较。
- 可执行闭包：以带执行位的普通文件为入口集合调用回调，任何在 `IgnoredContentPaths` 中却被闭包实际读取的路径直接判差异——未比较字节不会被当作可信。
- 输出：`Report.Differences` 为最小差异路径，每条携带双方 `Evidence`（类型、模式、摘要/symlink 哈希、时间与属主）；仅当全等时签发 `Receipt`，绑定源码提交、双构建者身份、规则摘要与两侧规范树根（内容替换会改变根哈希）。

测试位于 `artifactcmp/compare_test.go`，其中 `referenceClosure` 与 `referenceOracle` 是不共享生产代码的独立树遍历/比较参考实现，覆盖时间差、调试注释差、脚本执行位、symlink 换目标、被忽略数据进入闭包等场景，并与包结果交叉核对。

发布门禁要求两个独立构建者从同一源码生成等价制品，但时间戳等允许差异不能掩盖可执行内容替换。给Go门禁宿主实现可导入的制品比较包，输入是两个已解析归档树，每项含规范相对路径、类型{file,dir,symlink}、模式、字节、修改时间和来源构建者；路径唯一且不得绝对、含点段或穿过symlink父项。调用方给允许忽略的元数据字段及路径模式，模式只能忽略时间、uid/gid或明确的调用方列明路径的非执行文件内容，绝不能忽略类型、symlink目标、可执行文件内容或模式的执行位。先按规则规范化，再要求两树路径与类型全等；普通文件摘要比较，symlink目标逐字节比较。另接受调用方的可执行闭包回调，从入口集合返回实际读取路径；任何只在一侧存在、摘要不同或被忽略但进入闭包的对象都拒绝。输出最小差异路径及双方证据，完全相等才签发绑定源码提交、两构建者身份、规则摘要和规范树根的回执。用独立树遍历参考测试时间差、调试注释差、脚本执行位、symlink换目标及被忽略数据后来进入闭包，不能把“可复现”扩大成未比较字节也可信。
