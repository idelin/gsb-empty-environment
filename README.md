# 常量时间比较泄漏判定

MAC 标签比较若在首个不同字节处提前返回，网络攻击者可通过响应时间逐字节猜测。为 Go 密码库的测试写可导入的常量时间比较验证包：被测比较函数通过调用方提供的字节访问记录接口读取输入，包在每个失配位置、长度不同和空输入下比较访问序列，序列依赖秘密内容就失败。用提前返回、先比长度、逐字节异或累加的正确实现和故意泄漏位置的实现验证判定。

## 用法

被测函数接收 `netsec.ByteView`，所有字节读取都经由 `ByteAt`，因此验证器可以记录完整访问序列：

```go
func compare(a, b netsec.ByteView) bool {
	if a.Len() != b.Len() {
		return false
	}
	var diff byte
	for i := 0; i < a.Len(); i++ {
		diff |= a.ByteAt(i) ^ b.ByteAt(i)
	}
	return diff == 0
}

func TestCompareConstantTime(t *testing.T) {
	if err := netsec.Verify(compare); err != nil {
		t.Fatal(err)
	}
}
```

验证器按公开长度分组，在每组内覆盖全部失配位置、不同长度组合和空输入，并比较精确的 `(来源, 下标)` 访问序列。长度本身不是秘密，因此访问序列可以随长度变化；固定长度对下若序列随字节内容变化，`Verify` 返回错误。

`netsec.Analyze` 返回结构化 `Report`，`netsec.Trace` 可用于检查单次比较的结果和访问记录。

## 自测

`verify_test.go` 使用四个对照实现验证判定：

- `compareXORAccumulate`：逐个异或累加，通过。
- `compareLengthFirst`：先比较公开长度，再常量时间扫描，通过。
- `compareEarlyReturn`：首个失配即返回，失败。
- `compareLeakPosition`：全量扫描但在失配处额外访问，失败。

运行：

```sh
go test ./...
```
