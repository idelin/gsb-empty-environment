package admission

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// fakeVerifier 模拟调用方的摘要范围校验回调。
type fakeVerifier struct {
	want         DigestCoverage
	got          DigestCoverage
	called       bool
	missingCD    bool // 让回调宣称摘要未覆盖中央目录
	signatureErr error
}

func (f *fakeVerifier) verify(coverage DigestCoverage, _ SigBlock) error {
	f.called = true
	f.got = coverage
	if f.signatureErr != nil {
		return f.signatureErr
	}
	if coverage != f.want {
		return errors.New("digest coverage mismatch")
	}
	if f.missingCD {
		return errors.Join(ErrDigestCoverage, errors.New("central directory segment not covered by digest"))
	}
	return nil
}

// archive 是合成安装包的字节与关键偏移。
type archive struct {
	data             []byte
	localStart       int64
	cdStart          int64
	eocdStart        int64
	sigStart         int64
	sigSizeOfBlock   uint64
	firstLocalOffset uint32
}

func le16(v uint16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, v)
	return b
}

func le32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func le64(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

// buildLocalEntry 构造本地文件头 + 数据。
func buildLocalEntry(name string, payload []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x50, 0x4b, 0x03, 0x04})
	b.Write(le16(20))                   // version needed
	b.Write(le16(0))                    // flags
	b.Write(le16(0))                    // compression: stored
	b.Write(le16(0))                    // mod time
	b.Write(le16(0))                    // mod date
	b.Write(le32(0))                    // crc32
	b.Write(le32(uint32(len(payload)))) // compressed size
	b.Write(le32(uint32(len(payload)))) // uncompressed size
	b.Write(le16(uint16(len(name))))    // file name length
	b.Write(le16(0))                    // extra length
	b.WriteString(name)
	b.Write(payload)
	return b.Bytes()
}

// buildCentralEntry 构造对应的中央目录条目，localOffset 为本地头偏移。
func buildCentralEntry(name string, payload []byte, localOffset uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x50, 0x4b, 0x01, 0x02})
	b.Write(le16(20)) // version made by
	b.Write(le16(20)) // version needed
	b.Write(le16(0))  // flags
	b.Write(le16(0))  // compression
	b.Write(le16(0))  // mod time
	b.Write(le16(0))  // mod date
	b.Write(le32(0))  // crc32
	b.Write(le32(uint32(len(payload))))
	b.Write(le32(uint32(len(payload))))
	b.Write(le16(uint16(len(name)))) // file name length
	b.Write(le16(0))                 // extra length
	b.Write(le16(0))                 // comment length
	b.Write(le16(0))                 // disk number start
	b.Write(le16(0))                 // internal attrs
	b.Write(le32(0))                 // external attrs
	b.Write(le32(localOffset))       // local header offset
	b.WriteString(name)
	return b.Bytes()
}

func buildEOCD(cdOffset, cdSize uint32, entries uint16, comment []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x50, 0x4b, 0x05, 0x06})
	b.Write(le16(0))       // disk number
	b.Write(le16(0))       // disk with central directory
	b.Write(le16(entries)) // entries on this disk
	b.Write(le16(entries)) // total entries
	b.Write(le32(cdSize))  // central directory size
	b.Write(le32(cdOffset))
	b.Write(le16(uint16(len(comment))))
	b.Write(comment)
	return b.Bytes()
}

// sigPair 构造一个签名分块 ID-value pair。
func sigPair(id uint32, value []byte) []byte {
	pairLen := uint64(4 + len(value))
	var b bytes.Buffer
	b.Write(le64(pairLen))
	b.Write(le32(id))
	b.Write(value)
	return b.Bytes()
}

// buildSigBlock 用给定 pairs 构造 APK Signing Block。
func buildSigBlock(pairs ...[]byte) (block []byte, sizeOfBlock uint64) {
	var pairData bytes.Buffer
	for _, p := range pairs {
		pairData.Write(p)
	}
	// AOSP 定义：
	//   头部 sizeOfBlock = head(8) + pairs + tail(8)，不含 magic；
	//   尾部 sizeOfBlock = pairs + tail(8) + magic(16)，不含 head。
	headSizeOfBlock := uint64(pairData.Len()) + 8 + 8
	sizeOfBlock = headSizeOfBlock
	var b bytes.Buffer
	b.Write(le64(headSizeOfBlock))
	b.Write(pairData.Bytes())
	b.Write(le64(headSizeOfBlock + 8))
	b.Write([]byte("APK Sig Block 42"))
	return b.Bytes(), sizeOfBlock
}
