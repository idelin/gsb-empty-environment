package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"sync"
)

// entry 是磁盘目录项：随机 16 字节 entryNonce + AEAD 封装的原始名。
type entry struct {
	lookupKey     [20]byte
	generation    uint64
	entryNonce    [16]byte
	ciphertext    []byte
	canonicalHash [32]byte
	inodeID       uint64
	inodeGen      uint64
}

// Directory 是一个加密目录。策略与目录 nonce 创建后不可变，
// 轮换仅推进密钥代次并逐项事务迁移目录项。
type Directory struct {
	host    *Host
	policy  Policy
	current uint64
	gens    map[uint64]struct{}
	serial  uint64

	mu      sync.Mutex
	buckets map[[20]byte][]*entry
}

// Policy 返回目录冻结策略的副本。
func (d *Directory) Policy() Policy {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.policy
}

func (d *Directory) nameKey(gen uint64) []byte {
	return d.host.nameKey(d.policy.DirectoryID, gen, d.policy.DirectoryNonce)
}

// aeadFor 由名密钥导出 AEAD（AES-256-GCM）。
func (d *Directory) aeadFor(gen uint64) (cipher.AEAD, error) {
	seed := sha256.Sum256(append([]byte("netsec/aead/v1"), d.nameKey(gen)...))
	block, err := aes.NewCipher(seed[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// aad 构造附加数据：绑定目录策略摘要、规范名 SHA256、inodeID 与 inode 代次。
func (d *Directory) aad(gen uint64, canonHash [32]byte, inodeID, inodeGen uint64) []byte {
	pd := d.policy.digest(gen)
	buf := make([]byte, 0, 16+32+32+8+8)
	buf = append(buf, "netsec/aad/v1"...)
	buf = append(buf, pd[:]...)
	buf = append(buf, canonHash[:]...)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], inodeID)
	buf = append(buf, b[:]...)
	binary.BigEndian.PutUint64(b[:], inodeGen)
	buf = append(buf, b[:]...)
	return buf
}

// seal 用指定代次封装原始名，生成随机 entryNonce 的目录项。
func (d *Directory) seal(gen uint64, rawName string, canonHash [32]byte, inodeID, inodeGen uint64) (*entry, error) {
	aead, err := d.aeadFor(gen)
	if err != nil {
		return nil, err
	}
	e := &entry{
		generation:    gen,
		canonicalHash: canonHash,
		inodeID:       inodeID,
		inodeGen:      inodeGen,
	}
	if _, err := io.ReadFull(d.host.rand, e.entryNonce[:]); err != nil {
		return nil, err
	}
	e.ciphertext = aead.Seal(nil, e.entryNonce[:aead.NonceSize()], []byte(rawName),
		d.aad(gen, canonHash, inodeID, inodeGen))
	return e, nil
}

// open 解密目录项并核对附加数据中的策略摘要、规范名摘要与 inode 身份。
func (d *Directory) open(e *entry) (string, error) {
	aead, err := d.aeadFor(e.generation)
	if err != nil {
		return "", err
	}
	pt, err := aead.Open(nil, e.entryNonce[:aead.NonceSize()], e.ciphertext,
		d.aad(e.generation, e.canonicalHash, e.inodeID, e.inodeGen))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// prepare 校验名称并求规范名与其摘要。
func (d *Directory) prepare(name string) (string, [32]byte, error) {
	if err := ValidateName(name); err != nil {
		return "", [32]byte{}, err
	}
	if len(name) > d.policy.MaxRawNameBytes {
		return "", [32]byte{}, ErrNameTooLong
	}
	canonical := Canonicalize(name, d.policy.CaseMode)
	if len(canonical) > d.policy.MaxCanonicalNameBytes {
		return "", [32]byte{}, ErrNameTooLong
	}
	return canonical, sha256.Sum256([]byte(canonical)), nil
}

// conflictLocked 原子检查所有已知代次的查找键桶：摘要碰撞可共存，
// 规范名相同而原始名不同则拒绝，原始名也相同则报已存在。
// 调用方须持有 d.mu。
func (d *Directory) conflictLocked(canonical string, canonHash [32]byte, rawName string, exclude map[*entry]struct{}) error {
	for gen := range d.gens {
		lk := d.host.lookupKey(d.nameKey(gen), canonical)
		for _, e := range d.buckets[lk] {
			if _, skip := exclude[e]; skip {
				continue
			}
			if e.canonicalHash != canonHash {
				continue // 摘要碰撞，可共存
			}
			existing, err := d.open(e)
			if err != nil {
				return ErrIntegrity
			}
			if existing == rawName {
				return ErrExist
			}
			return ErrCanonicalConflict
		}
	}
	return nil
}

// findLocked 在全部已知代次的桶中定位规范名匹配且可解密的目录项。
// 调用方须持有 d.mu。
func (d *Directory) findLocked(canonical string, canonHash [32]byte) (*entry, error) {
	for gen := range d.gens {
		lk := d.host.lookupKey(d.nameKey(gen), canonical)
		for _, e := range d.buckets[lk] {
			if e.canonicalHash != canonHash {
				continue
			}
			if _, err := d.open(e); err != nil {
				return nil, ErrIntegrity
			}
			return e, nil
		}
	}
	return nil, ErrNotFound
}

func (d *Directory) removeEntryLocked(e *entry) {
	bucket := d.buckets[e.lookupKey]
	for i, x := range bucket {
		if x == e {
			bucket = append(bucket[:i], bucket[i+1:]...)
			break
		}
	}
	if len(bucket) == 0 {
		delete(d.buckets, e.lookupKey)
	} else {
		d.buckets[e.lookupKey] = bucket
	}
}

func (d *Directory) insertEntryLocked(e *entry) {
	d.buckets[e.lookupKey] = append(d.buckets[e.lookupKey], e)
}

// Create 原子检查查找键桶中的全部候选后插入新目录项。
func (d *Directory) Create(rawName string, inodeID, inodeGen uint64) error {
	canonical, canonHash, err := d.prepare(rawName)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.conflictLocked(canonical, canonHash, rawName, nil); err != nil {
		return err
	}
	e, err := d.seal(d.current, rawName, canonHash, inodeID, inodeGen)
	if err != nil {
		return err
	}
	e.lookupKey = d.host.lookupKey(d.nameKey(d.current), canonical)
	d.insertEntryLocked(e)
	return nil
}

// Lookup 查找并解密目录项，核对 inode 身份后返回原始名与 inode 标识。
func (d *Directory) Lookup(name string) (resolved string, inodeID, inodeGen uint64, err error) {
	canonical, canonHash, err := d.prepare(name)
	if err != nil {
		return "", 0, 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e, err := d.findLocked(canonical, canonHash)
	if err != nil {
		return "", 0, 0, err
	}
	raw, err := d.open(e)
	if err != nil {
		return "", 0, 0, ErrIntegrity
	}
	return raw, e.inodeID, e.inodeGen, nil
}

// Remove 删除与给定名规范匹配的目录项。
func (d *Directory) Remove(name string) error {
	canonical, canonHash, err := d.prepare(name)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e, err := d.findLocked(canonical, canonHash)
	if err != nil {
		return err
	}
	d.removeEntryLocked(e)
	return nil
}

// BeginRotation 开始轮换：仅新增 keyGeneration+1，目录 nonce 与其余策略不变。
func (d *Directory) BeginRotation() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.gens) != 1 {
		return ErrRotationActive
	}
	d.gens[d.current+1] = struct{}{}
	d.current++
	return nil
}

// RotateOne 以单事务迁移一个旧代次目录项，返回迁移后剩余的旧代次项数。
func (d *Directory) RotateOne() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.gens) != 2 {
		return 0, ErrNoRotation
	}
	remaining := 0
	var target *entry
	for _, bucket := range d.buckets {
		for _, e := range bucket {
			if e.generation != d.current {
				remaining++
				if target == nil {
					target = e
				}
			}
		}
	}
	if target == nil {
		return 0, nil
	}
	raw, err := d.open(target)
	if err != nil {
		return remaining, ErrIntegrity
	}
	canonical := Canonicalize(raw, d.policy.CaseMode)
	if sha256.Sum256([]byte(canonical)) != target.canonicalHash {
		return remaining, ErrIntegrity
	}
	migrated, err := d.seal(d.current, raw, target.canonicalHash, target.inodeID, target.inodeGen)
	if err != nil {
		return remaining, err
	}
	migrated.lookupKey = d.host.lookupKey(d.nameKey(d.current), canonical)
	// 单项事务：先摘除旧项再插入新项，持锁期间对外原子。
	d.removeEntryLocked(target)
	d.insertEntryLocked(migrated)
	return remaining - 1, nil
}

// FinishRotation 在全部目录项迁移完成后撤下旧密钥代次。
func (d *Directory) FinishRotation() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.gens) != 2 {
		return ErrNoRotation
	}
	for _, bucket := range d.buckets {
		for _, e := range bucket {
			if e.generation != d.current {
				return ErrRotationIncomplete
			}
		}
	}
	for gen := range d.gens {
		if gen != d.current {
			delete(d.gens, gen)
		}
	}
	d.policy.KeyGeneration = d.current
	return nil
}

// Rotate 一次性完成整轮轮换。
func (d *Directory) Rotate() error {
	if err := d.BeginRotation(); err != nil {
		return err
	}
	for {
		remaining, err := d.RotateOne()
		if err != nil {
			return err
		}
		if remaining == 0 {
			break
		}
	}
	return d.FinishRotation()
}
