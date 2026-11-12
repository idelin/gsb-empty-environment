package netsec

import (
	"fmt"
	"time"
)

// cacheEntry pairs an accepted record with its absolute expiry instant.
type cacheEntry struct {
	rec     Record
	expires time.Time
}

// selectCacheable keeps only records reachable from the question section and
// discards every unrelated record, including attacker-injected additional
// records. Reachability follows name-bearing RDATA (CNAME, PTR, SRV, MX, NS,
// SOA): a record is relevant when its owner name is a question name or a
// name some already-relevant record points at. Address and TXT records
// introduce no names and therefore cannot pull extra records into scope.
func selectCacheable(msg *Message) []Record {
	relevant := make(map[string]bool)
	for i := range msg.Questions {
		relevant[normalizeName(msg.Questions[i].Name)] = true
	}

	sections := [][]Record{msg.Answers, msg.Authority, msg.Additional}
	kept := make([][]bool, len(sections))
	for i := range sections {
		kept[i] = make([]bool, len(sections[i]))
	}

	// Fixpoint expansion: each pass may add new names via name-bearing
	// records; it terminates in at most len(records)+1 passes.
	for changed := true; changed; {
		changed = false
		for si, section := range sections {
			for ri := range section {
				if kept[si][ri] {
					continue
				}
				rec := &section[ri]
				if !relevant[normalizeName(rec.Name)] {
					continue
				}
				kept[si][ri] = true
				changed = true
				if rec.Data != nil {
					for _, target := range rec.Data.targets() {
						if t := normalizeName(target); t != "" && !relevant[t] {
							relevant[t] = true
						}
					}
				}
			}
		}
	}

	var out []Record
	for si, section := range sections {
		for ri := range section {
			if kept[si][ri] {
				out = append(out, section[ri])
			}
		}
	}
	return out
}

func recordKey(r Record) string {
	return fmt.Sprintf("%d|%d|%s|%T|%#v", r.Type, r.Class, normalizeName(r.Name), r.Data, r.Data)
}

// store writes already-admitted records into the cache and returns the
// records that were newly inserted. A zero DNS TTL is a mDNS goodbye: the
// record is not cached (and any prior entry for the same key is removed).
func (a *Admitter) store(records []Record, now time.Time) []Record {
	a.lock()
	defer a.unlock()

	var added []Record
	for _, r := range records {
		owner := normalizeName(r.Name)
		key := recordKey(r)

		if r.TTL == 0 {
			if bucket := a.cache[owner]; bucket != nil {
				delete(bucket, key)
			}
			continue
		}

		bucket := a.cache[owner]
		if bucket == nil {
			bucket = make(map[string]cacheEntry)
			a.cache[owner] = bucket
		}
		if _, exists := bucket[key]; exists {
			continue
		}
		bucket[key] = cacheEntry{rec: r, expires: now.Add(time.Duration(r.TTL) * time.Second)}
		added = append(added, r)
	}
	return added
}

// Lookup returns the live, non-expired cached records of rtype for name.
func (a *Admitter) Lookup(name string, rtype uint16) ([]Record, bool) {
	owner := normalizeName(name)
	now := a.now()

	a.lock()
	defer a.unlock()

	bucket := a.cache[owner]
	var out []Record
	for key, entry := range bucket {
		if now.After(entry.expires) {
			delete(bucket, key)
			continue
		}
		if entry.rec.Type == rtype {
			out = append(out, entry.rec)
		}
	}
	if len(bucket) == 0 {
		delete(a.cache, owner)
	}
	return out, len(out) > 0
}

// CachedNames returns the normalized owner names currently present with at
// least one live entry, after expired entries have been swept.
func (a *Admitter) CachedNames() []string {
	now := a.now()
	a.lock()
	defer a.unlock()

	var names []string
	for owner, bucket := range a.cache {
		for key, entry := range bucket {
			if now.After(entry.expires) {
				delete(bucket, key)
			}
		}
		if len(bucket) > 0 {
			names = append(names, owner)
		} else {
			delete(a.cache, owner)
		}
	}
	return names
}
