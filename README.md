# NuGet锁文件contentHash核对

packages.lock.json 记录每个包的 resolved 版本与 contentHash，攻击者改动已发布版本的内容，或在依赖图里塞入无人引用的包。为Go构建网关写可导入的 NuGet 锁文件核对包：每个条目含 type（Direct、Transitive、CentralTransitive）、requested、resolved 与 contentHash（SHA-512 的 base64）；Direct 条目的 resolved 必须满足 requested 的版本区间（如 [1.0,2.0)）；contentHash 须与调用方给出的实际 .nupkg 的 SHA-512 base64 完全一致；同一包 ID（不分大小写）在同一目标框架下出现两条 resolved 不同的条目拒绝；Transitive 条目必须被其他条目的 dependencies 引用，孤立的拒绝；缺 contentHash 的条目拒绝。用越出区间、哈希不符、大小写同名、孤立传递依赖和缺哈希验证。
