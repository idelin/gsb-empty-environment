package netsec

import (
	"crypto/sha1"
	"errors"
	"strconv"
	"sync"
	"testing"
)

// buildDataMessage 生成 ut_metadata data 消息负载：
// d8:msg_typei1e5:piecei<index>e10:total_sizei<size>ee + 分片字节。
func buildDataMessage(index, totalSize int64, piece []byte) []byte {
	header := "d8:msg_typei1e5:piecei" + strconv.FormatInt(index, 10) +
		"e10:total_sizei" + strconv.FormatInt(totalSize, 10) + "ee"
	return append([]byte(header), piece...)
}

func buildHandshake(metadataSize int64) []byte {
	return []byte("d13:metadata_sizei" + strconv.FormatInt(metadataSize, 10) + "ee")
}

func newTestMetadata(size int) []byte {
	metadata := make([]byte, size)
	for i := range metadata {
		metadata[i] = byte(i*7 + 3)
	}
	return metadata
}

func splitPieces(metadata []byte) [][]byte {
	var pieces [][]byte
	for offset := 0; offset < len(metadata); offset += PieceSize {
		end := offset + PieceSize
		if end > len(metadata) {
			end = len(metadata)
		}
		pieces = append(pieces, metadata[offset:end])
	}
	return pieces
}

// StrikesForTest 便于测试读取当前对端在封禁簿上的计数。
func (ex *Exchange) StrikesForTest() int {
	return ex.book.Strikes(ex.peer)
}

func TestInflatedMetadataSizeRejected(t *testing.T) {
	book := NewBanBook(1)
	peer := PeerKey("10.0.0.1:51413")
	infoHash := sha1.Sum(newTestMetadata(10))

	ex, err := NewExchange(peer, book, 1<<30, 1<<20, infoHash)
	if !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("expected ErrMetadataTooLarge, got %v", err)
	}
	if !ex.Failed() {
		t.Fatal("exchange should be marked failed after oversize declaration")
	}
	if !book.Banned(peer) || book.Strikes(peer) != 1 {
		t.Fatalf("peer must be banned, strikes=%d banned=%v", book.Strikes(peer), book.Banned(peer))
	}

	// 失败后继续投递分片必须被拒绝，且不得重复计数。
	if _, err := ex.HandleDataPiece(0, make([]byte, PieceSize)); !errors.Is(err, ErrExchangeFailed) {
		t.Fatalf("expected ErrExchangeFailed, got %v", err)
	}
	if book.Strikes(peer) != 1 {
		t.Fatalf("failed exchange must not accumulate extra strikes, got %d", book.Strikes(peer))
	}
}

func TestNegativeAndParsedMetadataSize(t *testing.T) {
	book := NewBanBook(1)
	peer := PeerKey("10.0.0.2:51413")
	infoHash := sha1.Sum(nil)

	if _, err := NewExchange(peer, book, -1, 100, infoHash); !errors.Is(err, ErrInvalidMetadataSize) {
		t.Fatalf("negative size: expected ErrInvalidMetadataSize, got %v", err)
	}
	if !book.Banned(peer) {
		t.Fatal("negative size must count as a ban strike")
	}

	size, err := ParseExtendedHandshake(buildHandshake(32768))
	if err != nil || size != 32768 {
		t.Fatalf("handshake parse: size=%d err=%v", size, err)
	}
	if _, err := ParseExtendedHandshake([]byte("i32768e")); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("non-dict handshake: expected ErrMalformedMessage, got %v", err)
	}
}

func TestPieceIndexAndLastPieceLength(t *testing.T) {
	metadata := newTestMetadata(40000) // 3 片：16384, 16384, 7232
	infoHash := sha1.Sum(metadata)
	book := NewBanBook(2)
	peer := PeerKey("10.0.0.3:51413")

	ex, err := NewExchange(peer, book, int64(len(metadata)), 1<<20, infoHash)
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}

	// 越界序号：合法区间是 0..ceil(40000/16384)-1 = 0..2。
	if _, err := ex.HandleDataPiece(3, make([]byte, PieceSize)); !errors.Is(err, ErrPieceOutOfRange) {
		t.Fatalf("index 3: expected ErrPieceOutOfRange, got %v", err)
	}
	if ex.StrikesForTest() != 1 {
		t.Fatalf("expected 1 strike, got %d", ex.StrikesForTest())
	}

	// 负序号用独立对端验证，避免被上一次失败后的 failed 状态短路。
	peerNeg := PeerKey("10.0.0.33:51413")
	exNeg, _ := NewExchange(peerNeg, book, int64(len(metadata)), 1<<20, infoHash)
	if _, err := exNeg.HandleDataPiece(-1, nil); !errors.Is(err, ErrPieceOutOfRange) {
		t.Fatalf("index -1: expected ErrPieceOutOfRange, got %v", err)
	}

	// 新对端：末片长度必须精确（7232），多一个字节都不行。
	peer2 := PeerKey("10.0.0.4:51413")
	ex2, _ := NewExchange(peer2, book, int64(len(metadata)), 1<<20, infoHash)
	if _, err := ex2.HandleDataPiece(2, make([]byte, 7233)); !errors.Is(err, ErrPieceLength) {
		t.Fatalf("oversized last piece: expected ErrPieceLength, got %v", err)
	}

	// 首片长度也必须精确（16384），独立对端验证。
	peer3 := PeerKey("10.0.0.44:51413")
	ex3, _ := NewExchange(peer3, book, int64(len(metadata)), 1<<20, infoHash)
	if _, err := ex3.HandleDataPiece(0, make([]byte, PieceSize-1)); !errors.Is(err, ErrPieceLength) {
		t.Fatalf("short first piece: expected ErrPieceLength, got %v", err)
	}
}

func TestDuplicatePieces(t *testing.T) {
	metadata := newTestMetadata(2 * PieceSize)
	infoHash := sha1.Sum(metadata)
	book := NewBanBook(1)
	peer := PeerKey("10.0.0.5:51413")
	ex, _ := NewExchange(peer, book, int64(len(metadata)), 1<<20, infoHash)

	first := metadata[:PieceSize]
	if _, err := ex.HandleDataPiece(0, first); err != nil {
		t.Fatalf("first piece: %v", err)
	}
	// 内容一致的重复分片幂等忽略。
	duplicate := make([]byte, PieceSize)
	copy(duplicate, first)
	if complete, err := ex.HandleDataPiece(0, duplicate); err != nil || complete {
		t.Fatalf("identical duplicate should be ignored, complete=%v err=%v", complete, err)
	}
	if book.Strikes(peer) != 0 {
		t.Fatalf("identical duplicate must not count as failure, got %d", book.Strikes(peer))
	}

	// 内容冲突的重复分片视为投毒，立即失败并封禁。
	conflicting := make([]byte, PieceSize)
	copy(conflicting, first)
	conflicting[0] ^= 0xFF
	if _, err := ex.HandleDataPiece(0, conflicting); !errors.Is(err, ErrDuplicatePiece) {
		t.Fatalf("expected ErrDuplicatePiece, got %v", err)
	}
	if !book.Banned(peer) {
		t.Fatal("conflicting duplicate must ban the peer")
	}
}

func TestHashMismatchBansPeer(t *testing.T) {
	metadata := newTestMetadata(2 * PieceSize)
	wrongHash := sha1.Sum(append([]byte("not-the-info"), make([]byte, 12)...))
	book := NewBanBook(1)
	peer := PeerKey("10.0.0.6:51413")
	ex, _ := NewExchange(peer, book, int64(len(metadata)), 1<<20, wrongHash)

	pieces := splitPieces(metadata)
	for index, piece := range pieces {
		complete, err := ex.HandleDataPiece(int64(index), piece)
		if index == len(pieces)-1 {
			if !errors.Is(err, ErrHashMismatch) || complete {
				t.Fatalf("expected ErrHashMismatch, got complete=%v err=%v", complete, err)
			}
		} else if err != nil {
			t.Fatalf("piece %d: %v", index, err)
		}
	}
	if !book.Banned(peer) || ex.Metadata() != nil {
		t.Fatalf("hash mismatch must ban peer and withhold metadata, banned=%v", book.Banned(peer))
	}
}

func TestHappyPathAssemblesAndVerifies(t *testing.T) {
	metadata := newTestMetadata(40000)
	infoHash := sha1.Sum(metadata)
	book := NewBanBook(1)
	peer := PeerKey("10.0.0.7:51413")
	ex, _ := NewExchange(peer, book, int64(len(metadata)), 1<<20, infoHash)

	pieces := splitPieces(metadata)
	for index, piece := range pieces {
		payload := buildDataMessage(int64(index), int64(len(metadata)), piece)
		complete, err := ex.HandleDataMessage(payload)
		if err != nil {
			t.Fatalf("data message piece %d: %v", index, err)
		}
		wantComplete := index == len(pieces)-1
		if complete != wantComplete {
			t.Fatalf("piece %d complete=%v want %v", index, complete, wantComplete)
		}
	}

	if !ex.Done() || !bytesEqual(ex.Metadata(), metadata) {
		t.Fatal("assembled metadata must equal the original bytes")
	}
	if book.Strikes(peer) != 0 {
		t.Fatal("successful exchange must record no strikes")
	}

	// 收齐后多余分片被忽略，不产生新失败。
	if _, err := ex.HandleDataMessage(buildDataMessage(0, int64(len(metadata)), pieces[0])); !errors.Is(err, ErrExchangeComplete) {
		t.Fatalf("expected ErrExchangeComplete, got %v", err)
	}
}

func TestMessageLevelValidation(t *testing.T) {
	metadata := newTestMetadata(PieceSize)
	infoHash := sha1.Sum(metadata)
	book := NewBanBook(1)

	// total_size 与握手尺寸不一致。
	peer := PeerKey("10.0.0.8:51413")
	ex, _ := NewExchange(peer, book, int64(len(metadata)), 1<<20, infoHash)
	if _, err := ex.HandleDataMessage(buildDataMessage(0, PieceSize+1, metadata)); !errors.Is(err, ErrInvalidMetadataSize) {
		t.Fatalf("total_size drift: expected ErrInvalidMetadataSize, got %v", err)
	}

	// 非 bencode 负载。
	peer2 := PeerKey("10.0.0.9:51413")
	ex2, _ := NewExchange(peer2, book, int64(len(metadata)), 1<<20, infoHash)
	if _, err := ex2.HandleDataMessage([]byte("garbage")); err == nil {
		t.Fatal("malformed payload must return an error and strike the peer")
	}
	if !book.Banned(peer2) {
		t.Fatal("malformed message must ban peer at threshold 1")
	}

	// 缺 piece 字段。
	peer3 := PeerKey("10.0.0.10:51413")
	ex3, _ := NewExchange(peer3, book, int64(len(metadata)), 1<<20, infoHash)
	if _, err := ex3.HandleDataMessage([]byte("d8:msg_typei1ee")); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("missing piece: expected ErrMalformedMessage, got %v", err)
	}
}

func TestBanThresholdAndReset(t *testing.T) {
	book := NewBanBook(3)
	peer := PeerKey("10.0.0.11:51413")
	for strike := 1; strike <= 2; strike++ {
		book.Strike(peer)
		if book.Banned(peer) {
			t.Fatalf("peer must not be banned before threshold, strike=%d", strike)
		}
	}
	book.Strike(peer)
	if !book.Banned(peer) {
		t.Fatal("peer must be banned once threshold reached")
	}
	book.Reset(peer)
	if book.Banned(peer) || book.Strikes(peer) != 0 {
		t.Fatal("reset must clear strikes and ban state")
	}
}

func TestBuildRequest(t *testing.T) {
	request, err := BuildRequest(2)
	if err != nil {
		t.Fatal(err)
	}
	if want := "d8:msg_typei0e5:piecei2ee"; string(request) != want {
		t.Fatalf("got %q want %q", request, want)
	}
	if _, err := BuildRequest(-1); !errors.Is(err, ErrPieceOutOfRange) {
		t.Fatalf("negative request index: expected ErrPieceOutOfRange, got %v", err)
	}
}

func TestBencodeStrictness(t *testing.T) {
	invalid := [][]byte{
		[]byte("i01e"),           // 整数前导零
		[]byte("i-0e"),           // 负零
		[]byte("3:ab"),           // 截断字符串
		[]byte("d1:ai1e1:ai2ee"), // 重复字典键
		[]byte("d1:bi1e1:ai2ee"), // 键未排序
		[]byte("li1eeX"),         // 列表后尾随垃圾
	}
	for index, input := range invalid {
		if _, err := decodeBencode(input); err == nil {
			t.Fatalf("case %d (%s): expected decode error", index, input)
		}
	}
}

func TestConcurrentPiecesAreRaceFree(t *testing.T) {
	metadata := newTestMetadata(4 * PieceSize)
	infoHash := sha1.Sum(metadata)
	book := NewBanBook(1)
	ex, _ := NewExchange(PeerKey("10.0.0.12:51413"), book, int64(len(metadata)), 1<<20, infoHash)

	var wg sync.WaitGroup
	for index, piece := range splitPieces(metadata) {
		wg.Add(1)
		go func(i int, p []byte) {
			defer wg.Done()
			_, _ = ex.HandleDataPiece(int64(i), p)
		}(index, piece)
	}
	wg.Wait()

	if !ex.Done() || !bytesEqual(ex.Metadata(), metadata) {
		t.Fatal("concurrent delivery must still assemble verifiable metadata")
	}
}

func TestZeroSizeMetadataCompletesImmediately(t *testing.T) {
	infoHash := sha1.Sum(nil)
	book := NewBanBook(1)
	peer := PeerKey("10.0.0.13:51413")
	ex, err := NewExchange(peer, book, 0, 1<<20, infoHash)
	if err != nil {
		t.Fatalf("zero size must be accepted: %v", err)
	}

	// 没有任何合法分片：任何序号都越界。
	if _, err := ex.HandleDataPiece(0, nil); !errors.Is(err, ErrPieceOutOfRange) {
		t.Fatalf("zero-size exchange has no valid pieces, got %v", err)
	}
}
