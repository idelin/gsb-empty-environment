package waldb

// LogManager 负责追加日志记录、分配严格递增的 LSN（即文件偏移），
// 并按 WAL 规则把缓冲的记录刷到持久存储。
type LogManager struct {
	file       *InjectedFile
	log        []byte // 本次会话的完整日志映像（含未刷缓冲）
	flushedLSN int64  // 小于该 LSN 的记录均已持久化
	hook       func(CrashPoint)
}

// openLogManager 从文件重建日志管理器：扫描到最后一条完整记录，
// 截断不完整/损坏的尾部（撕裂写），新记录从截断处继续追加。
func openLogManager(file *InjectedFile, hook func(CrashPoint)) *LogManager {
	data := file.Bytes()
	off := int64(0)
	for off < int64(len(data)) {
		_, next, err := decodeAt(data, off)
		if err != nil {
			break
		}
		off = next
	}
	file.Truncate(off)
	file.Sync()
	return &LogManager{
		file:       file,
		log:        append([]byte(nil), data[:off]...),
		flushedLSN: off,
		hook:       hook,
	}
}

// Append 追加一条记录并返回其 LSN。记录此时只在内存缓冲中，
// 需 Flush 后才持久。
func (lm *LogManager) Append(r *LogRecord) int64 {
	r.LSN = int64(len(lm.log))
	enc := r.encode()
	lm.log = append(lm.log, enc...)
	lm.fire(CutLogBuffered)
	return r.LSN
}

// Flush 保证 LSN <= upto 的记录全部持久化（实际会刷出缓冲中的全部记录）。
func (lm *LogManager) Flush(upto int64) {
	if upto < lm.flushedLSN {
		return
	}
	if int64(len(lm.log)) > lm.flushedLSN {
		lm.file.WriteAt(lm.log[lm.flushedLSN:], lm.flushedLSN)
	}
	lm.file.Sync()
	lm.flushedLSN = int64(len(lm.log))
}

// ReadRecord 按 LSN 随机读取一条记录（撤销时沿 prevLSN/undoNextLSN 链走）。
func (lm *LogManager) ReadRecord(lsn int64) *LogRecord {
	r, _, err := decodeAt(lm.log, lsn)
	if err != nil {
		panic(err)
	}
	return r
}

func (lm *LogManager) endLSN() int64 { return int64(len(lm.log)) }

func (lm *LogManager) fire(p CrashPoint) {
	if lm.hook != nil {
		lm.hook(p)
	}
}
