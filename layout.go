package netsec

import (
	"context"
	"errors"
	"io"
)

// 本包把“区域布局”问题建模为页地址域上的有序决策树，从而在不枚举全部
// 布局的前提下得到合法布局的精确总数，并可在该总数上做无偏均匀抽样。
//
// 域为页 [0, DomainPages)；保留页不可占用；每个区域有固定页数与对齐要求
// （起始页必须是对齐页数的倍数）。区域间不可重叠且必须完整落在域内。
//
// 决策树按“从左到右扫描域”的方式生成全部且仅有的合法布局：
//   - 当前页被保留，则跳过；
//   - 否则对每个尚未放置、且可从当前页合法放置的区域开一个分支；
//   - 以及一个“当前页留空”的分支。
// 每条到达“所有区域均已放置”状态的根到叶路径恰好对应一个合法布局，
// 因此按该树的叶子顺序给出的排名覆盖完整布局分布，抽样时只需对均匀的
// 排名做逆排名，而不使用任何针对合法性的随机重试。

const (
	// MaxRegions 是单个 Spec 允许的区域数量上限。
	MaxRegions = 6
	// MaxDomainPages 是地址域的页数上限。
	MaxDomainPages = 32
)

var (
	// ErrInvalidSpec 表示规格参数超出允许范围或自相矛盾。
	ErrInvalidSpec = errors.New("netsec: invalid layout specification")
	// ErrNoLayout 表示不存在任何满足全部约束的合法布局。
	ErrNoLayout = errors.New("netsec: no valid layout exists")
	// ErrInvalidRank 表示排名越界或布局不是合法布局。
	ErrInvalidRank = errors.New("netsec: invalid rank or layout")
)

// Region 描述一个待放置区域：占用 SizePages 个连续页，起始页必须是
// AlignPages 的倍数（AlignPages 必须是 2 的幂）。
type Region struct {
	SizePages  int
	AlignPages int
}

// Reservation 描述一段不可占用的保留页区间 [StartPage, StartPage+Pages)。
type Reservation struct {
	StartPage int
	Pages     int
}

// Spec 是一次布局问题的完整规格。
type Spec struct {
	DomainPages int
	Regions     []Region
	Reserved    []Reservation
}

// Layout 是区域索引到起始页的映射；Start[i] 为区域 i 的起始页。
type Layout struct {
	Start []int
}

// engine 持有一个规格的预计算结果与计数记忆表。
type engine struct {
	spec     Spec
	reserved []bool
	memo     [][]uint64
}

// Count 返回规格 spec 下合法布局的精确总数；无合法布局时返回 0。
func Count(spec Spec) (uint64, error) {
	eng, err := newEngine(spec)
	if err != nil {
		return 0, err
	}
	return eng.count(0, 0), nil
}

// Rank 返回布局 layout 在完整合法布局序列（决策树叶子顺序）中的排名，
// 排名区间为 [0, Count)。非法布局返回 ErrInvalidRank。
func Rank(spec Spec, layout Layout) (uint64, error) {
	eng, err := newEngine(spec)
	if err != nil {
		return 0, err
	}
	if len(layout.Start) != len(spec.Regions) {
		return 0, ErrInvalidRank
	}
	for i, start := range layout.Start {
		if start < 0 || !eng.canPlace(i, start) {
			return 0, ErrInvalidRank
		}
	}

	rank := uint64(0)
	page, mask := 0, 0
	for page < spec.DomainPages {
		if eng.reserved[page] {
			page++
			continue
		}
		// 分支顺序与 count 的分支顺序严格一致：先放区域（按索引升序），
		// 再留空。对未走的前置分支整体累加其叶子数。
		placed := false
		for i := 0; i < len(spec.Regions); i++ {
			if mask&(1<<uint(i)) != 0 {
				continue
			}
			if !eng.canPlace(i, page) {
				continue
			}
			if layout.Start[i] == page {
				mask |= 1 << uint(i)
				page += spec.Regions[i].SizePages
				placed = true
				break
			}
			rank += eng.count(page+spec.Regions[i].SizePages, mask|(1<<uint(i)))
		}
		if placed {
			continue
		}
		page++ // 选择“当前页留空”分支，其权重为剩余子树，不前置累加
	}
	if mask != (1<<uint(len(spec.Regions)))-1 {
		return 0, ErrInvalidRank
	}
	return rank, nil
}

// Unrank 按完整合法布局序列中的排名 rank 重建布局，是 Rank 的逆操作。
func Unrank(spec Spec, rank uint64) (Layout, error) {
	eng, err := newEngine(spec)
	if err != nil {
		return Layout{}, err
	}
	total := eng.count(0, 0)
	if total == 0 {
		return Layout{}, ErrNoLayout
	}
	if rank >= total {
		return Layout{}, ErrInvalidRank
	}

	start := make([]int, len(spec.Regions))
	page, mask := 0, 0
	for mask != (1<<uint(len(spec.Regions)))-1 {
		if eng.reserved[page] {
			page++
			continue
		}
		advanced := false
		for i := 0; i < len(spec.Regions); i++ {
			if mask&(1<<uint(i)) != 0 {
				continue
			}
			if !eng.canPlace(i, page) {
				continue
			}
			weight := eng.count(page+spec.Regions[i].SizePages, mask|(1<<uint(i)))
			if rank < weight {
				start[i] = page
				mask |= 1 << uint(i)
				page += spec.Regions[i].SizePages
				advanced = true
				break
			}
			rank -= weight
		}
		if !advanced {
			page++ // 落入“当前页留空”分支
		}
	}
	return Layout{Start: start}, nil
}

// Sample 在全部合法布局中均匀无偏地抽取一个布局。
//
// 均匀性由两层保证：先对区间 [0, Count) 做拒绝式均匀取数（拒绝仅发生在
// 把随机字节折叠到有限整数区间时，不涉及布局合法性），再用 Unrank 精确
// 逆排名；因此每个合法布局被选中的概率严格相等，不存在“随机试放、失败
// 重试”所引入的分布偏置。
//
// 零解、上下文取消或随机流提前结束时不消耗任何随机字节（见 Stream.Position）。
func Sample(ctx context.Context, spec Spec, rng *Stream) (Layout, error) {
	eng, err := newEngine(spec)
	if err != nil {
		return Layout{}, err
	}
	total := eng.count(0, 0)
	if total == 0 {
		return Layout{}, ErrNoLayout
	}
	if err := ctx.Err(); err != nil {
		return Layout{}, err
	}
	rank, err := DrawUniform(rng, total)
	if err != nil {
		return Layout{}, err
	}
	return Unrank(spec, rank)
}

// Stream 包装注入的随机字节源，并记录已消耗字节数，使宿主可以在取消或
// 失败后确定随机流所处的位置。
type Stream struct {
	src      io.Reader
	position int64
}

// NewStream 用随机字节源创建一个随机流。
func NewStream(src io.Reader) *Stream {
	return &Stream{src: src}
}

// Position 返回至今已从底层源读取的字节数。
func (s *Stream) Position() int64 {
	return s.position
}

// DrawUniform 从流中抽取 [0, n) 上严格均匀的整数。
//
// 采用按字节的经典拒绝采样：以 2^k（k 为覆盖 n 所需的位数）为模数，拒绝
// 落在不完整尾部区间的取值，从而消除模偏置。被拒绝的字节同样计入
// Position，因此调用方可以精确复现或接续随机流。n 必须为正。
func DrawUniform(s *Stream, n uint64) (uint64, error) {
	if n == 0 {
		return 0, ErrInvalidSpec
	}
	if n == 1 {
		return 0, nil
	}
	bits := 0
	for bound := uint64(1); bound < n; bound <<= 1 {
		bits++
	}
	mod := uint64(1) << uint(bits)
	limit := mod - (mod % n) // [0, limit) 为接受区间
	for {
		var value uint64
		for remaining := (bits + 7) / 8; remaining > 0; remaining-- {
			b, err := s.readByte()
			if err != nil {
				return 0, err
			}
			value = value<<8 | uint64(b)
		}
		value %= mod
		if value < limit {
			return value % n, nil
		}
	}
}

func (s *Stream) readByte() (byte, error) {
	var buf [1]byte
	if _, err := io.ReadFull(s.src, buf[:]); err != nil {
		return 0, err
	}
	s.position++
	return buf[0], nil
}

func newEngine(spec Spec) (*engine, error) {
	if spec.DomainPages <= 0 || spec.DomainPages > MaxDomainPages {
		return nil, ErrInvalidSpec
	}
	if len(spec.Regions) > MaxRegions {
		return nil, ErrInvalidSpec
	}
	for _, region := range spec.Regions {
		if region.SizePages <= 0 || region.AlignPages <= 0 {
			return nil, ErrInvalidSpec
		}
		if region.AlignPages&(region.AlignPages-1) != 0 {
			return nil, ErrInvalidSpec
		}
		if region.SizePages > spec.DomainPages {
			return nil, ErrInvalidSpec
		}
	}
	eng := &engine{spec: spec}
	eng.reserved = make([]bool, spec.DomainPages)
	for _, reservation := range spec.Reserved {
		if reservation.StartPage < 0 || reservation.Pages < 0 ||
			reservation.StartPage+reservation.Pages > spec.DomainPages {
			return nil, ErrInvalidSpec
		}
		for page := reservation.StartPage; page < reservation.StartPage+reservation.Pages; page++ {
			eng.reserved[page] = true
		}
	}
	eng.memo = make([][]uint64, spec.DomainPages+1)
	for page := range eng.memo {
		eng.memo[page] = make([]uint64, 1<<uint(len(spec.Regions)))
		for mask := range eng.memo[page] {
			eng.memo[page][mask] = ^uint64(0)
		}
	}
	return eng, nil
}

// canPlace 判断区域 i 是否可以从 page 起始放置：满足对齐、不越域、不触碰保留页。
// 区域间重叠由决策树中“已放置区域占用的页不会再次进入分支”隐式排除。
func (eng *engine) canPlace(i, page int) bool {
	region := eng.spec.Regions[i]
	if page < 0 || page%region.AlignPages != 0 || page+region.SizePages > eng.spec.DomainPages {
		return false
	}
	for p := page; p < page+region.SizePages; p++ {
		if eng.reserved[p] {
			return false
		}
	}
	return true
}

// count 返回子问题 (page, mask) 的合法补全布局数：当前扫描到 page，
// 已放置区域集合为 mask。结果记忆化，域与区域数都很小时状态数至多
// (MaxDomainPages+1) * 2^MaxRegions。
func (eng *engine) count(page, mask int) uint64 {
	if page >= eng.spec.DomainPages {
		if mask == (1<<uint(len(eng.spec.Regions)))-1 {
			return 1
		}
		return 0
	}
	if cached := eng.memo[page][mask]; cached != ^uint64(0) {
		return cached
	}
	var result uint64
	if eng.reserved[page] {
		result = eng.count(page+1, mask)
	} else {
		for i := range eng.spec.Regions {
			if mask&(1<<uint(i)) == 0 && eng.canPlace(i, page) {
				result += eng.count(page+eng.spec.Regions[i].SizePages, mask|(1<<uint(i)))
			}
		}
		result += eng.count(page+1, mask) // 当前页留空
	}
	eng.memo[page][mask] = result
	return result
}
