# 批量付款文件被重放

企业批量付款按文件标识处理，攻击者重放昨天的文件，银行重复扣款或重复入账。请实现供Go批量付款服务调用的可导入文件幂等组件。文件绑定企业、批次、日期、笔数与总金额并由客户签名，处理按文件摘要幂等；重复提交只返回原结果。文件内明细按指令标识去重，金额合计核对。用文件重放、明细重复、总额不符、合法提交和部分失败验证只扣款一次。

## 组件说明

- `batchfile.go`：`File`/`Header`/`Detail` 结构、规范化编码、HMAC-SHA256 签名与校验、文件摘要（幂等键）、笔数与总额核对、按指令标识去重。
- `processor.go`：`Processor` 按文件摘要幂等处理；重复提交（含并发）只返回首次结果，不重复扣款；`Store` 接口抽象结果持久化，附内存实现；`DebitFunc` 由服务方注入执行扣款。

## 用法

```go
p := netsec.NewProcessor(keyStore, store, debitFunc)
res, err := p.Process(ctx, signedFile) // 重放同一文件返回原结果，res.Replayed == true
```

## 测试

```sh
go test -race ./...
```
