# 额外字段穿越

ZIP的Info-ZIP Unix额外字段里的链接目标越界。额外字段中的路径同样要做根内规范化；越界失败。额外字段没有路径时按条目名检查。归档读取调用可导入的Go解码器做这次裁决。在额外字段放入绝对链接目标。

## 用法

`netsec.Decode` / `netsec.Validate` 会同时读取中央目录和本地文件头的额外字段。
它解析 `0x000d`、`0x5855`、`0x756e` 中的链接目标，以及 `0x7075` 中的
Unicode 路径。绝对路径、盘符路径和越过根的路径返回 `ErrPathTraversal`；
额外字段没有路径时，条目名仍会按根内路径检查。

```go
archive, err := netsec.DecodeFile("archive.zip")
if err != nil {
    // 越界路径、损坏的额外字段或无效 ZIP。
}
```

命令行检查：

```sh
go run ./cmd/zipguard check archive.zip
```

`testdata/absolute-link.zip` 是在 `0x000d` 和 `0x5855` 额外字段中都放入
绝对链接目标 `/tmp/q0339-link-target` 的测试归档。
