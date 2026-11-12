// Package netsec 实现从低信任进程共享页读取变长记录的无锁 seqlock 读取器。
//
// 读取器只在以下条件全部满足时接受记录：采样窗口两端 (gen, seq) 一致且
// seq 为偶数、紧邻两次稳定快照逐字节相同、长度/字段/摘要来自同一快照且
// SHA-256 校验通过。重试受 MaxAttempts 预算约束，无法稳定时返回
// ErrUncertain，稳定复现的损坏分别归类为 ErrMalformed 与 ErrDigestMismatch。
package netsec
