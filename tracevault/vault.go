// Package tracevault 实现“可追溯轨迹时态加密索引与假名化审计器”：
//
//   - 实体真实 ID 经带密钥的时变伪随机函数（HMAC-SHA256）假名化；
//   - 空间坐标经加盐网格哈希索引，密文轨迹按“实体 × 时间桶”密钥加密；
//   - 日常只读查询仅返回脱敏聚合热力分布（k-匿名抑制）；
//   - 出示司法审计授权凭证时，定向解密单个实体在指定时间段内的轨迹，
//     并写入带哈希链的完整审计日志。
package tracevault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// DefaultBucketSize 为默认时间桶长度（秒），即 1 小时。
const DefaultBucketSize = int64(time.Hour / time.Second)

// PlainPoint 是解密还原后的轨迹点。
type PlainPoint struct {
	EntityID  string
	Lat       float64
	Lng       float64
	Timestamp int64
}

type record struct {
	bucket     int64
	gridToken  string
	nonce      []byte
	ciphertext []byte
}

// Vault 是保护隐私的时空加密索引。
type Vault struct {
	keys       *Keys
	bucketSize int64
	resolution float64

	mu     sync.RWMutex
	byPseu map[string][]*record     // 假名 -> 加密轨迹记录
	heat   map[int64]map[string]int // 时间桶 -> 网格令牌 -> 计数
	audit  *AuditLog
}

// NewVault 创建索引。resolution 为网格边长（度），bucketSize 为时间桶（秒）。
func NewVault(keys *Keys, bucketSize int64, resolution float64) *Vault {
	return &Vault{
		keys:       keys,
		bucketSize: bucketSize,
		resolution: resolution,
		byPseu:     make(map[string][]*record),
		heat:       make(map[int64]map[string]int),
		audit:      NewAuditLog(),
	}
}

// BucketOf 返回时间戳所属的时间桶。
func (v *Vault) BucketOf(ts int64) int64 { return ts / v.bucketSize }

// AuditLog 返回审计日志（只读视图）。
func (v *Vault) AuditLog() *AuditLog { return v.audit }

// Insert 加密并索引一条位置观测。
func (v *Vault) Insert(entityID string, lat, lng float64, ts int64) error {
	if entityID == "" {
		return errors.New("tracevault: empty entity id")
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return fmt.Errorf("tracevault: invalid coordinate (%v, %v)", lat, lng)
	}
	bucket := v.BucketOf(ts)
	pseu := v.keys.pseudonymFor(entityID, bucket)
	token := v.keys.gridToken(CellFor(lat, lng, v.resolution), bucket)

	payload := make([]byte, 0, 24)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(lat))
	payload = append(payload, b[:]...)
	binary.BigEndian.PutUint64(b[:], math.Float64bits(lng))
	payload = append(payload, b[:]...)
	binary.BigEndian.PutUint64(b[:], uint64(ts))
	payload = append(payload, b[:]...)

	block, err := aes.NewCipher(v.keys.recordKey(entityID, bucket))
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	// AAD 绑定假名，防止密文被移植到其他假名条目下。
	ct := gcm.Seal(nil, nonce, payload, []byte(pseu))

	v.mu.Lock()
	v.byPseu[pseu] = append(v.byPseu[pseu], &record{
		bucket:     bucket,
		gridToken:  token,
		nonce:      nonce,
		ciphertext: ct,
	})
	if v.heat[bucket] == nil {
		v.heat[bucket] = make(map[string]int)
	}
	v.heat[bucket][token]++
	v.mu.Unlock()
	return nil
}

// Heatmap 返回某时间桶的脱敏聚合热力分布。
// 观测数少于 minCount 的网格被抑制（k-匿名），结果不含任何实体信息。
func (v *Vault) Heatmap(bucket int64, minCount int) map[string]int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]int)
	for token, n := range v.heat[bucket] {
		if n >= minCount {
			out[token] = n
		}
	}
	return out
}

// Reveal 在司法审计授权凭证有效时，定向解密目标实体在
// [From, To] 时间桶范围内的轨迹，并写入审计日志。
func (v *Vault) Reveal(cred *Credential) ([]PlainPoint, error) {
	if err := cred.verify(v.keys.audit, time.Now().Unix()); err != nil {
		v.audit.append("reveal-denied", cred.Issuer, cred.EntityID, cred.From, cred.To, 0, err.Error())
		return nil, err
	}

	var out []PlainPoint
	v.mu.RLock()
	for bucket := cred.From; bucket <= cred.To; bucket++ {
		pseu := v.keys.pseudonymFor(cred.EntityID, bucket)
		key := v.keys.recordKey(cred.EntityID, bucket)
		for _, rec := range v.byPseu[pseu] {
			pt, err := decryptRecord(key, pseu, rec)
			if err != nil {
				continue
			}
			pt.EntityID = cred.EntityID
			out = append(out, pt)
		}
	}
	v.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	v.audit.append("reveal", cred.Issuer, cred.EntityID, cred.From, cred.To, len(out), "")
	return out, nil
}

func decryptRecord(key []byte, pseu string, rec *record) (PlainPoint, error) {
	var pt PlainPoint
	block, err := aes.NewCipher(key)
	if err != nil {
		return pt, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return pt, err
	}
	payload, err := gcm.Open(nil, rec.nonce, rec.ciphertext, []byte(pseu))
	if err != nil {
		return pt, err
	}
	if len(payload) != 24 {
		return pt, errors.New("tracevault: malformed record payload")
	}
	pt.Lat = math.Float64frombits(binary.BigEndian.Uint64(payload[0:8]))
	pt.Lng = math.Float64frombits(binary.BigEndian.Uint64(payload[8:16]))
	pt.Timestamp = int64(binary.BigEndian.Uint64(payload[16:24]))
	return pt, nil
}
