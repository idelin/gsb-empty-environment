package linker

import "fmt"

// LinkOptions controls the end-to-end pipeline.
type LinkOptions struct {
	// GCSections enables section-level garbage collection.
	GCSections bool
	// ICF selects ICFDisabled, ICFSafe, or ICFAll.
	ICF ICFMode
	// EntrySymbols are appended to the image's existing roots.
	EntrySymbols []string
	// RootSections are appended to the image's existing explicit roots.
	RootSections []SectionID

	// Validate runs the built-in VM equivalence checker after linking.
	Validate bool
	// Probes are explicit function calls to compare.  If empty, one probe is
	// generated for every code section using its Arity metadata.
	Probes []CallProbe
	// AutoProbes is retained for compatibility; validation generates probes
	// automatically unless DisableAutoProbes is set.
	AutoProbes bool
	// DisableAutoProbes suppresses automatic probe generation when Probes is
	// empty.
	DisableAutoProbes bool
	// MaxSteps bounds each VM execution.  Zero selects a safe default.
	MaxSteps int
}

// Options is an alias for LinkOptions.
type Options = LinkOptions

// LinkResult is the output of the end-to-end pipeline.
type LinkResult struct {
	Image *Image
	GC    *GCResult
	// ICF describes the folding pass in the post-GC ID space.  Use
	// SectionMap to translate original input IDs all the way to Image.
	ICF *ICFResult
	// SectionMap maps section IDs in the input image to section IDs in
	// Image.
	SectionMap map[SectionID]SectionID
	Validation *ValidationReport
}

// Result is an alias for LinkResult.
type Result = LinkResult

// Link applies the requested GC and ICF passes to a flat image.
func Link(img *Image, opts LinkOptions) (*LinkResult, error) {
	return LinkImage(img, opts)
}

// LinkImage is the explicit spelling of Link.
func LinkImage(img *Image, opts LinkOptions) (*LinkResult, error) {
	if img == nil {
		return nil, fmt.Errorf("linker: nil image")
	}
	if err := img.Validate(); err != nil {
		return nil, err
	}
	work := img.Clone()
	if len(opts.EntrySymbols) > 0 {
		work.EntrySymbols = uniqueStrings(append(work.EntrySymbols, opts.EntrySymbols...))
	}
	if len(opts.RootSections) > 0 {
		work.RootSections = append(work.RootSections, opts.RootSections...)
	}

	res := &LinkResult{SectionMap: make(map[SectionID]SectionID)}
	inputToPre := make(map[SectionID]SectionID)
	pre := work
	if opts.GCSections {
		gcImage, gcRes, err := GCSections(work)
		if err != nil {
			return nil, err
		}
		res.GC = gcRes
		pre = gcImage
		for old := range work.Sections {
			id := SectionID(old)
			if newID, ok := gcRes.OldToNew[id]; ok {
				inputToPre[id] = newID
			}
		}
	} else {
		for i := range work.Sections {
			id := SectionID(i)
			inputToPre[id] = id
		}
	}

	mode := opts.ICF
	if mode == "" {
		mode = ICFDisabled
	}
	final, icfRes, err := ICF(pre, mode)
	if err != nil {
		return nil, err
	}
	res.ICF = icfRes
	res.Image = final

	for old := range img.Sections {
		id := SectionID(old)
		preID, ok := inputToPre[id]
		if !ok {
			continue
		}
		if finalID, ok := icfRes.OldToNew[preID]; ok {
			res.SectionMap[id] = finalID
		}
	}

	if opts.Validate {
		liveOriginal := make(map[SectionID]bool)
		if res.GC != nil {
			for id := range res.GC.OldToNew {
				liveOriginal[id] = true
			}
		} else {
			for i := range work.Sections {
				liveOriginal[SectionID(i)] = true
			}
		}
		probes := opts.Probes
		if len(probes) == 0 && !opts.DisableAutoProbes {
			probes = AutoProbesFor(work, liveOriginal)
		}
		report, err := Validate(work, final, res.SectionMap, ValidateOptions{
			Probes:       probes,
			MaxSteps:     opts.MaxSteps,
			LiveSections: liveOriginal,
		})
		if err != nil {
			return res, err
		}
		res.Validation = report
		if mode == ICFAll {
			// --icf=all deliberately permits pointer-identity changes.  Surface
			// them through Validation.OK but only fail on execution errors.
			for _, c := range report.Calls {
				if c.Error != "" {
					return res, fmt.Errorf("linker: validation failed: %s", c.Error)
				}
			}
		} else {
			if !report.CallsOK {
				return res, fmt.Errorf("linker: validation failed: call results differ")
			}
			if mode == ICFSafe && !report.PointersOK {
				return res, fmt.Errorf("linker: validation failed: safe ICF changed observable pointer identity")
			}
		}
	}
	return res, nil
}

// LinkObjects merges objects and runs the linking pipeline.
func LinkObjects(objects []*Object, opts LinkOptions) (*LinkResult, error) {
	img, err := MergeObjects(objects...)
	if err != nil {
		return nil, err
	}
	return Link(img, opts)
}
