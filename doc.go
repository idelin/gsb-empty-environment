// Package netsec 实现 JPMS（Java Platform Module System）强封装下的
// 反射开放判定，供 Go 宿主的插件加载器在加载/执行插件前评估反射访问。
//
// 判定模型对应 java.lang.Module#canRead 与 isOpen/implAddOpens 的语义：
// 调用者模块 A 对目标模块 B 的包 p 调用 setAccessible 成功，当且仅当
// A 可读 B（含 requires transitive 的传递可读性）且 p 对 A 开放
// （open module、自动模块、未限定 opens、限定 opens 点名 A，或启动时
// --add-opens 补丁命中）。未命名模块与自动模块的所有包均视为开放。
package netsec
