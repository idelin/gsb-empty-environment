// layermerge-demo 构造若干内存镜像层，演示层合并组件对被替换中间层的拒绝：
// 第三个层的 blob 被篡改但仍声明旧摘要，合并必须在第 3 层中止并报告期望摘要。
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"prescreen.local/ps-5e1e53b8eb/layermerge"
)

type entry struct {
	name string
	body string
}

func makeGzipLayer(files []entry) ([]byte, string) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, file := range files {
		hdr := &tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			panic(err)
		}
		if _, err := io.WriteString(tw, file.body); err != nil {
			panic(err)
		}
	}
	if err := tw.Close(); err != nil {
		panic(err)
	}

	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	if _, err := gz.Write(tarBuf.Bytes()); err != nil {
		panic(err)
	}
	if err := gz.Close(); err != nil {
		panic(err)
	}
	blob := gzBuf.Bytes()
	sum := sha256.Sum256(blob)
	return blob, "sha256:" + hex.EncodeToString(sum[:])
}

func openLayer(blob []byte, digest string) layermerge.Layer {
	return layermerge.Layer{
		Digest:    digest,
		MediaType: layermerge.MediaTypeLayerGzip,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(blob)), nil
		},
	}
}

func main() {
	tempDir, err := os.MkdirTemp("", "layermerge-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tempDir)

	root := filepath.Join(tempDir, "rootfs")
	work := filepath.Join(tempDir, "staging")

	l1Blob, l1Digest := makeGzipLayer([]entry{
		{name: "etc/hostname", body: "demo-host"},
		{name: "etc/version", body: "1"},
	})
	l2Blob, l2Digest := makeGzipLayer([]entry{
		{name: "etc/version", body: "2"},
		{name: "etc/.wh.old.conf", body: ""},
	})
	// 清单登记的是原始第三层摘要，但实际 blob 已被替换。
	declaredBlob, declaredDigest := makeGzipLayer([]entry{
		{name: "etc/version", body: "3-original"},
	})
	replacedBlob, replacedDigest := makeGzipLayer([]entry{
		{name: "etc/version", body: "3-tampered"},
		{name: "etc/backdoor", body: "unexpected"},
	})
	l4Blob, l4Digest := makeGzipLayer([]entry{
		{name: "etc/version", body: "4"},
	})

	layers := []layermerge.Layer{
		openLayer(l1Blob, l1Digest),
		openLayer(l2Blob, l2Digest),
		openLayer(replacedBlob, declaredDigest),
		openLayer(l4Blob, l4Digest),
	}

	fmt.Println("合并 4 个层，其中第 3 层 blob 已被替换：")
	fmt.Printf("  第 3 层声明（期望）摘要: %s\n", declaredDigest)
	fmt.Printf("  第 3 层实际 blob 摘要:  %s\n", replacedDigest)

	err = layermerge.MergeLayers(root, layers, &layermerge.Options{WorkDir: work})
	if err == nil {
		fmt.Fprintln(os.Stderr, "错误：预期拒绝被替换的层，但合并成功了")
		os.Exit(2)
	}

	var layerErr *layermerge.LayerError
	if !errors.As(err, &layerErr) {
		fmt.Fprintf(os.Stderr, "未预期的错误类型: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("\n合并被拒绝：%v\n", err)
	fmt.Printf("  被拒绝层号: %d\n", layerErr.Index)
	fmt.Printf("  拒绝原因:   %s\n", layerErr.Kind)
	fmt.Printf("  期望摘要:   %s\n", layerErr.Expected)
	fmt.Printf("  实际摘要:   %s\n", layerErr.Got)

	// 对照组：使用原始 blob 与匹配摘要时，全部层应合并成功。
	goodRoot := filepath.Join(tempDir, "rootfs-good")
	goodWork := filepath.Join(tempDir, "staging-good")
	goodLayers := []layermerge.Layer{
		openLayer(l1Blob, l1Digest),
		openLayer(l2Blob, l2Digest),
		openLayer(declaredBlob, declaredDigest),
		openLayer(l4Blob, l4Digest),
	}
	if err := layermerge.MergeLayers(goodRoot, goodLayers, &layermerge.Options{WorkDir: goodWork}); err != nil {
		fmt.Fprintf(os.Stderr, "对照组合并失败: %v\n", err)
		os.Exit(2)
	}
	version, err := os.ReadFile(filepath.Join(goodRoot, "etc/version"))
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n对照组（摘要匹配）合并成功，最终 etc/version = %q\n", string(version))

	if _, statErr := os.Lstat(filepath.Join(root, "etc/backdoor")); !os.IsNotExist(statErr) {
		fmt.Fprintln(os.Stderr, "错误：被拒绝层的内容泄漏到了合并根目录")
		os.Exit(2)
	}
	fmt.Println("被拒绝层及其后的层均未落地，已合并根目录保持第 2 层之后的状态。")
}
