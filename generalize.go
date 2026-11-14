package netsec

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"time"
)

const metersPerDegree = 111_320.0

type cellBox struct {
	ID        string
	CenterLat float64
	CenterLon float64
	MinLat    float64
	MaxLat    float64
	MinLon    float64
	MaxLon    float64
}

type spatialGrid struct {
	cellMeters float64
	latStep    float64
	lonStep    float64
}

func newSpatialGrid(cellMeters float64) spatialGrid {
	step := cellMeters / metersPerDegree
	return spatialGrid{cellMeters: cellMeters, latStep: step, lonStep: step}
}

func (g spatialGrid) cell(lat, lon float64) cellBox {
	lat = clamp(lat, -90, 90)
	lon = normalizeLon(lon)

	latIdx := int(math.Floor((lat + 90) / g.latStep))
	lonIdx := int(math.Floor((lon + 180) / g.lonStep))

	maxLatIdx := int(math.Ceil(180/g.latStep)) - 1
	maxLonIdx := int(math.Ceil(360/g.lonStep)) - 1
	latIdx = min(max(latIdx, 0), maxLatIdx)
	lonIdx = min(max(lonIdx, 0), maxLonIdx)

	minLat := -90 + float64(latIdx)*g.latStep
	minLon := -180 + float64(lonIdx)*g.lonStep
	maxLat := math.Min(90, minLat+g.latStep)
	maxLon := math.Min(180, minLon+g.lonStep)
	return cellBox{
		ID:        fmt.Sprintf("c%d:%d", latIdx, lonIdx),
		CenterLat: (minLat + maxLat) / 2,
		CenterLon: (minLon + maxLon) / 2,
		MinLat:    minLat,
		MaxLat:    maxLat,
		MinLon:    minLon,
		MaxLon:    maxLon,
	}
}

type workRecord struct {
	outputID      string
	quasi         map[string]string
	points        []Point
	cells         []cellBox
	inputPoints   int
	sampledPoints int
	pointsRemoved int
	active        bool
	suppressions  []Suppression
}

func (r *workRecord) suppress(stage, reason, details string) {
	if !r.active {
		return
	}
	r.active = false
	r.suppressions = append(r.suppressions, Suppression{
		RecordID: r.outputID,
		Stage:    stage,
		Reason:   reason,
		Details:  details,
	})
}

func prepareRecords(ds Dataset, cfg Config) []*workRecord {
	grid := newSpatialGrid(cfg.SpatialCellMeters)
	records := make([]*workRecord, 0, len(ds))

	for i := range ds {
		in := cloneTrajectory(ds[i])
		rec := &workRecord{
			inputPoints: len(in.Points),
			quasi:       cloneMap(in.QuasiIdentifiers),
			active:      true,
		}
		if rec.quasi == nil {
			rec.quasi = map[string]string{}
		}
		for name := range rec.quasi {
			if _, reserved := reservedQIs[name]; reserved {
				delete(rec.quasi, name)
			}
		}
		if cfg.PreserveIDs {
			rec.outputID = in.ID
		}
		if rec.outputID == "" {
			rec.outputID = hashToken(strconv.Itoa(i), strconv.FormatInt(cfg.Seed, 10))
		}

		// Direct identifiers are deliberately not carried into workRecord.
		_ = in.UserID
		_ = in.DirectIdentifiers

		points, removedInvalid := cleanPoints(in.Points)
		rec.pointsRemoved += removedInvalid
		if len(points) == 0 {
			rec.suppress("input", "empty-trajectory", "no valid points")
			records = append(records, rec)
			continue
		}

		points = samplePoints(points, cfg.MaxSamplePoints, cfg.MinSampleInterval)
		rec.sampledPoints = len(points)
		if rec.inputPoints == 0 {
			rec.inputPoints = len(points)
		}
		if len(points) < cfg.MinPointsAfterSuppression {
			rec.suppress("input", "too-few-points",
				fmt.Sprintf("trajectory has %d points after sampling; minimum is %d", len(points), cfg.MinPointsAfterSuppression))
			records = append(records, rec)
			continue
		}

		rec.points = make([]Point, 0, len(points))
		rec.cells = make([]cellBox, 0, len(points))
		for _, p := range points {
			cell := grid.cell(p.Lat, p.Lon)
			rec.points = append(rec.points, Point{
				Time: bucketTime(p.Time, cfg.TimeBucket),
				Lat:  cell.CenterLat,
				Lon:  cell.CenterLon,
			})
			rec.cells = append(rec.cells, cell)
		}
		records = append(records, rec)
	}
	return records
}

func cleanPoints(in []Point) ([]Point, int) {
	points := make([]Point, 0, len(in))
	invalid := 0
	seen := make(map[string]struct{}, len(in))
	for _, p := range in {
		if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) ||
			math.IsInf(p.Lat, 0) || math.IsInf(p.Lon, 0) ||
			p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
			invalid++
			continue
		}
		key := p.Time.UTC().Format(time.RFC3339Nano) + "|" +
			strconv.FormatFloat(p.Lat, 'f', 9, 64) + "|" +
			strconv.FormatFloat(p.Lon, 'f', 9, 64)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		points = append(points, p)
	}
	sort.SliceStable(points, func(i, j int) bool {
		return points[i].Time.Before(points[j].Time)
	})
	return points, invalid
}

func samplePoints(points []Point, maxPoints int, minInterval time.Duration) []Point {
	if len(points) == 0 {
		return nil
	}
	kept := make([]Point, 0, len(points))
	kept = append(kept, points[0])
	var last time.Time
	last = points[0].Time
	for _, p := range points[1:] {
		if minInterval > 0 && p.Time.Sub(last) < minInterval {
			continue
		}
		kept = append(kept, p)
		last = p.Time
	}

	if maxPoints > 0 && len(kept) > maxPoints {
		if maxPoints == 1 {
			kept = kept[:1]
		} else {
			selected := make([]Point, 0, maxPoints)
			for i := 0; i < maxPoints; i++ {
				idx := int(math.Round(float64(i) * float64(len(kept)-1) / float64(maxPoints-1)))
				if len(selected) > 0 && idx == int(math.Round(float64(i-1)*float64(len(kept)-1)/float64(maxPoints-1))) {
					continue
				}
				selected = append(selected, kept[idx])
			}
			kept = selected
		}
	}
	return kept
}

func applyPerturbation(rec *workRecord, cfg Config) {
	for i := range rec.points {
		if cfg.PerturbationMeters > 0 {
			// Perturbation is a deterministic function of the generalized cell,
			// so every trajectory visiting that cell receives the same noisy
			// coordinate and the equivalence class is preserved.
			rng := rand.New(rand.NewSource(valueSeed(cfg.Seed, "cell", rec.cells[i].ID)))
			rec.points[i].Lat, rec.points[i].Lon = perturbInsideCell(
				rec.cells[i], cfg.PerturbationMeters, rng)
		}
		if cfg.TemporalJitter > 0 {
			temporalOffset := temporalOffsetFor(rec.points[i].Time, cfg)
			rec.points[i].Time = rec.points[i].Time.Add(temporalOffset)
		}
	}
}

// temporalOffsetFor returns a bucket-constant jitter. All trajectories with an
// observation in the same generalized time bucket receive the same offset, so
// jitter cannot split an equivalence class.
func temporalOffsetFor(bucketStart time.Time, cfg Config) time.Duration {
	rng := rand.New(rand.NewSource(valueSeed(cfg.Seed, "bucket", bucketStart.UTC().Format(time.RFC3339Nano))))
	offset := time.Duration(rng.Int63n(int64(cfg.TemporalJitter)))
	if offset == 0 {
		return 0
	}
	candidate := bucketStart.Add(offset)
	if !bucketTime(candidate, cfg.TimeBucket).Equal(bucketStart) ||
		candidate.Format("2006-01-02") != bucketStart.Format("2006-01-02") ||
		timeOfDaySlot(candidate, cfg.TimeBucket) != timeOfDaySlot(bucketStart, cfg.TimeBucket) {
		return 0
	}
	return offset
}

func bucketTime(t time.Time, bucket time.Duration) time.Time {
	if bucket <= 0 {
		return t
	}
	// For common sub-day buckets (and full days), align to local wall-clock
	// boundaries instead of an absolute-duration boundary. This makes the
	// published time-of-day meaningful in the record's own time zone.
	if bucket >= time.Second && bucket <= 24*time.Hour {
		y, m, d := t.Date()
		midnight := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
		since := t.Sub(midnight)
		return midnight.Add(since.Truncate(bucket))
	}
	return t.Truncate(bucket)
}

func perturbInsideCell(cell cellBox, radius float64, rng *rand.Rand) (float64, float64) {
	if radius <= 0 {
		return cell.CenterLat, cell.CenterLon
	}
	dx := (rng.Float64()*2 - 1) * radius
	dy := (rng.Float64()*2 - 1) * radius

	lat := cell.CenterLat + dy/metersPerDegree
	cosLat := math.Cos(lat * math.Pi / 180)
	if math.Abs(cosLat) < 1e-6 {
		cosLat = 1e-6
	}
	lon := cell.CenterLon + dx/(metersPerDegree*cosLat)

	const inset = 1e-9
	lat = clamp(lat, cell.MinLat+inset, cell.MaxLat-inset)
	lon = clamp(lon, cell.MinLon+inset, cell.MaxLon-inset)
	return lat, lon
}

func valueSeed(seed int64, parts ...string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strconv.FormatInt(seed, 10)))
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return int64(h.Sum64())
}

func normalizeLon(lon float64) float64 {
	lon = math.Mod(lon+180, 360)
	if lon < 0 {
		lon += 360
	}
	return lon - 180
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
