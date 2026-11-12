//go:build linux

package netsec

import (
	"fmt"
	"os"
	"syscall"
)

func defaultOps() Ops {
	return Ops{
		Lstat:              os.Lstat,
		BindMount:          bindMount,
		MountReadonlyTmpfs: mountReadonlyTmpfs,
		RemountReadonly:    remountReadonly,
		DevNull:            devNullDevice,
	}
}

func bindMount(source, target string) error {
	return syscall.Mount(source, target, "", syscall.MS_BIND, "")
}

func mountReadonlyTmpfs(target string) error {
	return syscall.Mount("tmpfs", target, "tmpfs", syscall.MS_RDONLY, "")
}

func remountReadonly(target string) error {
	if err := syscall.Mount(target, target, "", syscall.MS_BIND, ""); err != nil {
		return err
	}
	return syscall.Mount("", target, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, "")
}

func devNullDevice() (uint32, uint32, error) {
	var st syscall.Stat_t
	if err := syscall.Stat("/dev/null", &st); err != nil {
		return 0, 0, err
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return 0, 0, fmt.Errorf("/dev/null 不是字符设备 (mode %o)", st.Mode)
	}
	return major(st.Rdev), minor(st.Rdev), nil
}

// Linux 设备号解码（对应内核 new_encode_dev 编码）。
func major(dev uint64) uint32 {
	return uint32((dev>>8)&0xfff) | uint32((dev>>32)&0xfffff000)
}

func minor(dev uint64) uint32 {
	return uint32(dev&0xff) | uint32((dev>>12)&0xffffff00)
}
