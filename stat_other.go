//go:build !linux && !darwin

package netsec

import (
	"os"
)

func identifyFile(info os.FileInfo) (fileID, bool) { return fileID{}, false }

func fileVersionOf(info os.FileInfo) fileVersion {
	return fileVersion{
		size:  info.Size(),
		mode:  info.Mode(),
		modNS: info.ModTime().UnixNano(),
	}
}
