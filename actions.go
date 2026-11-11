package pdfactions

import (
	"fmt"
)

// Target describes a safe, in-document navigation destination delivered to
// the previewer frontend.
type Target struct {
	// PageIndex is the 0-based index of the destination page in the revision
	// that produced the target.
	PageIndex int
	// PageID is the indirect object id of the destination page.
	PageID ObjectID
	// View is the destination view: the raw PDF fit name (for example "Fit",
	// "FitH") or "XYZ" with the coordinates carried by Params.
	View string
	// Params holds the optional destination parameters in PDF order.
	Params []float64
	// Source identifies where the action came from, for example an
	// annotation object id or the catalog open action.
	Source string
}

// BlockedAction reports an action that the security policy intercepted.
type BlockedAction struct {
	Type   Name
	Reason string
	Source string
}

// Policy decides which action types are safe enough for the previewer. The
// zero value is not usable; obtain one from DefaultPolicy.
type Policy struct {
	// AllowedActions lists the action (/S) names that may run. Everything
	// else is intercepted.
	AllowedActions map[Name]bool
	// AllowedNamed lists the /N values of named actions that count as page
	// navigation. Other named actions are intercepted.
	AllowedNamed map[Name]bool
}

// DefaultPolicy allows in-document navigation only: GoTo actions plus the
// standard first/next/prev/last page named actions. JavaScript, URI,
// Launch (external programs), submit-form, sound, movie and every other
// action type are blocked.
func DefaultPolicy() Policy {
	return Policy{
		AllowedActions: map[Name]bool{
			Name("GoTo"):  true,
			Name("Named"): true,
		},
		AllowedNamed: map[Name]bool{
			Name("FirstPage"): true,
			Name("PrevPage"):  true,
			Name("NextPage"):  true,
			Name("LastPage"):  true,
		},
	}
}

// Callbacks receives the preview result of a single revision.
type Callbacks struct {
	OnTarget  func(Target)
	OnBlocked func(BlockedAction)
}

// Previewer caches parsed actions per revision. The cache key includes the
// revision index, so a newer incremental update can never populate or replace
// an entry served to a caller previewing an older revision.
type Previewer struct {
	policy Policy
	cache  map[cacheKey]outcome
}

type cacheKey struct {
	revision int
	id       ObjectID
}

type outcome struct {
	target  *Target
	blocked *BlockedAction
	next    Value
}

// NewPreviewer builds a Previewer using the given policy.
func NewPreviewer(policy Policy) *Previewer {
	return &Previewer{policy: policy, cache: make(map[cacheKey]outcome)}
}

// Preview resolves every page-facing action in rev and dispatches the safe
// navigation targets and intercepted actions through the callbacks. All
// resolution stays inside rev's cross-reference generation.
func (pv *Previewer) Preview(rev *Revision, cb Callbacks) {
	catalog, err := rev.Catalog()
	if err != nil {
		return
	}
	if openAction, ok := catalog.Get(Name("OpenAction")); ok {
		source := "catalog:OpenAction"
		pv.dispatch(rev, openAction, source, cb, newActionChain(openAction))
	}
	rev.collectPages()
	for _, pageID := range rev.pageIDs {
		page, ok := asDict(rev, Ref(pageID), nil)
		if !ok {
			continue
		}
		pv.previewPageFields(rev, page, pageID, cb)
		annotations, ok := page.Get(Name("Annots"))
		if !ok {
			continue
		}
		array, ok := asArray(rev, annotations, nil)
		if !ok {
			continue
		}
		for _, annotationValue := range array {
			annotation, resolved := asDict(rev, annotationValue, nil)
			if !resolved {
				if cb.OnBlocked != nil {
					source := fmt.Sprintf("page %d annotation", rev.pageByID[pageID])
					if ref, isRef := annotationValue.(Ref); isRef {
						source = fmt.Sprintf("page %d annotation %s", rev.pageByID[pageID], ref.ID())
					}
					cb.OnBlocked(BlockedAction{
						Reason: "annotation reference cannot be resolved within this revision",
						Source: source,
					})
				}
				continue
			}
			source := fmt.Sprintf("page %d annotation", rev.pageByID[pageID])
			if ref, isRef := annotationValue.(Ref); isRef {
				source = fmt.Sprintf("page %d annotation %s", rev.pageByID[pageID], ref.ID())
			}
			if action, hasAction := annotation.Get(Name("A")); hasAction {
				chain := newActionChain(action)
				if ref, isRef := annotationValue.(Ref); isRef {
					chain[ref.ID()] = true
					pv.dispatchCached(rev, ref.ID(), action, source, cb, chain)
					continue
				}
				pv.dispatch(rev, action, source, cb, chain)
			}
			if dest, hasDest := annotation.Get(Name("Dest")); hasDest {
				pv.dispatchDestination(rev, dest, source, cb)
			}
		}
	}
}

func (pv *Previewer) previewPageFields(rev *Revision, page *Dict, pageID ObjectID, cb Callbacks) {
	source := fmt.Sprintf("page %d", rev.pageByID[pageID])
	if action, ok := page.Get(Name("Action")); ok {
		pv.dispatch(rev, action, source+" /Action", cb, newActionChain(action))
	}
	additional, ok := page.Get(Name("AA"))
	if !ok {
		return
	}
	aaDict, ok := asDict(rev, additional, nil)
	if !ok {
		return
	}
	for _, event := range aaDict.Keys {
		action, _ := aaDict.Get(event)
		pv.dispatch(rev, action, source+" /AA/"+string(event), cb, newActionChain(action))
	}
}

// newActionChain seeds the cycle guard with the root action so a /Next chain
// pointing back to the root terminates instead of re-entering it.
func newActionChain(root Value) map[ObjectID]bool {
	chain := map[ObjectID]bool{}
	if ref, ok := root.(Ref); ok {
		chain[ref.ID()] = true
	}
	return chain
}

// dispatch resolves one action value, following /Next chains. Action cycles
// are broken with the per-chain seen set.
func (pv *Previewer) dispatch(rev *Revision, value Value, source string, cb Callbacks, chain map[ObjectID]bool) {
	ref, isRef := value.(Ref)
	if isRef {
		id := ref.ID()
		if chain[id] {
			return
		}
		if result, cached := pv.cache[cacheKey{revision: rev.Index, id: id}]; cached {
			chain[id] = true
			pv.emit(result, source, cb)
			return
		}
		chain[id] = true
		result := pv.resolveAction(rev, value, source)
		pv.cache[cacheKey{revision: rev.Index, id: id}] = result
		pv.emit(result, source, cb)
		pv.followNext(rev, result, source, cb, chain)
		return
	}
	result := pv.resolveAction(rev, value, source)
	pv.emit(result, source, cb)
	pv.followNext(rev, result, source, cb, chain)
}

// dispatchCached resolves an action carried by the indirect object id and
// stores the result in the revision-scoped cache. It covers annotations whose
// /A dictionary is embedded, which would otherwise bypass caching.
func (pv *Previewer) dispatchCached(rev *Revision, id ObjectID, value Value, source string, cb Callbacks, chain map[ObjectID]bool) {
	key := cacheKey{revision: rev.Index, id: id}
	if result, ok := pv.cache[key]; ok {
		pv.emit(result, source, cb)
		return
	}
	result := pv.resolveAction(rev, value, source)
	pv.cache[key] = result
	pv.emit(result, source, cb)
	pv.followNext(rev, result, source, cb, chain)
}

// followNext continues through an action's /Next chain. Blocked actions
// carry no next value and terminate the chain.
func (pv *Previewer) followNext(rev *Revision, result outcome, source string, cb Callbacks, chain map[ObjectID]bool) {
	if result.target == nil || result.next == nil {
		return
	}
	switch typed := result.next.(type) {
	case Array:
		for _, item := range typed {
			pv.dispatch(rev, item, source+" /Next", cb, chain)
		}
	default:
		pv.dispatch(rev, typed, source+" /Next", cb, chain)
	}
}

func (pv *Previewer) emit(result outcome, source string, cb Callbacks) {
	switch {
	case result.target != nil && cb.OnTarget != nil:
		target := *result.target
		if target.Source == "" {
			target.Source = source
		}
		cb.OnTarget(target)
	case result.blocked != nil && cb.OnBlocked != nil:
		blocked := *result.blocked
		if blocked.Source == "" {
			blocked.Source = source
		}
		cb.OnBlocked(blocked)
	}
}

func (pv *Previewer) resolveAction(rev *Revision, value Value, source string) outcome {
	action, ok := asDict(rev, value, nil)
	if !ok {
		return blocked("", "action cannot be resolved within this revision", source)
	}
	typeValue, ok := action.Get(Name("S"))
	if !ok {
		return blocked("", "action dictionary missing /S", source)
	}
	actionType, ok := asName(rev, typeValue, nil)
	if !ok {
		return blocked("", "action /S is not a name", source)
	}
	if !pv.policy.AllowedActions[actionType] {
		return blocked(actionType, "action type is not allowed by policy", source)
	}
	switch actionType {
	case Name("GoTo"):
		dest, ok := action.Get(Name("D"))
		if !ok {
			return blocked(actionType, "GoTo action missing /D", source)
		}
		result := pv.resolveDestination(rev, dest, source)
		if result.target != nil {
			result.next = dictValue(action, Name("Next"))
		}
		return result
	case Name("Named"):
		named, ok := asName(rev, dictValue(action, Name("N")), nil)
		if !ok {
			return blocked(actionType, "Named action missing /N", source)
		}
		if !pv.policy.AllowedNamed[named] {
			return blocked(actionType, "named action "+string(named)+" is not page navigation", source)
		}
		return outcome{
			target: &Target{View: "Named:" + string(named), Source: source},
			next:   dictValue(action, Name("Next")),
		}
	default:
		return blocked(actionType, "action type is not allowed by policy", source)
	}
}

func (pv *Previewer) dispatchDestination(rev *Revision, value Value, source string, cb Callbacks) {
	result := pv.resolveDestination(rev, value, source)
	pv.emit(result, source, cb)
}

func (pv *Previewer) resolveDestination(rev *Revision, value Value, source string) outcome {
	resolved, ok := rev.dereference(value, nil)
	if !ok {
		return blocked(Name("GoTo"), "destination reference cannot be resolved within this revision", source)
	}
	switch typed := resolved.(type) {
	case Name:
		dest, found := lookupNamedDestination(rev, typed)
		if !found {
			return blocked(Name("GoTo"), "named destination "+string(typed)+" is not defined", source)
		}
		return pv.resolveDestination(rev, dest, source)
	case String:
		dest, found := lookupNamedDestination(rev, Name(typed))
		if !found {
			return blocked(Name("GoTo"), "named destination "+string(typed)+" is not defined", source)
		}
		return pv.resolveDestination(rev, dest, source)
	case Array:
		return pv.resolveExplicitDestination(rev, typed, source)
	default:
		return blocked(Name("GoTo"), "unsupported destination type", source)
	}
}

func (pv *Previewer) resolveExplicitDestination(rev *Revision, array Array, source string) outcome {
	if len(array) < 2 {
		return blocked(Name("GoTo"), "explicit destination needs a page and a view", source)
	}
	pageRef, ok := array[0].(Ref)
	if !ok {
		return blocked(Name("GoTo"), "destination page must be an indirect reference", source)
	}
	index, known := rev.PageIndex(pageRef.ID())
	if !known {
		return blocked(Name("GoTo"), "destination page is outside this revision's page tree", source)
	}
	view, ok := asName(rev, array[1], nil)
	if !ok {
		return blocked(Name("GoTo"), "destination view must be a name", source)
	}
	target := &Target{
		PageIndex: index,
		PageID:    pageRef.ID(),
		View:      string(view),
		Source:    source,
	}
	for _, param := range array[2:] {
		switch number := param.(type) {
		case Integer:
			target.Params = append(target.Params, float64(number))
		case Real:
			target.Params = append(target.Params, float64(number))
		}
	}
	return outcome{target: target}
}

// lookupNamedDestination resolves a name through the catalog /Dests
// dictionary or the document-level name tree, staying inside rev.
func lookupNamedDestination(rev *Revision, name Name) (Value, bool) {
	catalog, err := rev.Catalog()
	if err != nil {
		return nil, false
	}
	if destsValue, ok := catalog.Get(Name("Dests")); ok {
		if dests, ok := asDict(rev, destsValue, nil); ok {
			if direct, ok := dests.Get(name); ok {
				return direct, true
			}
		}
	}
	namesValue, ok := catalog.Get(Name("Names"))
	if !ok {
		return nil, false
	}
	names, ok := asDict(rev, namesValue, nil)
	if !ok {
		return nil, false
	}
	destsTreeValue, ok := names.Get(Name("Dests"))
	if !ok {
		return nil, false
	}
	return searchNameTree(rev, destsTreeValue, name, map[ObjectID]bool{})
}

func searchNameTree(rev *Revision, value Value, name Name, seen map[ObjectID]bool) (Value, bool) {
	if ref, ok := value.(Ref); ok {
		if seen[ref.ID()] {
			return nil, false
		}
	}
	node, ok := asDict(rev, value, seen)
	if !ok {
		return nil, false
	}
	if ref, ok := value.(Ref); ok {
		seen[ref.ID()] = true
	}
	if names, ok := node.Get(Name("Names")); ok {
		array, ok := asArray(rev, names, nil)
		if ok {
			for i := 0; i+1 < len(array); i += 2 {
				switch key := array[i].(type) {
				case String:
					if Name(key) == name {
						return array[i+1], true
					}
				case Name:
					if key == name {
						return array[i+1], true
					}
				}
			}
		}
	}
	if kids, ok := node.Get(Name("Kids")); ok {
		array, ok := asArray(rev, kids, nil)
		if ok {
			for _, kid := range array {
				if value, found := searchNameTree(rev, kid, name, seen); found {
					return value, true
				}
			}
		}
	}
	return nil, false
}

func blocked(actionType Name, reason, source string) outcome {
	return outcome{blocked: &BlockedAction{Type: actionType, Reason: reason, Source: source}}
}
