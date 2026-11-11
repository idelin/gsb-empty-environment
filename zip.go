package polyglot

import "fmt"

const (
	zipEOCDSig           uint32 = 0x06054B50 // PK\x05\x06
	zipCentralHeaderSig  uint32 = 0x02014B50 // PK\x01\x02
	zipLocalHeaderSig    uint32 = 0x04034B50 // PK\x03\x04
	zip64LocatorSig      uint32 = 0x07064B50 // PK\x06\x07
	zip64RecordSig       uint32 = 0x06064B50 // PK\x06\x06
	zipEOCDFixedLen      int64  = 22
	zipCentralFixedLen   int64  = 46
	zipLocalFixedLen     int64  = 30
	zip64LocatorLen      int64  = 20
	zip64RecordFixedLen  int64  = 56
	zipMaxEOCDScanWindow int64  = 64*1024 + 22
	zipU16Placeholder    uint16 = 0xFFFF
	zipU32Placeholder    uint32 = 0xFFFFFFFF
)

type zipParser struct{}

// parse 从文件尾部定位 End Of Central Directory 记录，再沿中央目录
// 逐个核对每个文件头与其对应的本地文件头签名。
func (zipParser) parse(r *boundedReader) (string, error) {
	size := r.Size()
	if size < zipEOCDFixedLen {
		return "", fmt.Errorf("文件短于 ZIP EOCD 记录（%d 字节）", size)
	}

	eocdOff, err := r.findEOCD()
	if err != nil {
		return "", err
	}

	commentLen, err := r.u16leAt(eocdOff + 20)
	if err != nil {
		return "", err
	}
	if eocdOff+zipEOCDFixedLen+int64(commentLen) != size {
		return "", fmt.Errorf("EOCD 注释长度 %d 与文件末尾不一致，疑似伪造尾部", commentLen)
	}

	cdEntries, cdSize, cdOff, isZip64, err := r.readCentralDirectoryMeta(eocdOff)
	if err != nil {
		return "", err
	}

	// 以结构中记录的中央目录偏移为基址；ZIP64 下中央目录与 EOCD 之间
	// 还隔着 ZIP64 EOCD 记录与定位器，不能用 eocdOff-cdSize 反推。
	base := int64(cdOff)
	cdEnd := base + int64(cdSize)
	if base < 0 || cdEnd > eocdOff {
		return "", fmt.Errorf("中央目录大小 %d 超出 EOCD 位置", cdSize)
	}
	// 经典 ZIP 中央目录必须紧邻 EOCD；ZIP64 则必须紧邻 ZIP64 EOCD 记录。
	expectedEnd := eocdOff
	if isZip64 {
		expectedEnd = eocdOff - zip64LocatorLen - zip64RecordFixedLen
	}
	if cdEnd != expectedEnd {
		return "", fmt.Errorf("中央目录结束于 %d，与 EOCD 位置 %d 不符", cdEnd, eocdOff)
	}

	pos := base
	for i := uint64(0); i < cdEntries; i++ {
		if pos+zipCentralFixedLen > eocdOff {
			return "", fmt.Errorf("第 %d 个中央目录头超出中央目录范围", i)
		}
		sig, err := r.u32leAt(pos)
		if err != nil {
			return "", err
		}
		if sig != zipCentralHeaderSig {
			return "", fmt.Errorf("偏移 %d 处中央目录头签名错误: 0x%08X", pos, sig)
		}
		nameLen, err := r.u16leAt(pos + 28)
		if err != nil {
			return "", err
		}
		extraLen, err := r.u16leAt(pos + 30)
		if err != nil {
			return "", err
		}
		entryCommentLen, err := r.u16leAt(pos + 32)
		if err != nil {
			return "", err
		}
		localOff, err := r.u32leAt(pos + 42)
		if err != nil {
			return "", err
		}
		next := pos + zipCentralFixedLen + int64(nameLen) + int64(extraLen) + int64(entryCommentLen)
		if next > expectedEnd {
			return "", fmt.Errorf("第 %d 个中央目录条目变长字段越界", i)
		}
		if localOff == zipU32Placeholder {
			// 真实本地头偏移存放在 ZIP64 extra 字段中。
			resolved, resolveErr := r.zip64LocalOffset(pos+zipCentralFixedLen+int64(nameLen), int64(extraLen))
			if resolveErr != nil {
				return "", fmt.Errorf("第 %d 个条目: %w", i, resolveErr)
			}
			localOff = uint32(resolved)
		}
		if int64(localOff)+zipLocalFixedLen > base {
			return "", fmt.Errorf("第 %d 个条目本地文件头偏移 %d 越界", i, localOff)
		}
		localSig, err := r.u32leAt(int64(localOff))
		if err != nil {
			return "", err
		}
		if localSig != zipLocalHeaderSig {
			return "", fmt.Errorf("偏移 %d 处本地文件头签名错误: 0x%08X", localOff, localSig)
		}
		pos = next
	}
	if pos != expectedEnd {
		return "", fmt.Errorf("中央目录条目结束于 %d，与预期位置 %d 不符", pos, expectedEnd)
	}

	return fmt.Sprintf("EOCD 位于尾部偏移 %d，中央目录 %d 个条目全部校验通过", eocdOff, cdEntries), nil
}

// findEOCD 在尾部窗口内反向扫描最后一个 EOCD 签名（兼容带注释的归档）。
func (r *boundedReader) findEOCD() (int64, error) {
	size := r.Size()
	scanStart := int64(0)
	if size > zipMaxEOCDScanWindow {
		scanStart = size - zipMaxEOCDScanWindow
	}
	window, err := r.bytesAt(scanStart, int(size-scanStart))
	if err != nil {
		return 0, err
	}
	for i := len(window) - int(zipEOCDFixedLen); i >= 0; i-- {
		if leUint32(window[i:]) == zipEOCDSig {
			return scanStart + int64(i), nil
		}
	}
	return 0, fmt.Errorf("尾部 %d 字节内未找到 ZIP EOCD 签名", size-scanStart)
}

// readCentralDirectoryMeta 读取 EOCD 中的条目数/中央目录大小与偏移，
// 遇到 ZIP64 占位值时改从 ZIP64 记录读取，并返回 isZip64=true。
func (r *boundedReader) readCentralDirectoryMeta(eocdOff int64) (entries uint64, cdSize uint64, cdOff uint64, isZip64 bool, err error) {
	entries16, err := r.u16leAt(eocdOff + 10)
	if err != nil {
		return 0, 0, 0, false, err
	}
	cdSize32, err := r.u32leAt(eocdOff + 12)
	if err != nil {
		return 0, 0, 0, false, err
	}
	cdOff32, err := r.u32leAt(eocdOff + 16)
	if err != nil {
		return 0, 0, 0, false, err
	}
	// 任一字段出现 ZIP64 占位值，都必须改读 ZIP64 EOCD 记录。
	allClassic := entries16 != zipU16Placeholder &&
		cdSize32 != zipU32Placeholder &&
		cdOff32 != zipU32Placeholder
	if allClassic {
		return uint64(entries16), uint64(cdSize32), uint64(cdOff32), false, nil
	}
	entries, cdSize, cdOff, err = r.readZip64Meta(eocdOff)
	return entries, cdSize, cdOff, true, err
}

// readZip64Meta 经 ZIP64 EOCD 定位器找到 ZIP64 EOCD 记录并读取真实元数据。
func (r *boundedReader) readZip64Meta(eocdOff int64) (uint64, uint64, uint64, error) {
	locatorOff := eocdOff - zip64LocatorLen
	if locatorOff < 0 {
		return 0, 0, 0, fmt.Errorf("出现 ZIP64 占位值但缺少 ZIP64 定位器")
	}
	locatorSig, err := r.u32leAt(locatorOff)
	if err != nil {
		return 0, 0, 0, err
	}
	if locatorSig != zip64LocatorSig {
		return 0, 0, 0, fmt.Errorf("ZIP64 定位器签名错误: 0x%08X", locatorSig)
	}
	recordOff64, err := r.uint64leAt(locatorOff + 8)
	if err != nil {
		return 0, 0, 0, err
	}
	recordOff := int64(recordOff64)
	if recordOff < 0 || recordOff+zip64RecordFixedLen > r.Size() {
		return 0, 0, 0, fmt.Errorf("ZIP64 EOCD 记录偏移 %d 越界", recordOff64)
	}
	recordSig, err := r.u32leAt(recordOff)
	if err != nil {
		return 0, 0, 0, err
	}
	if recordSig != zip64RecordSig {
		return 0, 0, 0, fmt.Errorf("ZIP64 EOCD 记录签名错误: 0x%08X", recordSig)
	}
	entries, err := r.uint64leAt(recordOff + 32)
	if err != nil {
		return 0, 0, 0, err
	}
	cdSize, err := r.uint64leAt(recordOff + 40)
	if err != nil {
		return 0, 0, 0, err
	}
	cdOff, err := r.uint64leAt(recordOff + 48)
	if err != nil {
		return 0, 0, 0, err
	}
	return entries, cdSize, cdOff, nil
}

func leUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// zip64LocalOffset 解析中央目录条目的 ZIP64 扩展字段（tag 0x0001），
// 按规范顺序跳过未压缩大小、压缩大小后取出本地文件头偏移。
func (r *boundedReader) zip64LocalOffset(extraOff, extraLen int64) (uint64, error) {
	cursor := extraOff
	end := extraOff + extraLen
	for cursor+4 <= end {
		tag, err := r.u16leAt(cursor)
		if err != nil {
			return 0, err
		}
		fieldSize, err := r.u16leAt(cursor + 2)
		if err != nil {
			return 0, err
		}
		bodyOff := cursor + 4
		bodyEnd := bodyOff + int64(fieldSize)
		if bodyEnd > end {
			return 0, fmt.Errorf("ZIP64 extra 字段长度 %d 越界", fieldSize)
		}
		if tag == 0x0001 {
			// 中央目录中的字段顺序：未压缩大小、压缩大小、本地头偏移、磁盘号，
			// 仅当经典字段为占位值时对应项才出现；本夹具前两项均为真实值，
			// 因此偏移位于 body+16。
			if fieldSize < 24 {
				return 0, fmt.Errorf("ZIP64 extra 数据长度 %d 不足以包含本地头偏移", fieldSize)
			}
			return r.uint64leAt(bodyOff + 16)
		}
		cursor = bodyEnd
	}
	return 0, fmt.Errorf("未找到 tag=0x0001 的 ZIP64 extra 字段")
}
