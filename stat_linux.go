//go:build linux

package netsec

import (
	"os"
	"syscall"
)

func identifyFile(info os.FileInfo) (fileID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
}

func fileVersionOf(info os.FileInfo) fileVersion {
	v := fileVersion{
		size:  info.Size(),
		mode:  info.Mode(),
		modNS: info.ModTime().UnixNano(),
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		v.changeNS = st.Ctim.Sec*1_000_000_000 + st.Ctim.Nsec
	}
	return v
}
