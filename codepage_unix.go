//go:build darwin || linux

package netsec

import (
	"syscall"
	"unsafe"
)

// CodePage is an anonymous memory mapping used as a generated code page.
// It starts out read-only executable and can be recycled through a
// Coordinator proof, after which MakeWritable turns it read-write.
type CodePage struct {
	region []byte
}

// NewCodePage allocates size bytes (rounded up to the page size), maps them
// read-write, writes code, then flips them to read-only executable. The page
// is never simultaneously writable and executable.
func NewCodePage(code []byte) (*CodePage, error) {
	pageSize := syscall.Getpagesize()
	size := ((len(code) + pageSize - 1) / pageSize) * pageSize
	if size == 0 {
		size = pageSize
	}
	region, err := syscall.Mmap(-1, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		return nil, err
	}
	copy(region, code)
	if err := mprotect(region, syscall.PROT_READ|syscall.PROT_EXEC); err != nil {
		_ = syscall.Munmap(region)
		return nil, err
	}
	return &CodePage{region: region}, nil
}

// Interval returns the half-open address interval backing the code page.
func (p *CodePage) Interval() Interval {
	if len(p.region) == 0 {
		return Interval{}
	}
	return Interval{
		Start: uintptr(unsafe.Pointer(&p.region[0])),
		End:   uintptr(unsafe.Pointer(&p.region[0])) + uintptr(len(p.region)),
	}
}

// MakeWritable implements PageProtector and downgrades the page to
// read-write, non-executable once the coordinator has proven it is dead.
func (p *CodePage) MakeWritable(target Interval) error {
	page := p.Interval()
	if target != page {
		return ErrInvalidInterval
	}
	return mprotect(p.region, syscall.PROT_READ|syscall.PROT_WRITE)
}

// Write replaces page contents. It must only be called after MakeWritable.
func (p *CodePage) Write(code []byte) error {
	if len(code) > len(p.region) {
		return ErrInvalidInterval
	}
	copy(p.region, code)
	return nil
}

// Release unmaps the page.
func (p *CodePage) Release() error {
	if p.region == nil {
		return nil
	}
	err := syscall.Munmap(p.region)
	p.region = nil
	return err
}

func mprotect(region []byte, prot int) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_MPROTECT,
		uintptr(unsafe.Pointer(&region[0])),
		uintptr(len(region)),
		uintptr(prot),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
