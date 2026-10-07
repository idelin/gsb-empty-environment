package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// PrefixDigest 计算源文件 [0, offset) 前缀内容的 SHA-256 摘要（十六进制）。
// 恢复时用它核对源文件前缀未被篡改。
func PrefixDigest(path string, offset int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.CopyN(h, f, offset)
	if err != nil && err != io.EOF {
		return "", err
	}
	if n < offset {
		return "", fmt.Errorf("importer: source file truncated: need %d bytes, got %d", offset, n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
