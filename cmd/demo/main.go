// Command demo runs an end-to-end sample of the tracevault component:
// mass ingestion, anonymized heatmap query, and a credentialed targeted
// reveal with an audit trail.
package main

import (
	"crypto/rand"
	"fmt"
	mrand "math/rand"
	"sort"
	"time"

	"tracevault"
)

func randomKey(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func main() {
	regulatorKey := randomKey(32)

	vault, err := tracevault.NewVault(tracevault.Config{
		MasterKey:    randomKey(32),
		RegulatorKey: regulatorKey,
		Salt:         randomKey(32),
		EpochSeconds: 3600, // pseudonyms rotate hourly
		GridSize:     0.01, // ~1 km cells
	})
	if err != nil {
		panic(err)
	}

	// --- 1. Mass ingestion: simulated trajectories for many entities -------
	base := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	rng := mrand.New(mrand.NewSource(42))
	entities := []string{"user-1001", "user-1002", "user-1003", "user-1004", "user-1005"}
	centers := [][2]float64{
		{31.2304, 121.4737}, {31.2350, 121.4800}, {31.2200, 121.4600},
		{31.2400, 121.4900}, {31.2300, 121.4700},
	}
	for i, id := range entities {
		lat, lon := centers[i][0], centers[i][1]
		for h := 0; h < 24; h++ {
			for k := 0; k < 2; k++ { // 2 samples per hour
				lat += (rng.Float64() - 0.5) * 0.004
				lon += (rng.Float64() - 0.5) * 0.004
				ts := base.Add(time.Duration(h)*time.Hour + time.Duration(k*30)*time.Minute)
				if err := vault.Ingest(tracevault.Point{EntityID: id, Lat: lat, Lon: lon, Timestamp: ts}); err != nil {
					panic(err)
				}
			}
		}
	}
	fmt.Printf("[ingest] stored %d encrypted records for %d entities\n", vault.StoreLen(), len(entities))

	// --- 2. Day-to-day read-only query: anonymized heat distribution -------
	heat := vault.Heatmap(base, base.Add(24*time.Hour))
	type cellCount struct {
		cell  string
		count int
	}
	var cells []cellCount
	total := 0
	for c, n := range heat {
		cells = append(cells, cellCount{c, n})
		total += n
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i].count > cells[j].count })
	fmt.Printf("[heatmap] %d salted cells, %d samples; top cells (hash -> count):\n", len(cells), total)
	for i := 0; i < 5 && i < len(cells); i++ {
		fmt.Printf("          %s -> %d\n", cells[i].cell, cells[i].count)
	}

	// --- 3. Judicial audit: targeted reveal of one entity, one time window -
	target := "user-1002"
	from := vault.EpochOf(base.Add(9 * time.Hour))
	to := vault.EpochOf(base.Add(12 * time.Hour))
	now := base.Add(48 * time.Hour)
	cred, err := tracevault.IssueCredential(regulatorKey, target, from, to,
		"court-order-2026-1003", now.Add(-time.Hour), now.Add(2*time.Hour))
	if err != nil {
		panic(err)
	}
	points, err := vault.Reveal(cred, now)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[reveal] credential %s... authorized %d records for %q (epochs %d-%d):\n",
		cred.Nonce[:8], len(points), target, from, to)
	for _, p := range points {
		fmt.Printf("          %s  %s  (%.5f, %.5f)\n",
			p.Timestamp.Format("15:04"), p.EntityID, p.Lat, p.Lon)
	}

	// --- 4. Forged credential is rejected and logged -----------------------
	forged := cred
	forged.EntityID = "user-1001" // tampered after signing
	if _, err := vault.Reveal(forged, now); err != nil {
		fmt.Printf("[reveal] forged credential rejected: %v\n", err)
	}

	// --- 5. Audit trail -----------------------------------------------------
	fmt.Printf("[audit] chain valid: %v\n", vault.AuditLog().Verify())
	for _, e := range vault.AuditLog().Entries() {
		fmt.Printf("          #%d %-13s issuer=%-20s entity=%-10s epochs=%d-%d records=%d hash=%.12s...\n",
			e.Seq, e.Action, e.Issuer, e.EntityID, e.FromEpoch, e.ToEpoch, e.Records, e.Hash)
	}
}
