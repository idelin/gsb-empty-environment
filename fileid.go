package netsec

import (
	"os"
	"path/filepath"
)

func fileIDFor(path string, info os.FileInfo) fileID {
	if id, ok := identifyFile(info); ok {
		return id
	}
	return fileID{path: filepath.Clean(path)}
}
