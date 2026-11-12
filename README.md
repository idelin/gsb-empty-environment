# 特权程序库搜索路径

供发布准入服务调用的 Go 包。它读取 ELF 的 `DT_NEEDED`、`DT_RPATH`、
`DT_RUNPATH`、`DT_FLAGS_1` 和 `$ORIGIN`，再结合调用方提供的
`ld.so.cache` 快照、默认库目录及文件属主/权限视图，按安全执行模式下的
动态加载器顺序解析：

1. `DT_RPATH`（仅当没有 `DT_RUNPATH`）
2. `DT_RUNPATH`
3. `ld.so.cache`
4. 默认库目录（若未设置 `DF_1_NODEFLIB`）

`LD_LIBRARY_PATH`、`LD_PRELOAD` 在 setuid/setgid 安全模式下不被采用。
含斜杠的 `DT_NEEDED` 或 preload 条目直接按路径解析。解析会在第一个存在的
候选文件处停止，但会保留此前所有候选的风险：即使最终命中安全缓存，攻击者
仍可能在更早的可写目录补放同名库。

## 使用

```go
view := netsec.MapFileView{
	"/":              {Mode: fs.ModeDir | 0o755, UID: 0, GID: 0},
	"/opt":           {Mode: fs.ModeDir | 0o755, UID: 0, GID: 0},
	"/opt/helper":    {Mode: fs.ModeDir | 0o755, UID: 0, GID: 0},
	"/opt/helper/lib": {Mode: fs.ModeDir | 0o755, UID: 0, GID: 0},
	"/usr":           {Mode: fs.ModeDir | 0o755, UID: 0, GID: 0},
	"/usr/lib":       {Mode: fs.ModeDir | 0o755, UID: 0, GID: 0},
	"/usr/lib/libsupport.so.1": {Mode: 0o644, UID: 0, GID: 0},
}

report, err := netsec.Analyze("/opt/helper/helper", netsec.Options{
	Files:       view,
	DefaultDirs: []string{"/lib", "/usr/lib"},
	Cache: []netsec.CacheEntry{
		{Name: "libsupport.so.1", Path: "/usr/lib/libsupport.so.1"},
	},
	Preload: netsec.ParsePreloadList(preloadBytes),
})
```

`report.Safe` 仅在每个 `DT_NEEDED` 和 preload 条目都已解析且搜索过的
所有位置、最终文件均安全时为 `true`。相对路径、未知动态 token、非普通
文件、不可信属主、世界可写或不可信组可写路径都会产生稳定的 `IssueCode`。
调用方应提供完整的祖先目录快照；符号链接应先由快照层解析。

测试使用合成 ELF，覆盖可写 RPATH、可写路径先于安全缓存、相对 RPATH、
可写 `$ORIGIN`、安全 `$ORIGIN/../lib`、`RUNPATH` 覆盖 `RPATH`、cache/默认
目录顺序、preload 和无 section header 的 `PT_DYNAMIC` 解析。
