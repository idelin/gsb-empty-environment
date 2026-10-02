package shardgw

import (
	"fmt"
	"net"
	"sort"
	"sync"
)

// GeoDB 是一个内置的 IP 网段 → 地理位置库，
// 按最长前缀匹配解析客户端来源 IP 的地理位置。
type GeoDB struct {
	mu      sync.RWMutex
	entries []geoEntry
}

type geoEntry struct {
	network *net.IPNet
	loc     Location
}

func NewGeoDB() *GeoDB { return &GeoDB{} }

// Add 注册一个 CIDR 网段及其地理位置。
func (db *GeoDB) Add(cidr string, loc Location) error {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("shardgw: invalid CIDR %q: %w", cidr, err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.entries = append(db.entries, geoEntry{network: network, loc: loc})
	return nil
}

// Lookup 返回 IP 的地理位置，采用最长前缀匹配。
func (db *GeoDB) Lookup(ip net.IP) (Location, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	best := -1
	bestBits := -1
	for i, e := range db.entries {
		if !e.network.Contains(ip) {
			continue
		}
		ones, _ := e.network.Mask.Size()
		if ones > bestBits {
			best, bestBits = i, ones
		}
	}
	if best < 0 {
		return Location{}, false
	}
	return db.entries[best].loc, true
}

// Subnets 解析一组 CIDR 字符串为网段列表（用于节点局域网声明）。
func ParseSubnets(cidrs ...string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("shardgw: invalid subnet %q: %w", c, err)
		}
		out = append(out, n)
	}
	// 更具体的网段优先匹配。
	sort.Slice(out, func(i, j int) bool {
		oi, _ := out[i].Mask.Size()
		oj, _ := out[j].Mask.Size()
		return oi > oj
	})
	return out, nil
}
