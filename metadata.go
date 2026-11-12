package netsec

import (
	"crypto/sha1"
	"errors"
	"strconv"
	"sync"
)

// PieceSize 是 BEP 9 规定的 ut_metadata 分片大小（16 KiB）。
const PieceSize = 16384

// ut_metadata 消息的 msg_type 取值（BEP 9）。
const (
	MsgRequest = 0
	MsgData    = 1
	MsgReject  = 2
)

// PeerKey 标识一个对端，具体语义由调用方的对端表决定（通常是 IP:Port）。
type PeerKey string

var (
	// ErrMetadataTooLarge 表示扩展握手里的 metadata_size 超过调用方上限。
	ErrMetadataTooLarge = errors.New("netsec: metadata_size exceeds caller limit")
	// ErrInvalidMetadataSize 表示 metadata_size 非法（负值或缺失）。
	ErrInvalidMetadataSize = errors.New("netsec: invalid metadata_size")
	// ErrPieceOutOfRange 表示分片序号超出 ceil(metadata_size/PieceSize)-1。
	ErrPieceOutOfRange = errors.New("netsec: ut_metadata piece index out of range")
	// ErrPieceLength 表示分片长度与应得长度不符（末片必须精确）。
	ErrPieceLength = errors.New("netsec: ut_metadata piece length mismatch")
	// ErrDuplicatePiece 表示重复分片的内容与首次收到的不一致。
	ErrDuplicatePiece = errors.New("netsec: conflicting duplicate ut_metadata piece")
	// ErrHashMismatch 表示收齐后 SHA-1 与 info-hash 不一致。
	ErrHashMismatch = errors.New("netsec: metadata SHA-1 does not match info-hash")
	// ErrExchangeFailed 表示该对端的交换已因先前错误终止。
	ErrExchangeFailed = errors.New("netsec: metadata exchange already failed")
	// ErrExchangeComplete 表示元数据已收齐，多余分片被忽略。
	ErrExchangeComplete = errors.New("netsec: metadata exchange already complete")
	// ErrMalformedMessage 表示扩展消息不是合法的 ut_metadata 消息。
	ErrMalformedMessage = errors.New("netsec: malformed ut_metadata message")
)

// pieceCount 返回 ceil(size/PieceSize)，全程用 int64 算术避免溢出。
func pieceCount(size int64) int64 {
	if size <= 0 {
		return 0
	}
	return (size + PieceSize - 1) / PieceSize
}

// expectedPieceLength 返回给定序号分片应有的精确字节数。
func expectedPieceLength(size, index int64) int64 {
	remaining := size - index*PieceSize
	if remaining < 0 {
		return 0
	}
	if remaining > PieceSize {
		return PieceSize
	}
	return remaining
}

// BanBook 按对端累计交换失败次数并在达到阈值时判定封禁。并发安全。
type BanBook struct {
	mu        sync.Mutex
	threshold int
	strikes   map[PeerKey]int
	banned    map[PeerKey]struct{}
}

// NewBanBook 创建封禁簿；threshold 小于 1 时按 1 处理，即一次失败即封禁。
func NewBanBook(threshold int) *BanBook {
	if threshold < 1 {
		threshold = 1
	}
	return &BanBook{
		threshold: threshold,
		strikes:   make(map[PeerKey]int),
		banned:    make(map[PeerKey]struct{}),
	}
}

// Strike 记录一次失败并返回该对端当前的失败次数；达到阈值即加入封禁集合。
func (b *BanBook) Strike(peer PeerKey) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.strikes[peer]++
	if b.strikes[peer] >= b.threshold {
		b.banned[peer] = struct{}{}
	}
	return b.strikes[peer]
}

// Banned 报告对端是否已达到封禁阈值。
func (b *BanBook) Banned(peer PeerKey) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.banned[peer]
	return ok
}

// Strikes 返回对端累计失败次数。
func (b *BanBook) Strikes(peer PeerKey) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.strikes[peer]
}

// Reset 清除对端的失败计数与封禁状态。
func (b *BanBook) Reset(peer PeerKey) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.strikes, peer)
	delete(b.banned, peer)
}

// Exchange 维护单个对端一次 ut_metadata 交换的校验状态。
// 每个对端持有独立实例，方法内部加锁，可直接由对端消息循环调用。
type Exchange struct {
	mu       sync.Mutex
	peer     PeerKey
	book     *BanBook
	infoHash [sha1.Size]byte
	maxSize  int64
	size     int64
	pieces   [][]byte
	received int
	done     bool
	failed   bool
	metadata []byte
	err      error
}

// NewExchange 为对端创建交换状态。
//
// metadataSize 取自扩展握手，maxSize 是调用方可接受的尺寸上限，
// infoHash 是该种子的 info-hash，用于收齐后的 SHA-1 校验。
// 尺寸非法或超过上限时立即记一次失败并返回包装了对应哨兵错误的 error，
// 返回的 Exchange 仍处于 failed 状态，后续分片一律拒绝。
func NewExchange(peer PeerKey, book *BanBook, metadataSize, maxSize int64, infoHash [sha1.Size]byte) (*Exchange, error) {
	ex := &Exchange{
		peer:     peer,
		book:     book,
		infoHash: infoHash,
		maxSize:  maxSize,
	}
	if metadataSize < 0 {
		return ex, ex.fail(ErrInvalidMetadataSize)
	}
	ex.size = metadataSize
	if maxSize >= 0 && metadataSize > maxSize {
		return ex, ex.fail(ErrMetadataTooLarge)
	}
	count := pieceCount(metadataSize)
	ex.pieces = make([][]byte, count)
	return ex, nil
}

// Size 返回扩展握手声明的 metadata_size。
func (ex *Exchange) Size() int64 {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.size
}

// Done 报告元数据是否已通过全部校验并收齐。
func (ex *Exchange) Done() bool {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.done
}

// Failed 报告该对端是否已触发过校验失败。
func (ex *Exchange) Failed() bool {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.failed
}

// Metadata 返回收齐并校验通过的 info 字典；未完成时返回 nil。
func (ex *Exchange) Metadata() []byte {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.metadata
}

// HandleDataPiece 处理一个 ut_metadata data 分片（调用方已剥掉扩展消息 ID 字节，
// 并把 bencode 头与分片负载拆分好）。任何校验失败都会给对端记一次封禁计数。
//
// 返回 complete=true 表示全部收齐且 SHA-1 与 info-hash 一致。
func (ex *Exchange) HandleDataPiece(index int64, piece []byte) (complete bool, err error) {
	ex.mu.Lock()
	defer ex.mu.Unlock()

	if ex.failed {
		return false, ErrExchangeFailed
	}
	if ex.done {
		return true, ErrExchangeComplete
	}

	count := pieceCount(ex.size)
	if index < 0 || index >= count {
		return false, ex.failLocked(ErrPieceOutOfRange)
	}

	if want := expectedPieceLength(ex.size, index); int64(len(piece)) != want {
		return false, ex.failLocked(ErrPieceLength)
	}

	if existing := ex.pieces[index]; existing != nil {
		if !bytesEqual(existing, piece) {
			return false, ex.failLocked(ErrDuplicatePiece)
		}
		// 内容一致的重复分片幂等忽略，不计失败。
		return ex.done, nil
	}

	copied := make([]byte, len(piece))
	copy(copied, piece)
	ex.pieces[index] = copied
	ex.received++

	if int64(ex.received) < count {
		return false, nil
	}

	metadata := make([]byte, 0, ex.size)
	for _, part := range ex.pieces {
		metadata = append(metadata, part...)
	}
	if int64(len(metadata)) != ex.size {
		return false, ex.failLocked(ErrPieceLength)
	}
	if sha1.Sum(metadata) != ex.infoHash {
		return false, ex.failLocked(ErrHashMismatch)
	}

	ex.metadata = metadata
	ex.done = true
	return true, nil
}

// HandleDataMessage 处理完整的 ut_metadata data 扩展消息负载，
// 即去掉扩展消息 ID 字节之后的全部内容（bencode 字典 + 末尾分片字节）。
func (ex *Exchange) HandleDataMessage(payload []byte) (complete bool, err error) {
	dict, trailing, err := parseDataMessage(payload)
	if err != nil {
		return false, ex.fail(err)
	}
	msgType, present, err := dictInt(dict, "msg_type")
	if err != nil || !present || msgType != MsgData {
		return false, ex.fail(ErrMalformedMessage)
	}
	index, present, err := dictInt(dict, "piece")
	if err != nil || !present || index < 0 {
		return false, ex.fail(ErrMalformedMessage)
	}
	totalSize, present, err := dictInt(dict, "total_size")
	if err != nil {
		return false, ex.fail(ErrMalformedMessage)
	}
	if present {
		if totalSize != ex.size {
			return false, ex.fail(ErrInvalidMetadataSize)
		}
	}
	return ex.HandleDataPiece(index, trailing)
}

// fail 在无锁路径（构造函数）使用，内部自行加锁。
func (ex *Exchange) fail(cause error) error {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.failLocked(cause)
}

// failLocked 标记交换失败并给对端累计一次封禁计数。调用时必须持有 ex.mu。
func (ex *Exchange) failLocked(cause error) error {
	ex.failed = true
	ex.err = cause
	if ex.book != nil {
		ex.book.Strike(ex.peer)
	}
	return cause
}

func bytesEqual(a, b []byte) bool {
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

// parseDataMessage 解析 data 消息并返回字典与 bencode 头之后的分片负载。
func parseDataMessage(payload []byte) (bDict, []byte, error) {
	value, rest, err := decodeBencodeValue(payload, 0)
	if err != nil {
		return nil, nil, err
	}
	dict, ok := value.(bDict)
	if !ok {
		return nil, nil, ErrMalformedMessage
	}
	return dict, rest, nil
}

// ParseExtendedHandshake 从扩展握手负载中取 metadata_size。
// payload 为去掉扩展消息 ID 字节后的 bencode 字典。
func ParseExtendedHandshake(payload []byte) (int64, error) {
	value, err := decodeBencode(payload)
	if err != nil {
		return 0, err
	}
	dict, ok := value.(bDict)
	if !ok {
		return 0, ErrMalformedMessage
	}
	raw, ok := dictLookup(dict, "metadata_size")
	if !ok {
		return 0, ErrInvalidMetadataSize
	}
	size, ok := raw.(bInt)
	if !ok || size < 0 {
		return 0, ErrInvalidMetadataSize
	}
	return int64(size), nil
}

// BuildRequest 构造发给对端的 ut_metadata request 消息负载（不含扩展 ID 字节）。
func BuildRequest(index int64) ([]byte, error) {
	if index < 0 {
		return nil, ErrPieceOutOfRange
	}
	return []byte("d8:msg_typei0e5:piecei" + strconv.FormatInt(index, 10) + "ee"), nil
}
