package netsec

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"sort"
)

// Item 是一个待（批量）验证的 Schnorr 签名条目。所有字段必须为规范编码：
// Pub/R 为 65 字节非压缩 SEC1 点，S 为 32 字节大端标量。
type Item struct {
	Pub []byte
	R   []byte
	S   []byte
	Msg []byte
}

// Status 是批量验证的总体结论。
type Status string

const (
	// StatusAccepted 批量等式通过。该接受是概率性的：仅说明在本批随机系数下等式成立。
	StatusAccepted Status = "accepted"
	// StatusRejected 定位到一份或多份无效签名，索引见 Invalid。
	StatusRejected Status = "rejected"
	// StatusFallback 种子缺失或出现逻辑不一致，整批已退回逐份验证。
	StatusFallback Status = "single-fallback"
	// StatusDuplicates 存在重复的完整 (公钥,R,s,消息) 条目，未执行密码学验证。
	StatusDuplicates Status = "duplicates-rejected"
)

// Method 记录结论是通过哪条代码路径得到的。
type Method string

const (
	MethodBatch          Method = "batch-equation"
	MethodBisection      Method = "recursive-bisection"
	MethodSingleFallback Method = "per-item-verify"
)

// Result 是一次批量验证的完整结果与审计材料。
type Result struct {
	// Status 为总体结论。
	Status Status
	// Method 为得到结论的具体路径（重复拒绝时为空）。
	Method Method
	// Invalid 为无效条目在输入批次中的索引（升序、去重）。
	Invalid []int
	// Duplicates 为重复条目的全部索引（升序、去重）。
	Duplicates []int
	// Coefficients 为实际使用的随机系数 a_i，与输入条目一一对应；
	// 退回逐份验证或重复拒绝时为 nil。
	Coefficients [][32]byte
	// SeedDigest 为宿主种子的 SHA-256 摘要；未取种子时为 nil。
	SeedDigest []byte
	// TranscriptDigest 为整批规范转录的 SHA-256 摘要。
	TranscriptDigest []byte
}

// SeedProvider 提供每批新取的宿主随机 32 字节种子。
// 生产环境使用 HostSeedProvider；测试可注入确定性种子。
type SeedProvider interface {
	Seed() ([32]byte, error)
}

// HostSeedProvider 直接委托 crypto/rand，保证每批取得全新的宿主随机种子。
type HostSeedProvider struct{}

func (HostSeedProvider) Seed() ([32]byte, error) {
	var seed [32]byte
	_, err := rand.Read(seed[:])
	return seed, err
}

type parsedItem struct {
	pub, r, s, msg []byte
	px, py         *big.Int
	rx, ry         *big.Int
	sInt, e        *big.Int
}

type itemKey struct {
	pub, r, s, msg string
}

// VerifyBatch 验证 1 至 256 份签名。
//
// 处理顺序：解析与规范校验 -> 构造整批转录 -> 重复条目拒绝 ->
// 取宿主随机种子 -> 派生随机系数 -> 批量等式 -> 失败则递归二分定位。
// 种子缺失（provider 为 nil 或返回错误）时整批退回逐份验证，
// 调用方无法影响或指定种子。
func VerifyBatch(items []Item, provider SeedProvider) (*Result, error) {
	n := len(items)
	if n == 0 || n > MaxBatchSize {
		return nil, errEmptyBatch
	}

	parsed := make([]parsedItem, n)
	for i := range items {
		it := &items[i]
		p := &parsed[i]
		var err error
		if p.px, p.py, err = decodePoint(it.Pub); err != nil {
			return nil, &ItemError{Index: i, Reason: err}
		}
		if p.rx, p.ry, err = decodePoint(it.R); err != nil {
			return nil, &ItemError{Index: i, Reason: err}
		}
		if p.sInt, err = decodeScalar(it.S); err != nil {
			return nil, &ItemError{Index: i, Reason: err}
		}
		p.pub = append([]byte(nil), it.Pub...)
		p.r = append([]byte(nil), it.R...)
		p.s = append([]byte(nil), it.S...)
		p.msg = append([]byte(nil), it.Msg...)
		p.e = challenge(p.pub, p.r, p.msg)
	}

	transcript := buildTranscript(parsed)
	transcriptDigest := sha256.Sum256(transcript)

	// 先拒绝重复的完整 (公钥,R,s,消息) 条目。
	seen := make(map[itemKey]int, n)
	dupSet := make(map[int]struct{})
	for i, p := range parsed {
		key := itemKey{string(p.pub), string(p.r), string(p.s), string(p.msg)}
		if first, ok := seen[key]; ok {
			dupSet[first] = struct{}{}
			dupSet[i] = struct{}{}
		} else {
			seen[key] = i
		}
	}
	if len(dupSet) > 0 {
		return &Result{
			Status:           StatusDuplicates,
			Duplicates:       sortedKeys(dupSet),
			TranscriptDigest: transcriptDigest[:],
		}, nil
	}

	// 种子必须来自宿主且每批新取；缺失即退回逐份验证。
	if provider == nil {
		return singleFallback(parsed, transcriptDigest[:]), nil
	}
	seed, err := provider.Seed()
	if err != nil || len(seed) != 32 {
		return singleFallback(parsed, transcriptDigest[:]), nil
	}
	seedDigest := sha256.Sum256(seed[:])

	coeffs := deriveCoefficients(seed[:], transcript, n)
	coeffBytes := make([][32]byte, n)
	for i, c := range coeffs {
		c.FillBytes(coeffBytes[i][:])
	}

	out := &Result{
		Coefficients:     coeffBytes,
		SeedDigest:       seedDigest[:],
		TranscriptDigest: transcriptDigest[:],
	}

	if subsetEquation(parsed, coeffs, allIndices(n)) {
		out.Status = StatusAccepted
		out.Method = MethodBatch
		return out, nil
	}

	// 批量等式失败：用同一转录/种子派生的原索引系数递归二分。
	invalid, inconsistent := locateInvalid(parsed, coeffs)
	if inconsistent {
		// 父失败而两子均通过等不一致情形：整批退回全部单验。
		fb := singleFallback(parsed, transcriptDigest[:])
		fb.SeedDigest = seedDigest[:]
		return fb, nil
	}

	// 最终叶子均为单验，这里再对定位结果做一次逐份交叉确认，确保返回精确索引。
	confirmed := singleVerifyIndices(parsed, invalid)
	if len(confirmed) != len(invalid) {
		fb := singleFallback(parsed, transcriptDigest[:])
		fb.SeedDigest = seedDigest[:]
		return fb, nil
	}
	out.Status = StatusRejected
	out.Method = MethodBisection
	out.Invalid = confirmed
	return out, nil
}

// buildTranscript 构造整批规范转录：条目数前缀 + 每个条目的定长序列化字段。
func buildTranscript(items []parsedItem) []byte {
	var b bytes.Buffer
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(items)))
	b.Write(count[:])
	for _, p := range items {
		writeLenBytes(&b, p.pub)
		writeLenBytes(&b, p.r)
		writeLenBytes(&b, p.s)
		writeLenBytes(&b, p.msg)
	}
	return b.Bytes()
}

// deriveCoefficients 按规格派生系数：
// a_i = SHA256(批次域标签 || 种子 || 转录 || BE32(i) || BE32(counter)) mod q，
// 每个索引独立从 counter=0 开始，结果为 0 则递增计数器重试，保证 a_i 非零。
func deriveCoefficients(seed, transcript []byte, n int) []*big.Int {
	out := make([]*big.Int, n)
	for i := 0; i < n; i++ {
		var idx [4]byte
		binary.BigEndian.PutUint32(idx[:], uint32(i))
		v, _ := firstNonZero(0, func(counter uint32) *big.Int {
			var ctr [4]byte
			binary.BigEndian.PutUint32(ctr[:], counter)
			v, zero := hashModQ(
				[]byte(labelCoeff),
				seed,
				transcript,
				idx[:],
				ctr[:],
			)
			if zero {
				return big.NewInt(0)
			}
			return v
		})
		out[i] = v
	}
	return out
}

// firstNonZero 从 start 开始递增大端计数器，直到 hashAt 返回非零标量。
// 返回该标量与实际使用的计数器；用于实现“遇 0 递增计数器”。
func firstNonZero(start uint32, hashAt func(counter uint32) *big.Int) (*big.Int, uint32) {
	for counter := start; ; counter++ {
		v := hashAt(counter)
		if v.Sign() != 0 {
			return v, counter
		}
	}
}

// subsetEquation 验证给定索引子集上的批量等式：
// Σ a_i s_i G = Σ a_i R_i + Σ a_i e_i P_i。
func subsetEquation(items []parsedItem, coeffs []*big.Int, indices []int) bool {
	t := new(big.Int)
	var sumRX, sumRY *big.Int
	var sumPX, sumPY *big.Int

	for _, i := range indices {
		p := items[i]
		a := coeffs[i]

		t.Add(t, new(big.Int).Mul(a, p.sInt))

		arx, ary := scalarMult(p.rx, p.ry, encodeScalar(a))
		sumRX, sumRY = addPoint(sumRX, sumRY, arx, ary)

		ae := new(big.Int).Mul(a, p.e)
		ae.Mod(ae, q)
		aepx, aepy := scalarMult(p.px, p.py, encodeScalar(ae))
		sumPX, sumPY = addPoint(sumPX, sumPY, aepx, aepy)
	}
	t.Mod(t, q)

	var lhsX, lhsY *big.Int
	if t.Sign() != 0 {
		lhsX, lhsY = scalarBaseMult(encodeScalar(t))
	} // t == 0 时左式为无穷远点，坐标保持 nil

	rhsX, rhsY := addPoint(sumRX, sumRY, sumPX, sumPY)
	return pointsEqual(lhsX, lhsY, rhsX, rhsY)
}

// subsetCheck 是二分过程使用的子集判定，独立成变量便于测试注入矛盾场景。
var subsetCheck = subsetEquation

// locateInvalid 递归二分定位无效签名。
// 返回 (疑似无效索引, inconsistent)。inconsistent 为 true 表示出现
// “父失败而两子均通过”等逻辑矛盾，调用方须整批退回逐份验证。
func locateInvalid(items []parsedItem, coeffs []*big.Int) ([]int, bool) {
	var walk func(indices []int) ([]int, bool)
	walk = func(indices []int) ([]int, bool) {
		if len(indices) == 1 {
			// 叶子必须单验，而不是只看随机系数下的子集等式。
			ok, err := verifyParsed(items[indices[0]])
			if err != nil || !ok {
				return []int{indices[0]}, false
			}
			return nil, false
		}

		mid := len(indices) / 2
		left := indices[:mid]
		right := indices[mid:]
		leftOK := subsetCheck(items, coeffs, left)
		rightOK := subsetCheck(items, coeffs, right)

		if leftOK && rightOK {
			// 父集合此前已确认失败，两子却同时通过：概率性不一致，整批退回单验。
			return nil, true
		}

		var bad []int
		if !leftOK {
			l, inconsistent := walk(left)
			if inconsistent {
				return nil, true
			}
			bad = append(bad, l...)
		}
		if !rightOK {
			r, inconsistent := walk(right)
			if inconsistent {
				return nil, true
			}
			bad = append(bad, r...)
		}
		return bad, false
	}

	bad, inconsistent := walk(allIndices(len(items)))
	if inconsistent {
		return nil, true
	}
	sort.Ints(bad)
	return bad, false
}

func verifyParsed(p parsedItem) (bool, error) {
	lhsX, lhsY := scalarBaseMult(encodeScalar(p.sInt))
	epX, epY := scalarMult(p.px, p.py, encodeScalar(p.e))
	rhsX, rhsY := addPoint(p.rx, p.ry, epX, epY)
	return pointsEqual(lhsX, lhsY, rhsX, rhsY), nil
}

func singleVerifyIndices(items []parsedItem, indices []int) []int {
	var bad []int
	for _, i := range indices {
		if ok, _ := verifyParsed(items[i]); !ok {
			bad = append(bad, i)
		}
	}
	sort.Ints(bad)
	return bad
}

func singleFallback(items []parsedItem, transcriptDigest []byte) *Result {
	bad := singleVerifyIndices(items, allIndices(len(items)))
	res := &Result{
		Method:           MethodSingleFallback,
		Invalid:          bad,
		TranscriptDigest: transcriptDigest,
	}
	if len(bad) == 0 {
		res.Status = StatusAccepted
	} else {
		res.Status = StatusRejected
	}
	return res
}

func allIndices(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}
func sortedKeys(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
