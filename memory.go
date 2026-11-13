package netsec

import (
	"bytes"
	"errors"
	"fmt"
)

// GuestMemory 是客户机内存的只读视图。
//
// Version 必须单调递增：任何对客户机内存的写都必须使版本号前进。
// 宿主不得反向假设——最终版本相同不能证明读取期间没有发生过修改，
// 因此快照在版本一致之外还要求两次读取字节一致（见 snapshot）。
type GuestMemory interface {
	ReadAt(p []byte, addr uint64) (int, error)
	Version() uint64
}

var (
	// ErrABA 表示快照期间检测到 ABA：版本倒退，或版本相同但字节发生变化。
	ErrABA = errors.New("netsec: guest memory ABA detected during snapshot")
	// ErrUnstable 表示客户机内存持续变化，无法在有限次尝试内取得一致快照。
	ErrUnstable = errors.New("netsec: guest memory too unstable to snapshot")
)

// maxSnapshotAttempts 限定快照重试次数，避免被持续抖动饿死。
const maxSnapshotAttempts = 8

// snapshot 把 [addr, addr+n) 的客户机内存一致地复制到宿主私有缓冲区。
//
// 一致性判定（缺一不可）：
//  1. 读取前后版本号一致；
//  2. 版本号不倒退（倒退即拒绝，视为 ABA）；
//  3. 同一版本纪元内再读一次，两次字节完全相同——版本相同但字节不同
//     同样视为 ABA 并拒绝，不允许用"最终版本相同"证明期间没变。
func snapshot(mem GuestMemory, addr uint64, n int) ([]byte, error) {
	buf := make([]byte, n)
	verify := make([]byte, n)
	for attempt := 0; attempt < maxSnapshotAttempts; attempt++ {
		v1 := mem.Version()
		if _, err := mem.ReadAt(buf, addr); err != nil {
			return nil, fmt.Errorf("netsec: snapshot read: %w", err)
		}
		v2 := mem.Version()
		if v2 < v1 {
			return nil, fmt.Errorf("%w: version regressed (%d -> %d)", ErrABA, v1, v2)
		}
		if v2 != v1 {
			continue // 读取期间有写入，重试
		}
		// 版本一致还不够：同纪元内再读一次比对字节，防 ABA。
		if _, err := mem.ReadAt(verify, addr); err != nil {
			return nil, fmt.Errorf("netsec: snapshot verify read: %w", err)
		}
		if v3 := mem.Version(); v3 != v1 {
			if v3 < v1 {
				return nil, fmt.Errorf("%w: version regressed (%d -> %d)", ErrABA, v1, v3)
			}
			continue
		}
		if !bytes.Equal(buf, verify) {
			return nil, fmt.Errorf("%w: bytes changed under constant version %d", ErrABA, v1)
		}
		return buf, nil
	}
	return nil, ErrUnstable
}
