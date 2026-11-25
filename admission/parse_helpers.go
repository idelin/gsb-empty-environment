package admission

import (
	"encoding/binary"
	"errors"
)

// equalBytes 比较两个字节切片，避免在热路径上引入 bytes 包的额外语义。
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// findEOCD 在文件尾部扫描窗口内定位 EOCD。要求 EOCD（含注释）精确抵达 EOF，
// 并区分「注释长度溢出」与「完全找不到」两种失败。
func findEOCD(rd *reader) (int64, error) {
	window := rd.size
	if window > eocdMaxScanSize {
		window = eocdMaxScanSize
	}
	buf := make([]byte, window)
	base := rd.size - window
	if err := rd.at(base, buf); err != nil {
		return 0, err
	}

	var commentOverflow bool
	// 从后向前找，取最靠后且精确抵达 EOF 的候选。
	for i := int(window) - eocdFixedSize; i >= 0; i-- {
		if !equalBytes(buf[i:i+4], eocdSignature) {
			continue
		}
		off := base + int64(i)
		commentLen := int64(binary.LittleEndian.Uint16(buf[i+20 : i+22]))
		recordEnd := off + eocdFixedSize + commentLen
		if recordEnd > rd.size {
			// 魔数合法但注释长度越过 EOF：典型的伪造 EOCD 偏移攻击。
			commentOverflow = true
			continue
		}
		if recordEnd == rd.size {
			return off, nil
		}
	}
	if commentOverflow {
		return 0, ErrEOCDCommentOverflow
	}
	return 0, ErrEOCD
}

// rejectZip64Markers 检查 EOCD 紧邻区域是否存在 Zip64 EOCD Locator，
// 若存在则进一步核对 Zip64 EOCD 记录魔数，任何 Zip64 痕迹都拒绝。
func rejectZip64Markers(rd *reader, eocdOff int64) error {
	if eocdOff < zip64LocatorSize {
		return nil
	}
	locator := make([]byte, zip64LocatorSize)
	if err := rd.at(eocdOff-zip64LocatorSize, locator); err != nil {
		return err
	}
	if !equalBytes(locator[0:4], zip64EOCDLocatorSignature) {
		return nil
	}
	zip64EOCDOff := int64(binary.LittleEndian.Uint64(locator[8:16]))
	if zip64EOCDOff >= 0 && zip64EOCDOff+zip64EOCDFixedSize <= rd.size {
		head := make([]byte, 4)
		if err := rd.at(zip64EOCDOff, head); err == nil && equalBytes(head, zip64EOCDSignature) {
			return ErrZip64
		}
	}
	return ErrZip64
}

// parseCentralDirectory 遍历中央目录条目，返回第一个条目的本地文件头偏移。
func parseCentralDirectory(rd *reader, cdOff, cdSize int64) (int64, error) {
	var firstLocal int64 = -1
	var pos int64
	end := cdOff + cdSize
	header := make([]byte, cdHeaderFixedSize)

	for pos < cdSize {
		cur := cdOff + pos
		if cur+cdHeaderFixedSize > end {
			return 0, errors.Join(ErrCentralDirectory, errors.New("truncated central directory header"))
		}
		if err := rd.at(cur, header); err != nil {
			return 0, errors.Join(ErrCentralDirectory, err)
		}
		if !equalBytes(header[0:4], cdSignature) {
			return 0, errors.Join(ErrCentralDirectory, errors.New("bad central directory entry signature"))
		}
		nameLen := int64(binary.LittleEndian.Uint16(header[28:30]))
		extraLen := int64(binary.LittleEndian.Uint16(header[30:32]))
		commentLenEntry := int64(binary.LittleEndian.Uint16(header[32:34]))
		localOffField := binary.LittleEndian.Uint32(header[42:46])

		if localOffField == zip64OffsetSentinel {
			return 0, ErrZip64
		}
		localOff := int64(localOffField)
		if localOff < 0 {
			return 0, errors.Join(ErrCentralDirectory, errors.New("negative local header offset"))
		}
		if firstLocal == -1 {
			firstLocal = localOff
		}

		entrySize := cdHeaderFixedSize + nameLen + extraLen + commentLenEntry
		if entrySize <= 0 || pos+entrySize > cdSize {
			return 0, errors.Join(ErrCentralDirectory, errors.New("central directory entry overruns directory"))
		}
		pos += entrySize
	}

	if firstLocal < 0 {
		return 0, errors.Join(ErrCentralDirectory, errors.New("central directory has no entries"))
	}
	return firstLocal, nil
}

// parseSigBlock 在中央目录之前探测并解析 APK Signing Block。
// 不存在签名分块时返回 Present=false 且 error 为 nil。
func parseSigBlock(rd *reader, cdOff int64) (SigBlock, error) {
	absent := SigBlock{Present: false}

	// 尾部布局：sizeOfBlock(8) + magic(16)，中央目录前至少要有这 24 字节。
	if cdOff < sigBlockMagicSize+sigBlockLengthFieldSize {
		return absent, nil
	}
	trailerOff := cdOff - sigBlockMagicSize
	magic := make([]byte, sigBlockMagicSize)
	if err := rd.at(trailerOff, magic); err != nil {
		return absent, nil
	}
	if !equalBytes(magic, apkSigBlockMagic) {
		return absent, nil
	}

	sizeAfterBuf := make([]byte, sigBlockLengthFieldSize)
	if err := rd.at(cdOff-sigBlockMagicSize-sigBlockLengthFieldSize, sizeAfterBuf); err != nil {
		return absent, err
	}
	sizeOfBlockAfter := binary.LittleEndian.Uint64(sizeAfterBuf)

	// 字段语义（与 AOSP ApkSigningBlockUtils 一致）：
	//   头部 sizeOfBlock = head(8) + pairs + tail(8)，不含 magic；
	//   尾部 sizeOfBlock = pairs + tail(8) + magic(16)，不含 head。
	// 物理块布局：head(8) + pairs + tail(8) + magic(16)。
	// after 已包含 magic，仅缺少 head，因此分块总长 = after + 8。
	blockLen := int64(sizeOfBlockAfter) + sigBlockLengthFieldSize
	if blockLen <= sigBlockMagicSize+sigBlockLengthFieldSize || blockLen > cdOff {
		return absent, errors.Join(ErrSigBlock, errors.New("signing block length out of range"))
	}
	blockStart := cdOff - blockLen
	if blockStart < 0 {
		return absent, errors.Join(ErrSigBlock, errors.New("signing block starts before file beginning"))
	}

	sizeBeforeBuf := make([]byte, sigBlockLengthFieldSize)
	if err := rd.at(blockStart, sizeBeforeBuf); err != nil {
		return absent, err
	}
	sizeOfBlockBefore := binary.LittleEndian.Uint64(sizeBeforeBuf)

	// 即使前后长度字段被篡改，也尽量用尾部字段定位块并识别签名 ID，
	// 把「长度不一致」留给 Admit 返回专门的 ErrSigBlockSizeMismatch；
	// 只有 pair 区本身无法解析时才作为结构性错误返回。
	hasV2, hasV3, err := scanSigBlockIDs(rd, blockStart, int64(sizeOfBlockBefore))
	if err != nil {
		return absent, err
	}

	return SigBlock{
		Present:           true,
		Start:             blockStart,
		SizeOfBlockBefore: sizeOfBlockBefore,
		SizeOfBlockAfter:  sizeOfBlockAfter,
		HasV2:             hasV2,
		HasV3:             hasV3,
	}, nil
}

// scanSigBlockIDs 遍历签名分块内的 ID-value pair，识别 v2/v3 签名 ID。
// pair 布局：uint64 pairLen（含其后 4 字节 ID，不含自身 8 字节）+ uint32 ID + value。
// pairs 区从分块开头 8 字节之后开始。头部 sizeOfBlock =
// head(8) + pairs + tail(8)（不含 magic），故
// pairsEnd = blockStart + sizeOfBlock - tail(8)。
func scanSigBlockIDs(rd *reader, blockStart, sizeOfBlock int64) (hasV2, hasV3 bool, err error) {
	pairsEnd := blockStart + sizeOfBlock - sigBlockLengthFieldSize
	pos := blockStart + sigBlockLengthFieldSize
	lenBuf := make([]byte, 8)
	idBuf := make([]byte, 4)

	for pos < pairsEnd {
		if pos+8 > pairsEnd {
			break
		}
		if err := rd.at(pos, lenBuf); err != nil {
			break
		}
		pairLen := int64(binary.LittleEndian.Uint64(lenBuf))
		if pairLen < 4 || pos+8+pairLen > pairsEnd {
			break
		}
		if err := rd.at(pos+8, idBuf); err != nil {
			break
		}
		switch binary.LittleEndian.Uint32(idBuf) {
		case V2SchemeID:
			hasV2 = true
		case V3SchemeID:
			hasV3 = true
		}
		pos += 8 + pairLen
	}
	return hasV2, hasV3, nil
}
