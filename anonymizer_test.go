package netsec

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		K:                         3,
		SpatialCellMeters:         1000,
		TimeBucket:                time.Hour,
		MaxSamplePoints:           32,
		PerturbationMeters:        40,
		MinLocationSupport:        3,
		MinTrajectorySupport:      3,
		MinPatternSupport:         3,
		RegularRepeatDays:         2,
		MinPointsAfterSuppression: 2,
		MaxRisk:                   1.0 / 3.0,
		Seed:                      11,
	}
}

func baseDataset() Dataset {
	ds := make(Dataset, 0, 12)
	start := time.Date(2026, 11, 9, 8, 15, 0, 0, time.UTC)
	for group := 0; group < 4; group++ {
		homeLat := 31.20 + float64(group)*0.03
		workLat := homeLat + 0.05
		home := [2]float64{homeLat, 121.40}
		work := [2]float64{workLat, 121.40}
		for member := 0; member < 3; member++ {
			points := []Point{
				{Time: start, Lat: home[0], Lon: home[1]},
				{Time: start.Add(50 * time.Minute), Lat: work[0], Lon: work[1]},
				{Time: start.Add(9*time.Hour + 50*time.Minute), Lat: home[0], Lon: home[1]},
				{Time: start.Add(24*time.Hour + 35*time.Minute), Lat: work[0], Lon: work[1]},
				{Time: start.Add(34*time.Hour + 20*time.Minute), Lat: home[0], Lon: home[1]},
			}
			ds = append(ds, Trajectory{
				ID:     "record-" + string(rune('a'+group)) + string(rune('0'+member)),
				UserID: "person-" + string(rune('a'+group)) + string(rune('0'+member)),
				Points: points,
				QuasiIdentifiers: map[string]string{
					"age_band": "30-39",
					"district": "same",
				},
			})
		}
	}
	return ds
}

func TestFrequentLocationAssociationDoesNotReidentify(t *testing.T) {
	cfg := testConfig()
	result, err := Anonymize(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if !result.CanPublish() {
		t.Fatalf("expected publishable result, risk=%+v", result.Risk)
	}
	if len(result.Records) == 0 {
		t.Fatal("no records published")
	}

	original := map[string]struct{}{}
	for _, rec := range baseDataset() {
		for _, p := range rec.Points {
			original[coordKey(p.Lat, p.Lon)] = struct{}{}
		}
	}

	// Each generalized frequent location must be shared by at least k
	// trajectories. An attacker who knows one person's home cannot isolate the
	// corresponding published trajectory by exact coordinates.
	grid := newSpatialGrid(cfg.SpatialCellMeters)
	cellSupport := map[string]int{}
	coordsByCell := map[string]map[string]struct{}{}
	for _, rec := range result.Records {
		seen := map[string]struct{}{}
		for _, p := range rec.Points {
			key := coordKey(p.Lat, p.Lon)
			if _, leaked := original[key]; leaked {
				t.Fatalf("exact original coordinate leaked: %s", key)
			}
			cell := grid.cell(p.Lat, p.Lon).ID
			seen[cell] = struct{}{}
			if coordsByCell[cell] == nil {
				coordsByCell[cell] = map[string]struct{}{}
			}
			coordsByCell[cell][coordKey(p.Lat, p.Lon)] = struct{}{}
		}
		for cell := range seen {
			cellSupport[cell]++
		}
	}
	shared := 0
	for _, count := range cellSupport {
		if count >= cfg.K {
			shared++
		}
	}
	if shared == 0 {
		t.Fatal("no generalized frequent location is shared by k trajectories")
	}
	for cell, coords := range coordsByCell {
		if len(coords) != 1 {
			t.Fatalf("cell %s exposes distinguishable perturbed coordinates: %v", cell, coords)
		}
	}
}

func TestTemporalGeneralizationAndJitterStayInBucket(t *testing.T) {
	cfg := testConfig()
	result, err := Anonymize(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	rawTimes := map[string]struct{}{}
	for _, rec := range baseDataset() {
		for _, p := range rec.Points {
			rawTimes[p.Time.Format(time.RFC3339Nano)] = struct{}{}
		}
	}
	for _, rec := range result.Records {
		for _, p := range rec.Points {
			if !p.Time.Equal(bucketTime(p.Time, cfg.TimeBucket)) {
				t.Fatalf("time %s is not aligned to bucket %s", p.Time, cfg.TimeBucket)
			}
			if _, leaked := rawTimes[p.Time.Format(time.RFC3339Nano)]; leaked {
				t.Fatalf("exact observation time leaked: %s", p.Time)
			}
		}
	}

	// Optional jitter must remain inside the same generalized bucket and must
	// preserve intra-trajectory ordering.
	cfg = testConfig()
	cfg.TemporalJitter = 7 * time.Minute
	result, err = Anonymize(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	for _, rec := range result.Records {
		for _, p := range rec.Points {
			bucket := bucketTime(p.Time, cfg.TimeBucket)
			if p.Time.Before(bucket) || !p.Time.Before(bucket.Add(cfg.TimeBucket)) {
				t.Fatalf("jittered time %s escaped bucket %s", p.Time, bucket)
			}
		}
		for i := 1; i < len(rec.Points); i++ {
			if rec.Points[i].Time.Before(rec.Points[i-1].Time) {
				t.Fatalf("temporal jitter reversed point order: %s before %s",
					rec.Points[i].Time, rec.Points[i-1].Time)
			}
		}
	}

	raw, err := AssessRisk(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("AssessRisk: %v", err)
	}
	if raw.Records != len(baseDataset()) {
		t.Fatalf("risk assessment records=%d, want %d", raw.Records, len(baseDataset()))
	}
}

func TestDailyBucketUsesLocalMidnight(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	tm := time.Date(2026, 11, 9, 8, 15, 0, 0, loc)
	got := bucketTime(tm, 24*time.Hour)
	want := time.Date(2026, 11, 9, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("bucketTime=%s, want %s", got, want)
	}
}

func TestSamplingCapsTrajectoryPoints(t *testing.T) {
	cfg := testConfig()
	cfg.MaxSamplePoints = 3
	result, err := Anonymize(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	for _, rec := range result.Records {
		if len(rec.Points) > cfg.MaxSamplePoints {
			t.Fatalf("sampled trajectory has %d points, cap is %d", len(rec.Points), cfg.MaxSamplePoints)
		}
	}
}

func TestRareLocationIsSuppressed(t *testing.T) {
	cfg := testConfig()
	ds := baseDataset()
	rareStart := time.Date(2026, 11, 9, 8, 15, 0, 0, time.UTC)
	ds = append(ds, Trajectory{
		ID:     "rare-location",
		UserID: "rare-person",
		Points: []Point{
			{Time: rareStart, Lat: 35.5, Lon: 130.5},
			{Time: rareStart.Add(time.Hour), Lat: 35.6, Lon: 130.6},
		},
	})

	result, err := Anonymize(ds, cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if result.Report.RareLocationPointsRemoved == 0 {
		t.Fatal("rare location points were not removed")
	}
	if result.Risk.RareLocationRate != 0 {
		t.Fatalf("published rare location rate=%v, want 0", result.Risk.RareLocationRate)
	}
	for _, rec := range result.Records {
		for _, p := range rec.Points {
			if p.Lat > 35 && p.Lon > 130 {
				t.Fatalf("rare location survived anonymization: %+v", p)
			}
		}
	}
}

func TestRareGeneralizedTrajectoryIsSuppressed(t *testing.T) {
	cfg := testConfig()
	ds := baseDataset()
	start := time.Date(2026, 11, 9, 8, 15, 0, 0, time.UTC)

	// Both cells are common, but this particular path occurs only once.
	ds = append(ds, Trajectory{
		ID:     "rare-path",
		UserID: "rare-path-person",
		Points: []Point{
			{Time: start, Lat: 31.20, Lon: 121.40},
			{Time: start.Add(time.Hour), Lat: 31.23, Lon: 121.40},
			{Time: start.Add(2 * time.Hour), Lat: 31.20, Lon: 121.40},
			{Time: start.Add(24 * time.Hour), Lat: 31.23, Lon: 121.40},
		},
	})

	result, err := Anonymize(ds, cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if result.Report.RareTrajectoriesSuppressed == 0 {
		t.Fatal("rare generalized trajectory was not suppressed")
	}
	if result.Risk.RareTrajectoryRate != 0 {
		t.Fatalf("published rare trajectory rate=%v, want 0", result.Risk.RareTrajectoryRate)
	}
	if !hasReason(result.Report.Suppressions, "rare-trajectory") {
		t.Fatalf("missing rare-trajectory suppression reason: %+v", result.Report.Suppressions)
	}
}

func TestRareRegularPatternIsSuppressed(t *testing.T) {
	cfg := testConfig()
	ds := baseDataset()
	start := time.Date(2026, 11, 9, 6, 15, 0, 0, time.UTC)
	ds = append(ds, Trajectory{
		ID:     "rare-routine",
		UserID: "routine-person",
		Points: []Point{
			{Time: start, Lat: 31.20, Lon: 121.40},
			{Time: start.Add(45 * time.Minute), Lat: 31.25, Lon: 121.40},
			{Time: start.Add(9*time.Hour + 45*time.Minute), Lat: 31.20, Lon: 121.40},
			{Time: start.Add(24 * time.Hour), Lat: 31.25, Lon: 121.40},
			{Time: start.Add(24*time.Hour + 45*time.Minute), Lat: 31.20, Lon: 121.40},
		},
	})

	result, err := Anonymize(ds, cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if result.Report.RegularPatternsSuppressed == 0 {
		t.Fatal("rare regular temporal pattern was not suppressed")
	}
	if result.Risk.RegularPatternRate != 0 {
		t.Fatalf("published regular pattern rate=%v, want 0", result.Risk.RegularPatternRate)
	}
	if !hasReason(result.Report.Suppressions, "rare-regular-pattern") {
		t.Fatalf("missing rare-regular-pattern suppression reason: %+v", result.Report.Suppressions)
	}
}

func TestKAnonymityAcrossQICombinations(t *testing.T) {
	cfg := testConfig()
	result, err := Anonymize(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	report, err := ValidateKAnonymity(result.Records, cfg)
	if err != nil {
		t.Fatalf("ValidateKAnonymity: %v", err)
	}
	if !report.Satisfied {
		t.Fatalf("k-anonymity not satisfied: %+v", report.Violations)
	}
	if report.MinGroupSize < cfg.K {
		t.Fatalf("min group size=%d, want >=%d", report.MinGroupSize, cfg.K)
	}
	for _, combo := range report.Combinations {
		if combo.MinGroupSize < cfg.K {
			t.Fatalf("combination %v has min group %d", combo.Combination, combo.MinGroupSize)
		}
	}
	if result.Report.KAnonymity.MinGroupSize < cfg.K {
		t.Fatalf("run report min group=%d, want >=%d", result.Report.KAnonymity.MinGroupSize, cfg.K)
	}
}

func TestPublishedTrajectoriesAreIndistinguishableWithinK(t *testing.T) {
	cfg := testConfig()
	result, err := Publish(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	groups := map[string]int{}
	for _, rec := range result.Records {
		copy := rec
		copy.ID = ""
		encoded, err := json.Marshal(copy)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		groups[string(encoded)]++
	}
	for key, count := range groups {
		if count < cfg.K {
			t.Fatalf("published trajectory is unique to %d records (want >=%d): %s", count, cfg.K, key)
		}
	}
}

func TestRareQuasiIdentifierValuesAreGeneralized(t *testing.T) {
	cfg := testConfig()
	ds := baseDataset()
	for i := range ds {
		ds[i].QuasiIdentifiers["age"] = strconv.Itoa(20 + i)
	}
	result, err := Publish(ds, cfg)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Report.PublishedRecords != len(ds) {
		t.Fatalf("published %d/%d records after QI generalization",
			result.Report.PublishedRecords, len(ds))
	}
	for _, rec := range result.Records {
		if rec.QuasiIdentifiers["age"] != "*" {
			t.Fatalf("rare age value was not generalized: %+v", rec.QuasiIdentifiers)
		}
	}
}

func TestExplicitQICombinationsAndPublishedRisk(t *testing.T) {
	cfg := testConfig()
	cfg.QICombinations = [][]string{
		{"age_band"},
		{"district", "age_band"},
		{QIFrequentLocation1, QIStartTimeOfDay},
	}
	result, err := Publish(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(result.Report.KAnonymity.Combinations) != len(cfg.QICombinations)+1 {
		t.Fatalf("checked %d combinations, want %d (including the full set)",
			len(result.Report.KAnonymity.Combinations), len(cfg.QICombinations)+1)
	}
	risk, err := AssessRisk(Dataset(result.Records), cfg)
	if err != nil {
		t.Fatalf("AssessRisk published data: %v", err)
	}
	if !risk.Publishable {
		t.Fatalf("published output assessed unsafe: %+v", risk)
	}
}

func TestAllRecordsSuppressedDoesNotFailConfiguration(t *testing.T) {
	cfg := testConfig()
	cfg.K = 3
	cfg.SpatialCellMeters = 50
	cfg.MinLocationSupport = 1
	cfg.MinTrajectorySupport = 1
	cfg.MinPatternSupport = 1
	cfg.QICombinations = [][]string{{"frequent_location_1", "start_time_of_day"}}
	ds := Dataset{
		{ID: "u1", UserID: "user-one", Points: []Point{{Time: time.Now(), Lat: 10, Lon: 10}, {Time: time.Now().Add(time.Hour), Lat: 10.1, Lon: 10.1}}},
		{ID: "u2", UserID: "user-two", Points: []Point{{Time: time.Now(), Lat: 20, Lon: 20}, {Time: time.Now().Add(time.Hour), Lat: 20.1, Lon: 20.1}}},
		{ID: "u3", UserID: "user-three", Points: []Point{{Time: time.Now(), Lat: 30, Lon: 30}, {Time: time.Now().Add(time.Hour), Lat: 30.1, Lon: 30.1}}},
	}
	result, err := Anonymize(ds, cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if result.Report.PublishedRecords != 0 {
		t.Fatalf("published %d records, want 0", result.Report.PublishedRecords)
	}
	if result.Risk.Publishable {
		t.Fatal("empty output marked publishable")
	}
}

func TestLegitimatePublishRemovesDirectIdentifiers(t *testing.T) {
	cfg := testConfig()
	result, err := Publish(baseDataset(), cfg)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Report.PublishedRecords != len(baseDataset()) {
		t.Fatalf("published %d/%d records, suppressions=%+v risk=%+v",
			result.Report.PublishedRecords, len(baseDataset()), result.Report.Suppressions, result.Risk)
	}
	if result.Report.DirectIdentifiersRemoved != len(baseDataset()) {
		t.Fatalf("direct identifier removal count=%d, want %d",
			result.Report.DirectIdentifiersRemoved, len(baseDataset()))
	}
	if len(result.Published()) != len(result.Records) {
		t.Fatal("Published did not return the accepted records")
	}
	if !result.Risk.Unlinkable {
		t.Fatal("accepted result is not marked unlinkable")
	}
	for _, rec := range result.Records {
		if rec.UserID != "" || rec.DirectIdentifiers != nil {
			t.Fatalf("direct identifier leaked in %+v", rec)
		}
		if strings.HasPrefix(rec.ID, "record-") {
			t.Fatalf("source record identifier retained: %s", rec.ID)
		}
	}
	encoded, err := json.Marshal(result.Records)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "person-") || strings.Contains(string(encoded), "user_id") {
		t.Fatalf("direct identifier serialized: %s", encoded)
	}
}

func TestRiskAssessmentBlocksUnsafePublication(t *testing.T) {
	cfg := testConfig()
	ds := Dataset{
		{ID: "u1", Points: []Point{{Time: time.Now(), Lat: 10, Lon: 10}, {Time: time.Now().Add(time.Hour), Lat: 11, Lon: 11}}},
		{ID: "u2", Points: []Point{{Time: time.Now(), Lat: 20, Lon: 20}, {Time: time.Now().Add(time.Hour), Lat: 21, Lon: 21}}},
		{ID: "u3", Points: []Point{{Time: time.Now(), Lat: 30, Lon: 30}, {Time: time.Now().Add(time.Hour), Lat: 31, Lon: 31}}},
	}

	risk, err := AssessRisk(ds, cfg)
	if err != nil {
		t.Fatalf("AssessRisk: %v", err)
	}
	if risk.Publishable {
		t.Fatalf("unsafe dataset assessed publishable: %+v", risk)
	}
	if risk.Unlinkable {
		t.Fatalf("unsafe dataset assessed unlinkable: %+v", risk)
	}
	if risk.MaxReidentificationProbability < 1 {
		t.Fatalf("max re-identification probability=%v, want 1", risk.MaxReidentificationProbability)
	}

	_, err = Publish(ds, cfg)
	var publicationErr *PublicationError
	if !errors.As(err, &publicationErr) {
		t.Fatalf("Publish error=%v, want *PublicationError", err)
	}
	if publicationErr.Risk.Publishable {
		t.Fatal("PublicationError contains a publishable assessment")
	}
}

func TestAnonymizationIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	cfg := testConfig()
	ds := baseDataset()
	before := ds[0].Points[0]
	first, err := Anonymize(ds, cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	second, err := Anonymize(ds, cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if !ds[0].Points[0].Time.Equal(before.Time) || ds[0].Points[0].Lat != before.Lat {
		t.Fatal("input dataset was mutated")
	}
	firstJSON, _ := json.Marshal(first.Records)
	secondJSON, _ := json.Marshal(second.Records)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("anonymization is not deterministic")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	cfg := testConfig()
	cfg.K = 1
	if _, err := NewAnonymizer(cfg); err == nil {
		t.Fatal("expected invalid k error")
	}
	cfg = testConfig()
	cfg.SpatialCellMeters = -1
	if _, err := NewAnonymizer(cfg); err == nil {
		t.Fatal("expected invalid grid error")
	}
	cfg = testConfig()
	cfg.QICombinations = [][]string{{"not_present"}}
	if _, err := Anonymize(baseDataset(), cfg); err == nil {
		t.Fatal("expected unknown quasi-identifier error")
	}
}

func TestDefaultConfigTracksK(t *testing.T) {
	cfg := DefaultConfig()
	cfg.K = 3
	anonymizer, err := NewAnonymizer(cfg)
	if err != nil {
		t.Fatalf("NewAnonymizer: %v", err)
	}
	got := anonymizer.Config()
	if got.MinLocationSupport != 3 || got.MinPatternSupport != 3 || got.MinTrajectorySupport != 3 {
		t.Fatalf("support thresholds did not track K: %+v", got)
	}
	if got.MaxRisk != 1.0/3.0 {
		t.Fatalf("MaxRisk=%v, want %v", got.MaxRisk, 1.0/3.0)
	}
}

func TestAnonymizerIsSafeForConcurrentUse(t *testing.T) {
	anonymizer, err := NewAnonymizer(testConfig())
	if err != nil {
		t.Fatalf("NewAnonymizer: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := anonymizer.Publish(baseDataset()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Publish: %v", err)
	}
}

func hasReason(suppressions []Suppression, reason string) bool {
	for _, s := range suppressions {
		if s.Reason == reason {
			return true
		}
	}
	return false
}

func coordKey(lat, lon float64) string {
	return formatFloat(lat) + "," + formatFloat(lon)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 9, 64)
}
