# NuGet锁文件contentHash核对

packages.lock.json 记录每个包的 resolved 版本与 contentHash，攻击者改动已发布版本的内容，或在依赖图里塞入无人引用的包。为Go构建网关写可导入的 NuGet 锁文件核对包：每个条目含 type（Direct、Transitive、CentralTransitive）、requested、resolved 与 contentHash（SHA-512 的 base64）；Direct 条目必须出现在根框架依赖里，resolved 须满足 requested 的版本区间且不得低于下限；contentHash 须与调用方给出的实际 .nupkg 的 SHA-512 标准 base64 完全一致，URL-safe 变体拒绝；同一包 ID（不分大小写）在同一目标框架下出现两条 resolved 不同的条目拒绝，跨框架可不同；Transitive 条目必须被同框架其他条目的 dependencies 引用，孤立的拒绝；依赖闭包须无环且所有条目可达；缺 contentHash 的条目拒绝。用越出区间、版本降级、哈希不符、URL-safe 变体、跨框架同名、孤立依赖和不可达闭包验证。
