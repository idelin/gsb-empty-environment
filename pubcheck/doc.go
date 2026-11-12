// Package pubcheck 为 Go 测试宿主提供惰性初始化对象的安全发布检查。
//
// 背景：双重检查锁等惰性初始化模式可能先把对象指针发布到共享位置，再完成
// 敏感字段的初始化。另一个 goroutine 若仅按现实时间先后读取该指针，可能看到
// 未初始化或半初始化的对象。
//
// 事件模型显式区分五类事件：
//   - KindFieldWrite：对象字段写（初始化敏感状态）；
//   - KindPublish：指针发布写（把指针写入共享位置）；
//   - KindAcquireRead：获取侧对已发布指针的读取；
//   - KindReleaseSync：释放侧同步（close、WaitGroup Done、Unlock 等）；
//   - KindAcquireSync：获取侧同步（通道接收、Wait、Lock 等）。
//
// 先行发生图只包含两类边：同一 goroutine 内的程序顺序边，以及释放/获取同步
// 事件按 SyncMode（通道 FIFO 或屏障）配对产生的 synchronizes-with 边。
// Event.Wall 仅用于宿主排查，构造图时被完全忽略——现实时间先后不构成同步。
//
// 对象通过 Ref（ObjectID + Generation）定位。对象被回收重用时必须分配新的
// Generation，不同代次上的访问相互隔离，避免旧代次的初始化写掩盖新代次竞争。
//
// 任一字段读取若与同代次的初始化写之间不存在先行发生路径，Check 即返回
// 包含具体竞争对与分阶段诊断路径的 Race。
package pubcheck
