//go:build linux || darwin

package netsec

import (
	"io"
	"syscall"
)

// MmapPage 是基于 mmap 共享区域的 Page 实现，供采集宿主映射低信任进程
// 提供的共享内存文件（如 memfd、shm_open 或 tmpfs 文件）。
type MmapPage struct {
	mem []byte
}

// MapFile 以读写方式映射文件描述符的前 size 字节（MAP_SHARED）。
// 调用方负责在不再使用时调用 Unmap；fd 的生命周期仍归调用方所有。
func MapFile(fd uintptr, size int) (*MmapPage, error) {
	mem, err := syscall.Mmap(int(fd), 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return &MmapPage{mem: mem}, nil
}

// Unmap 解除映射。
func (p *MmapPage) Unmap() error {
	if p.mem == nil {
		return nil
	}
	err := syscall.Munmap(p.mem)
	p.mem = nil
	return err
}

func (p *MmapPage) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 || off > int64(len(p.mem)) {
		return 0, io.EOF
	}
	n := copy(b, p.mem[off:])
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

// WriteAt 仅用于宿主侧自测/初始化；低信任写入方通常在自己的进程内写。
func (p *MmapPage) WriteAt(b []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(p.mem)) || uint64(off)+uint64(len(b)) > uint64(len(p.mem)) {
		return 0, io.ErrShortWrite
	}
	return copy(p.mem[off:], b), nil
}

func (p *MmapPage) Size() int64 { return int64(len(p.mem)) }
