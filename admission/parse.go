package admission

import (
	"encoding/binary"
	"errors"
	"io"
)

// ZIP/APK 结构魔数与固定尺寸。
var (
	eocdSignature             = []byte{0x50, 0x4b, 0x05, 0x06} // "PK\x05\x06"
	cdSignature               = []byte{0x50, 0x4b, 0x01, 0x02} // "PK\x01\x02"
	zip64EOCDLocatorSignature = []byte{0x50, 0x4b, 0x06, 0x07} // "PK\x06\x07"
	zip64EOCDSignature        = []byte{0x50, 0x4b, 0x06, 0x06} // "PK\x06\x06"
	apkSigBlockMagic          = []byte("APK Sig Block 42")
)

const (
	eocdFixedSize           = 22 // EOCD 固定部分（不含注释）
	eocdMaxCommentSize      = 0xffff
	eocdMaxScanSize         = eocdFixedSize + eocdMaxCommentSize
	cdHeaderFixedSize       = 46
	sigBlockMagicSize       = 16
	sigBlockLengthFieldSize = 8
	zip64LocatorSize        = 20
	zip64EOCDFixedSize      = 56
)

// zip64OffsetSentinel 是 ZIP 32 位字段中表示「真实值在 Zip64 扩展里」的哨兵。
const zip64OffsetSentinel uint32 = 0xffffffff

// 解析阶段的失败原因，与 Admit 的准入判定错误区分开。
var (
	ErrNotZip              = errors.New("admission: not a valid zip archive")
	ErrEOCD                = errors.New("admission: end of central directory record not found")
	ErrEOCDCommentOverflow = errors.New("admission: forged EOCD comment length overflows file size")
	ErrZip64               = errors.New("admission: zip64 archives are rejected")
	ErrCentralDirectory    = errors.New("admission: invalid central directory")
	ErrSigBlock            = errors.New("admission: invalid APK signing block")
)

// Layout 是从安装包字节中解析出的布局信息，可直接交给 AdmitLayout。
type Layout struct {
	First8                 []byte // 文件前 8 字节
	CentralDirectory       Range  // 中央目录区间
	EOCD                   Range  // 结束记录区间
	FirstLocalHeaderOffset int64  // 第一个本地文件头偏移（取自首个中央目录条目）
	SigBlock               SigBlock
	Size                   int64 // 文件总字节数
}

// reader 封装随机读取，集中处理短读与边界。
type reader struct {
	r    io.ReaderAt
	size int64
}

func (rd *reader) at(off int64, buf []byte) error {
	if off < 0 || off+int64(len(buf)) > rd.size {
		return io.ErrUnexpectedEOF
	}
	n, err := rd.r.ReadAt(buf, off)
	if err == io.EOF && n == len(buf) {
		err = nil
	}
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

// ParseLayout 从 r 解析 ZIP/APK 布局，size 为文件总字节数。
//
// 解析过程即包含安全校验：EOCD 必须精确落在文件末尾、注释长度不得溢出、
// 任何 Zip64 痕迹（EOCD Locator/记录或 0xffffffff 哨兵字段）都会被拒绝、
// 中央目录条目完整且首个条目的本地头偏移非负、APK 签名分块魔数与
// 前后长度字段一致。
func ParseLayout(r io.ReaderAt, size int64) (*Layout, error) {
	if size < 8 {
		return nil, ErrNotZip
	}
	rd := &reader{r: r, size: size}

	first8 := make([]byte, 8)
	if err := rd.at(0, first8); err != nil {
		return nil, err
	}

	eocdOff, err := findEOCD(rd)
	if err != nil {
		return nil, err
	}

	eocd := make([]byte, eocdFixedSize)
	if err := rd.at(eocdOff, eocd); err != nil {
		return nil, err
	}
	cdSize := int64(binary.LittleEndian.Uint32(eocd[12:16]))
	cdOffField := binary.LittleEndian.Uint32(eocd[16:20])
	cdOff := int64(cdOffField)
	commentLen := int64(binary.LittleEndian.Uint16(eocd[20:22]))

	// EOCD 必须精确抵达文件末尾，注释长度不得越过 EOF（防伪造注释长度溢出）。
	if eocdOff+eocdFixedSize+commentLen != size {
		return nil, ErrEOCDCommentOverflow
	}
	// 32 位偏移/长度字段出现 Zip64 哨兵即拒绝。
	if cdOffField == zip64OffsetSentinel ||
		binary.LittleEndian.Uint32(eocd[12:16]) == zip64OffsetSentinel {
		return nil, ErrZip64
	}
	// Zip64 EOCD Locator 位于 EOCD 之前，先于「CD 紧邻 EOCD」检查，
	// 因为带 Locator 时 CD 与 EOCD 之间天然隔着 20 字节。
	if err := rejectZip64Markers(rd, eocdOff); err != nil {
		return nil, err
	}
	if cdOff < 0 || cdSize <= 0 || cdOff+cdSize != eocdOff {
		return nil, errors.Join(ErrCentralDirectory, errors.New("central directory does not meet EOCD"))
	}

	firstLocal, err := parseCentralDirectory(rd, cdOff, cdSize)
	if err != nil {
		return nil, err
	}

	sig, err := parseSigBlock(rd, cdOff)
	if err != nil {
		return nil, err
	}

	return &Layout{
		First8:                 first8,
		CentralDirectory:       Range{Start: cdOff, End: cdOff + cdSize},
		EOCD:                   Range{Start: eocdOff, End: size},
		FirstLocalHeaderOffset: firstLocal,
		SigBlock:               sig,
		Size:                   size,
	}, nil
}
