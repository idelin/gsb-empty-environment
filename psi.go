package tsdemux

// parsePAT 解析通过 CRC 校验的 PAT section，并按版本变化更新节目表。
func (rd *Reader) parsePAT(section []byte) {
	if section[0] != 0x00 {
		return
	}
	version := int(section[5]>>1) & 0x1F
	currentNext := section[5]&0x01 != 0
	if !currentNext {
		return // 下一生效版本暂不应用
	}

	// section_length 含最后 4 字节 CRC。
	sectionLength := int(section[1]&0x0F)<<8 | int(section[2])
	end := 3 + sectionLength - 4

	// 同版本重复广播无需重建节目表，直接返回，避免清掉已解析的 PMT 状态。
	if version == rd.patVersion {
		return
	}
	rd.patVersion = version
	rd.programs = make(map[uint16]*ProgramInfo)
	rd.pmtPIDs = make(map[uint16]bool)
	rd.streams = make(map[uint16]StreamInfo)

	for pos := 8; pos+3 < end; pos += 4 {
		programNumber := uint16(section[pos])<<8 | uint16(section[pos+1])
		pid := uint16(section[pos+2]&0x1F)<<8 | uint16(section[pos+3])
		if programNumber == 0 {
			continue // NIT PID
		}
		if _, exists := rd.programs[programNumber]; !exists {
			rd.programs[programNumber] = &ProgramInfo{
				ProgramNumber: programNumber,
				PMTPID:        pid,
				Version:       -1,
			}
			rd.pmtPIDs[pid] = true
		}
	}
}

// parsePMT 解析通过 CRC 校验的 PMT section，登记 PCR PID 与基本流。
func (rd *Reader) parsePMT(pid uint16, section []byte) {
	if section[0] != 0x02 {
		return
	}
	version := int(section[5]>>1) & 0x1F
	currentNext := section[5]&0x01 != 0
	if !currentNext {
		return
	}
	if stored, seen := rd.pmtVersion[pid]; seen && stored == version {
		return
	}
	rd.pmtVersion[pid] = version

	programNumber := uint16(section[3])<<8 | uint16(section[4])
	program, ok := rd.programs[programNumber]
	if !ok {
		program = &ProgramInfo{ProgramNumber: programNumber, PMTPID: pid, Version: -1}
		rd.programs[programNumber] = program
	}

	pcrPID := uint16(section[8]&0x1F)<<8 | uint16(section[9])
	programInfoLen := int(section[10]&0x0F)<<8 | int(section[11])

	sectionLength := int(section[1]&0x0F)<<8 | int(section[2])
	end := 3 + sectionLength - 4 // 排除 CRC

	streams := make([]StreamInfo, 0)
	pos := 12 + programInfoLen
	for pos+5 <= end {
		streamType := section[pos]
		esPID := uint16(section[pos+1]&0x1F)<<8 | uint16(section[pos+2])
		esInfoLen := int(section[pos+3]&0x0F)<<8 | int(section[pos+4])

		streams = append(streams, StreamInfo{
			ProgramNumber: programNumber,
			PID:           esPID,
			StreamType:    streamType,
		})
		rd.streams[esPID] = StreamInfo{
			ProgramNumber: programNumber,
			PID:           esPID,
			StreamType:    streamType,
		}
		pos += 5 + esInfoLen
	}

	program.PCRPID = pcrPID
	program.Version = version
	program.Streams = streams
}
