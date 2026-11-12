//go:build darwin

package netsec

import (
	"os"
	"syscall"
)

func statInoGen(fi os.FileInfo) (uint64, uint32) {
	s := fi.Sys().(*syscall.Stat_t)
	return s.Ino, 0 // Darwin Stat_t 不暴露 i_generation
}

func statUID(fi os.FileInfo) uint32 { return fi.Sys().(*syscall.Stat_t).Uid }

func statGID(fi os.FileInfo) uint32 { return fi.Sys().(*syscall.Stat_t).Gid }
