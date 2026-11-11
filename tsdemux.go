// Package tsdemux 实现面向直播录制场景的 MPEG-TS 解复用读取器。
//
// 它解析 PAT/PMT 及其版本更新，按 PID 重组跨包 PES，处理丢包、
// continuity_counter 跳变、重复包与适配字段，并把 PTS/DTS/PCR 的
// 模回绕展开为单调递增的 64 位时间。入口类型为 Reader。
package tsdemux
