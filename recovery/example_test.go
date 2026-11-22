package recovery_test

import (
	"fmt"

	"prescreen.local/ddd-9509-20261122-v2/recovery"
)

// 宿主侧典型集成：带外信任锚 -> 乱序导入记录 -> Evaluate -> 导出可独立
// 验证的最小 Bundle。这里只演示 API 形状，密钥与树由宿主目录/见证提供。
func ExampleNewAcceptor() {
	anchor := recovery.TrustAnchor{
		DirectoryKey: []byte("out-of-band-directory-public-key"),
		WitnessKey:   []byte("out-of-band-witness-public-key"),
	}

	var delivered []byte
	acceptor := recovery.NewAcceptor(anchor, func(d recovery.Decision) {
		if d.Status == recovery.StatusAccepted {
			delivered = d.Request.WrappedRecoveryKey
		}
	})

	// 离线见证允许先收到回执，再收到目录前缀；记录顺序不影响最终结论。
	// for _, r := range wireRecords { _ = acceptor.Ingest(r) }

	_ = acceptor
	fmt.Println(len(delivered) == 0)
	// Output: true
}
