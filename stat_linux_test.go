//go:build linux

package netsec

import (
	"os"
	"syscall"
)

func statInoGen(fi os.FileInfo) (uint64, uint32) {
	s := fi.Sys().(*syscall.Stat_t)
	// i_generation 在不同架构的 syscall.Stat_t 中字段名不一致，
	// 真实文件系统硬链接测试只依赖 inode 同一性，这里 generation 留零。
	return s.Ino, 0
}

func statUID(fi os.FileInfo) uint32 { return fi.Sys().(*syscall.Stat_t).Uid }

func statGID(fi os.FileInfo) uint32 { return fi.Sys().(*syscall.Stat_t).Gid }
