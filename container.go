// Package moc 实现单文件多对象容器。
//
// 文件布局:
//
//	[记录 0][记录 1]...[记录 N][索引块]
//
// 记录(数据区):
//
//	magic(8) | idLen(4) | id | version(8) | payloadLen(8) | sha256(32) | payload
//
// 索引块(文件尾部,记录每个对象的偏移/长度/摘要):
//
//	magic(8) | indexLen(8) | 索引负载 | indexLen(8) | magic(8)
//
// 发布协议(原子性):追加时先截断旧索引,写入新记录并 fsync,
// 再写入新索引块并 fsync。索引块是唯一的发布点:崩溃只会留下
// 无效尾部,未完成的记录永远不会进入已发布索引。
// 打开时优先读取尾部索引;索引缺失或损坏则从头扫描数据区重建,
// 并通过 Source() 标明索引来源(SourceIndex / SourceScan)。
package moc

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
)

const (
	recordMagic = "MOCR0001"
	indexMagic  = "MOCI0001"
	magicLen    = 8
	maxIDLen    = 256
	// indexOverhead = 头 magic + 头长度 + 尾长度 + 尾 magic
	indexOverhead = 2*magicLen + 2*8
)

var (
	// ErrNotFound 表示容器中没有该 ID。
	ErrNotFound = errors.New("moc: 对象不存在")
	// ErrNoVersion 表示该 ID 存在但无此版本。
	ErrNoVersion = errors.New("moc: 版本不存在")
)

// Entry 是索引中一个对象版本的物理位置与摘要。
type Entry struct {
	Version uint64
	Offset  int64 // payload 在文件中的偏移
	Length  int64 // payload 长度
	Digest  [32]byte
}

// Source 标明当前索引的来源。
type Source int

const (
	// SourceIndex 表示索引来自文件尾部的已发布索引块。
	SourceIndex Source = iota
	// SourceScan 表示尾部索引缺失或损坏,索引由扫描数据区重建。
	SourceScan
)

func (s Source) String() string {
	if s == SourceScan {
		return "scan-rebuilt"
	}
	return "tail-index"
}

// Container 是一个打开的多对象容器。
type Container struct {
	mu      sync.Mutex
	path    string
	index   map[string][]Entry // id -> 按版本升序
	source  Source
	dataEnd int64 // 已发布数据区的末尾偏移(索引块起点)
}

// Create 创建空容器,并立即写入空的已发布索引。
func Create(path string) (*Container, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := &Container{path: path, index: map[string][]Entry{}, source: SourceIndex}
	block, err := c.marshalIndexBlock()
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteAt(block, 0); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	return c, nil
}

// Open 打开已有容器。优先加载尾部索引;索引缺失、损坏或越界时
// 扫描数据区重建,并将 Source 标记为 SourceScan。
func Open(path string) (*Container, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if idx, dataEnd, ok := readTailIndex(path, info.Size()); ok {
		return &Container{path: path, index: idx, source: SourceIndex, dataEnd: dataEnd}, nil
	}
	idx, dataEnd, err := scanRecords(path, info.Size())
	if err != nil {
		return nil, err
	}
	return &Container{path: path, index: idx, source: SourceScan, dataEnd: dataEnd}, nil
}

// Source 返回当前索引来源(尾部索引 / 扫描重建)。
func (c *Container) Source() Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.source
}

// Append 追加一个对象版本并原子发布。同一 ID 重复追加时版本号递增。
// 返回本次发布的版本号。
func (c *Container) Append(id string, payload []byte) (uint64, error) {
	if len(id) == 0 || len(id) > maxIDLen {
		return 0, fmt.Errorf("moc: id 长度须在 1..%d 字节之间", maxIDLen)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	version := uint64(1)
	if es := c.index[id]; len(es) > 0 {
		version = es[len(es)-1].Version + 1
	}
	digest := sha256.Sum256(payload)

	f, err := os.OpenFile(c.path, os.O_RDWR, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// 1) 截断旧索引块,回到数据区末尾。
	if err := f.Truncate(c.dataEnd); err != nil {
		return 0, err
	}
	// 2) 写入新记录。
	var hdr bytes.Buffer
	hdr.WriteString(recordMagic)
	writeUint32(&hdr, uint32(len(id)))
	hdr.WriteString(id)
	var b8 [8]byte
	binary.BigEndian.PutUint64(b8[:], version)
	hdr.Write(b8[:])
	binary.BigEndian.PutUint64(b8[:], uint64(len(payload)))
	hdr.Write(b8[:])
	hdr.Write(digest[:])
	if _, err := f.WriteAt(hdr.Bytes(), c.dataEnd); err != nil {
		return 0, err
	}
	payloadOff := c.dataEnd + int64(hdr.Len())
	if _, err := f.WriteAt(payload, payloadOff); err != nil {
		return 0, err
	}
	// 3) 数据落盘后再写索引(发布点)。
	if err := f.Sync(); err != nil {
		return 0, err
	}
	newEnd := payloadOff + int64(len(payload))
	entry := Entry{Version: version, Offset: payloadOff, Length: int64(len(payload)), Digest: digest}
	newIndex := make(map[string][]Entry, len(c.index)+1)
	for k, v := range c.index {
		newIndex[k] = v
	}
	newIndex[id] = append(append([]Entry(nil), c.index[id]...), entry)
	c.index = newIndex
	block, err := c.marshalIndexBlock()
	if err != nil {
		return 0, err
	}
	if _, err := f.WriteAt(block, newEnd); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	c.dataEnd = newEnd
	c.source = SourceIndex
	return version, nil
}

// Get 读取 ID 的最新版本,返回前校验摘要。
func (c *Container) Get(id string) ([]byte, error) {
	c.mu.Lock()
	es := c.index[id]
	c.mu.Unlock()
	if len(es) == 0 {
		return nil, ErrNotFound
	}
	return c.readEntry(es[len(es)-1])
}

// GetVersion 读取 ID 的指定版本。版本不存在时返回 ErrNoVersion。
func (c *Container) GetVersion(id string, version uint64) ([]byte, error) {
	c.mu.Lock()
	es := c.index[id]
	c.mu.Unlock()
	if len(es) == 0 {
		return nil, ErrNotFound
	}
	for _, e := range es {
		if e.Version == version {
			return c.readEntry(e)
		}
	}
	return nil, ErrNoVersion
}

// Versions 返回 ID 的全部已发布版本(升序)。
func (c *Container) Versions(id string) []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	es := c.index[id]
	out := make([]uint64, len(es))
	for i, e := range es {
		out[i] = e.Version
	}
	return out
}

// IDs 返回容器中全部对象 ID(排序后)。
func (c *Container) IDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.index))
	for id := range c.index {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// readEntry 按索引项读取 payload。读取前做越界检查,返回前校验摘要。
func (c *Container) readEntry(e Entry) ([]byte, error) {
	c.mu.Lock()
	dataEnd := c.dataEnd
	c.mu.Unlock()
	if e.Offset < 0 || e.Length < 0 || e.Offset+e.Length > dataEnd {
		return nil, fmt.Errorf("moc: 索引项越界 (offset=%d length=%d dataEnd=%d)", e.Offset, e.Length, dataEnd)
	}
	f, err := os.Open(c.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// e.Length 已验证 <= dataEnd <= 文件大小,分配安全。
	buf := make([]byte, e.Length)
	if _, err := f.ReadAt(buf, e.Offset); err != nil {
		return nil, err
	}
	if sha256.Sum256(buf) != e.Digest {
		return nil, errors.New("moc: 摘要校验失败,数据已损坏")
	}
	return buf, nil
}

// marshalIndexBlock 将当前索引编码为完整索引块。
func (c *Container) marshalIndexBlock() ([]byte, error) {
	m := make(map[string]interface{}, len(c.index))
	for id, es := range c.index {
		arr := make([]interface{}, len(es))
		for i, e := range es {
			arr[i] = map[string]interface{}{
				"version": int64(e.Version),
				"offset":  e.Offset,
				"length":  e.Length,
				"digest":  e.Digest[:],
			}
		}
		m[id] = arr
	}
	payload, err := Marshal(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(indexMagic)
	var b8 [8]byte
	binary.BigEndian.PutUint64(b8[:], uint64(len(payload)))
	buf.Write(b8[:])
	buf.Write(payload)
	buf.Write(b8[:]) // 尾部重复长度,便于从 EOF 定位
	buf.WriteString(indexMagic)
	return buf.Bytes(), nil
}

// readTailIndex 从文件尾部读取已发布索引。任何一步校验失败都返回 ok=false。
func readTailIndex(path string, size int64) (map[string][]Entry, int64, bool) {
	if size < indexOverhead {
		return nil, 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false
	}
	defer f.Close()

	// 从 EOF 定位:尾部长度(8) + 尾部 magic(8)。
	tail := make([]byte, 16)
	if _, err := f.ReadAt(tail, size-16); err != nil {
		return nil, 0, false
	}
	if string(tail[8:]) != indexMagic {
		return nil, 0, false
	}
	indexLen := binary.BigEndian.Uint64(tail[:8])
	if indexLen > uint64(size)-indexOverhead {
		return nil, 0, false
	}
	dataEnd := size - int64(indexLen) - indexOverhead

	// 校验头部 magic 与长度。
	head := make([]byte, 16)
	if _, err := f.ReadAt(head, dataEnd); err != nil {
		return nil, 0, false
	}
	if string(head[:8]) != indexMagic || binary.BigEndian.Uint64(head[8:]) != indexLen {
		return nil, 0, false
	}
	payload := make([]byte, indexLen)
	if _, err := f.ReadAt(payload, dataEnd+16); err != nil {
		return nil, 0, false
	}
	v, err := Unmarshal(payload)
	if err != nil {
		return nil, 0, false
	}
	idx, ok := decodeIndex(v, dataEnd)
	if !ok {
		return nil, 0, false
	}
	return idx, dataEnd, true
}

// decodeIndex 把解码后的通用值转换为索引,并验证每一项:
// 版本 >= 1、同 ID 版本严格递增、偏移与长度不越出数据区、摘要 32 字节。
func decodeIndex(v interface{}, dataEnd int64) (map[string][]Entry, bool) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, false
	}
	idx := make(map[string][]Entry, len(m))
	for id, raw := range m {
		if len(id) == 0 || len(id) > maxIDLen {
			return nil, false
		}
		arr, ok := raw.([]interface{})
		if !ok {
			return nil, false
		}
		es := make([]Entry, 0, len(arr))
		var prev uint64
		for _, item := range arr {
			em, ok := item.(map[string]interface{})
			if !ok {
				return nil, false
			}
			version, ok1 := em["version"].(int64)
			offset, ok2 := em["offset"].(int64)
			length, ok3 := em["length"].(int64)
			digest, ok4 := em["digest"].([]byte)
			if !ok1 || !ok2 || !ok3 || !ok4 || len(digest) != 32 {
				return nil, false
			}
			// 越界拒绝:版本必须递增,区间必须落在已发布数据区内。
			if version < 1 || uint64(version) <= prev {
				return nil, false
			}
			if offset < 0 || length < 0 || offset+length > dataEnd {
				return nil, false
			}
			prev = uint64(version)
			var d [32]byte
			copy(d[:], digest)
			es = append(es, Entry{Version: uint64(version), Offset: offset, Length: length, Digest: d})
		}
		idx[id] = es
	}
	return idx, true
}

// scanRecords 从头扫描数据区重建索引。遇到损坏或不完整的记录即停止,
// 该记录及其后的内容不进入索引(未完成尾部不发布)。
func scanRecords(path string, size int64) (map[string][]Entry, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	idx := map[string][]Entry{}
	pos := int64(0)
	for {
		entry, id, next, ok := readRecord(f, pos, size)
		if !ok {
			break
		}
		idx[id] = append(idx[id], entry)
		pos = next
	}
	// 同 ID 按版本升序;同版本保留后出现者(后写覆盖)。
	for id, es := range idx {
		sort.SliceStable(es, func(i, j int) bool { return es[i].Version < es[j].Version })
		out := es[:0]
		for _, e := range es {
			if n := len(out); n > 0 && out[n-1].Version == e.Version {
				out[n-1] = e
			} else {
				out = append(out, e)
			}
		}
		idx[id] = out
	}
	return idx, pos, nil
}

// readRecord 读取并校验 pos 处的一条记录。返回 ok=false 表示记录
// 不完整或损坏(包括未完成写入的尾部),调用方应停止扫描。
func readRecord(f *os.File, pos, size int64) (Entry, string, int64, bool) {
	var zero Entry
	head := make([]byte, magicLen+4)
	if _, err := f.ReadAt(head, pos); err != nil {
		return zero, "", 0, false
	}
	if string(head[:magicLen]) != recordMagic {
		return zero, "", 0, false
	}
	idLen := binary.BigEndian.Uint32(head[magicLen:])
	if idLen == 0 || idLen > maxIDLen {
		return zero, "", 0, false
	}
	// 固定头部:id + version + payloadLen + digest
	fixedLen := int64(idLen) + 8 + 8 + 32
	if pos+magicLen+4+fixedLen > size {
		return zero, "", 0, false
	}
	fixed := make([]byte, fixedLen)
	if _, err := f.ReadAt(fixed, pos+magicLen+4); err != nil {
		return zero, "", 0, false
	}
	id := string(fixed[:idLen])
	rest := fixed[idLen:]
	version := binary.BigEndian.Uint64(rest[:8])
	payloadLen := binary.BigEndian.Uint64(rest[8:16])
	var digest [32]byte
	copy(digest[:], rest[16:48])
	if version < 1 {
		return zero, "", 0, false
	}
	payloadOff := pos + magicLen + 4 + fixedLen
	// 长度先验证:payload 必须完整落在文件内,否则视为未完成尾部。
	if payloadLen > uint64(size) || payloadOff+int64(payloadLen) > size {
		return zero, "", 0, false
	}
	payload := make([]byte, payloadLen)
	if _, err := f.ReadAt(payload, payloadOff); err != nil {
		return zero, "", 0, false
	}
	if sha256.Sum256(payload) != digest {
		return zero, "", 0, false
	}
	entry := Entry{Version: version, Offset: payloadOff, Length: int64(payloadLen), Digest: digest}
	return entry, id, payloadOff + int64(payloadLen), true
}
