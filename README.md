# 账户枚举错误可分辨性

登录服务在不存在账号与错误密码时对外应不可区分。实现供Go测试宿主调用的观测比较包，输入两类请求的状态码、头字段、正文模板、长度区间和分阶段耗时档位；仅允许调用方列明的随机字段被忽略，其他差异形成最小区分条件。用包含同名重复头、压缩后长度与多阶段超时的样本验证，不能只比最终错误文本。

## 用法

测试宿主将两类登录响应各自归一为一个 `netsec.Observation`（状态码、保留重复与顺序的头视图、占位符化的正文模板、原始/压缩长度区间、分阶段耗时档位），再调用 `netsec.Compare`：

```go
report := netsec.Compare(unknownAccount, wrongPassword, []netsec.FieldSelector{
    netsec.SelectHeader("X-Request-Id"), // 仅这些声明为随机的字段可忽略
})
if !report.Indistinguishable() {
    t.Fatalf("可区分条件: %+v", report.MinimalCondition())
}
```

- 长度与耗时均为闭区间/档位，区间重叠即视为不可区分；超时是独立档位，单侧超时必为区分条件。
- 未列入白名单的差异一律进入 `Report.Distinguishers`，被压制的差异保留在 `Report.Ignored` 以便审计。
- 运行：`go test -race ./...`。
