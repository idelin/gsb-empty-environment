package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
)

// Block is one entry of the archive manifest. The archive is
// self-describing: every block carries its own summary (logical length and
// SHA-256 digest) alongside its payload.
type Block struct {
	ID     int    `json:"id"`
	Source string `json:"source"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"` // logical length, used as sampling weight
	SHA256 string `json:"sha256"`
	Data   []byte `json:"data"` // base64 in JSON encoding
}

// Archive is a manifest plus block payloads.
type Archive struct {
	Version int     `json:"version"`
	Blocks  []Block `json:"blocks"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CreateArchive builds an archive from the given files, splitting each into
// blocks of at most blockSize bytes. A block's logical length is its payload
// size.
func CreateArchive(paths []string, blockSize int64) (*Archive, error) {
	if blockSize <= 0 {
		return nil, fmt.Errorf("block size must be positive")
	}
	arc := &Archive{Version: 1}
	id := 0
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		offset := int64(0)
		for {
			buf := make([]byte, blockSize)
			n, rerr := io.ReadFull(f, buf)
			if n > 0 {
				data := buf[:n]
				arc.Blocks = append(arc.Blocks, Block{
					ID:     id,
					Source: path,
					Offset: offset,
					Length: int64(n),
					SHA256: digest(data),
					Data:   data,
				})
				id++
				offset += int64(n)
			}
			if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
				break
			}
			if rerr != nil {
				f.Close()
				return nil, rerr
			}
		}
		f.Close()
	}
	if len(arc.Blocks) == 0 {
		return nil, fmt.Errorf("no blocks: all inputs were empty")
	}
	return arc, nil
}

func SaveArchive(arc *Archive, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(arc)
}

func LoadArchive(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var arc Archive
	if err := json.NewDecoder(f).Decode(&arc); err != nil {
		return nil, err
	}
	if len(arc.Blocks) == 0 {
		return nil, fmt.Errorf("archive has no blocks")
	}
	return &arc, nil
}

// Weights returns the logical lengths of all blocks as big integers, so
// arbitrarily large totals are handled without overflow.
func (arc *Archive) Weights() []*big.Int {
	w := make([]*big.Int, len(arc.Blocks))
	for i, b := range arc.Blocks {
		w[i] = big.NewInt(b.Length)
	}
	return w
}

// TotalLength is the exact total logical length as a big integer.
func (arc *Archive) TotalLength() *big.Int {
	total := big.NewInt(0)
	for _, b := range arc.Blocks {
		total.Add(total, big.NewInt(b.Length))
	}
	return total
}

// VerifyBlock recomputes the digest of a block's payload and compares it
// with the digest recorded in the manifest.
func VerifyBlock(b *Block) bool {
	return digest(b.Data) == b.SHA256
}
