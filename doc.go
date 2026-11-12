// Package netsec 提供 cgo 指针传递规则的运行时校验，
// 供 Go 测试宿主的 cgo 包装层在调用 C 前后调用：
// 调用前用 Checker.CheckArg 确认传入指针所指内存不含 Go 指针，
// 调用返回后用 Checker.CheckRetained 确认 C 端持有表不再引用 Go 对象。
package netsec
