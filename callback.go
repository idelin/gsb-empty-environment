package taskpkg

import (
	"fmt"
	"reflect"
)

func invokeAlternate(alt any, methods []string, raw []byte) ParseResult {
	switch f := alt.(type) {
	case ParserFunc:
		return f(methods, raw)
	case func([]string, []byte) ParseResult:
		return f(methods, raw)
	case func([]string, []byte) (ParseResult, error):
		result, err := f(methods, raw)
		if err != nil {
			result.Err = toParseError(err, len(raw))
		}
		return result
	case func([]string, []byte) ([]Response, error):
		responses, err := f(methods, raw)
		return resultFromRecords(methods, raw, responses, toParseError(err, len(raw)), false)
	case func([]string, []byte) ([]Response, *ParseError):
		responses, perr := f(methods, raw)
		return resultFromRecords(methods, raw, responses, perr, false)
	case func([]string, []byte) []Response:
		return resultFromRecords(methods, raw, f(methods, raw), nil, false)
	case func([]string, []byte) ([]Segment, error):
		segments, err := f(methods, raw)
		return resultFromRecords(methods, raw, responsesFromSegments(segments), toParseError(err, len(raw)), true)
	case func([]string, []byte) ([]Segment, *ParseError):
		segments, perr := f(methods, raw)
		return resultFromRecords(methods, raw, responsesFromSegments(segments), perr, true)
	case func([]string, []byte) []Segment:
		return resultFromRecords(methods, raw, responsesFromSegments(f(methods, raw)), nil, true)
	case func([]byte, []string) ParseResult:
		return f(raw, methods)
	case func([]byte, []string) (ParseResult, error):
		result, err := f(raw, methods)
		if err != nil {
			result.Err = toParseError(err, len(raw))
		}
		return result
	case func([]byte, []string) ([]Response, error):
		responses, err := f(raw, methods)
		return resultFromRecords(methods, raw, responses, toParseError(err, len(raw)), false)
	case func([]byte, []string) ([]Response, *ParseError):
		responses, perr := f(raw, methods)
		return resultFromRecords(methods, raw, responses, perr, false)
	case func([]byte, []string) []Response:
		return resultFromRecords(methods, raw, f(raw, methods), nil, false)
	case func([]byte, []string) ([]Segment, error):
		segments, err := f(raw, methods)
		return resultFromRecords(methods, raw, responsesFromSegments(segments), toParseError(err, len(raw)), true)
	case func([]byte) ([]Response, error):
		responses, err := f(raw)
		return resultFromRecords(methods, raw, responses, toParseError(err, len(raw)), false)
	case func([]byte) ([]Response, *ParseError):
		responses, perr := f(raw)
		return resultFromRecords(methods, raw, responses, perr, false)
	case func([]byte) []Response:
		return resultFromRecords(methods, raw, f(raw), nil, false)
	case interface {
		Parse([]string, []byte) ParseResult
	}:
		return f.Parse(methods, raw)
	case interface {
		Parse([]byte, []string) ParseResult
	}:
		return f.Parse(raw, methods)
	}
	return invokeReflect(alt, methods, raw)
}

func isNilParser(alt any) bool {
	if alt == nil {
		return true
	}
	value := reflect.ValueOf(alt)
	switch value.Kind() {
	case reflect.Func, reflect.Interface, reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

func resultFromRecords(methods []string, raw []byte, responses []Response, perr *ParseError, minimal bool) ParseResult {
	return ParseResult{
		Strategy:  "alternate",
		Methods:   append([]string(nil), methods...),
		Raw:       raw,
		Responses: responses,
		Err:       perr,
		Minimal:   minimal,
	}
}

func toParseError(err error, offset int) *ParseError {
	if err == nil {
		return nil
	}
	if perr, ok := err.(*ParseError); ok {
		return perr
	}
	return &ParseError{
		Kind:         ErrCallback,
		Offset:       offset,
		DetailOffset: offset,
		Message:      err.Error(),
	}
}

func responsesFromSegments(segments []Segment) []Response {
	responses := make([]Response, 0, len(segments))
	for i, segment := range segments {
		boundary := segment.Boundary
		if boundary == "" {
			boundary = BoundaryUnknown
		}
		responses = append(responses, Response{
			Index:          i,
			RequestIndex:   segment.RequestIndex,
			StatusCode:     segment.StatusCode,
			Start:          segment.Start,
			HeaderEnd:      segment.Start,
			BodyStart:      segment.Start,
			BodyEnd:        segment.End,
			End:            segment.End,
			Boundary:       boundary,
			DeclaredLength: -1,
			ExpectedEnd:    -1,
			Informational:  segment.Informational || (segment.StatusCode >= 100 && segment.StatusCode <= 199),
		})
	}
	return responses
}

func invokeReflect(alt any, methods []string, raw []byte) ParseResult {
	value := reflect.ValueOf(alt)
	if !value.IsValid() || value.Kind() != reflect.Func {
		return ParseResult{
			Strategy: "alternate",
			Methods:  append([]string(nil), methods...),
			Raw:      raw,
			Err: &ParseError{
				Kind:         ErrCallback,
				Offset:       len(raw),
				DetailOffset: len(raw),
				Message:      fmt.Sprintf("alternate parser has unsupported type %T", alt),
			},
		}
	}

	typ := value.Type()
	if typ.NumIn() > 2 {
		return callbackFailure(methods, raw, fmt.Sprintf("alternate parser has %d parameters", typ.NumIn()))
	}

	args := make([]reflect.Value, typ.NumIn())
	usedMethods, usedRaw := false, false
	for i := 0; i < typ.NumIn(); i++ {
		in := typ.In(i)
		switch {
		case in == reflect.TypeOf([]string{}):
			if usedMethods {
				return callbackFailure(methods, raw, "alternate parser has duplicate method parameters")
			}
			args[i] = reflect.ValueOf(methods)
			usedMethods = true
		case in == reflect.TypeOf([]byte{}):
			if usedRaw {
				return callbackFailure(methods, raw, "alternate parser has duplicate raw parameters")
			}
			args[i] = reflect.ValueOf(raw)
			usedRaw = true
		case in.Kind() == reflect.Slice && in.Elem().Kind() == reflect.String && reflect.TypeOf(methods).ConvertibleTo(in):
			if usedMethods {
				return callbackFailure(methods, raw, "alternate parser has duplicate method parameters")
			}
			args[i] = reflect.ValueOf(methods).Convert(in)
			usedMethods = true
		case in.Kind() == reflect.Slice && in.Elem().Kind() == reflect.Uint8 && reflect.TypeOf(raw).ConvertibleTo(in):
			if usedRaw {
				return callbackFailure(methods, raw, "alternate parser has duplicate raw parameters")
			}
			args[i] = reflect.ValueOf(raw).Convert(in)
			usedRaw = true
		case in.Kind() == reflect.Interface && in.NumMethod() == 0:
			// A single empty-interface parameter is treated as the raw stream.
			if usedRaw {
				return callbackFailure(methods, raw, "alternate parser has ambiguous interface parameters")
			}
			args[i] = reflect.ValueOf(raw)
			usedRaw = true
		default:
			return callbackFailure(methods, raw, fmt.Sprintf("unsupported alternate parser parameter %s", in))
		}
	}
	if typ.NumIn() > 0 && !usedMethods && !usedRaw {
		return callbackFailure(methods, raw, "alternate parser has no usable parameters")
	}

	outs := safeCall(value, args, len(raw))
	if len(outs) == 0 {
		return resultFromRecords(methods, raw, nil, nil, false)
	}

	result, perr := parseOutputs(outs, methods, raw)
	return resultWithError(methods, raw, result, perr)
}

func safeCall(value reflect.Value, args []reflect.Value, rawLen int) (outs []reflect.Value) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outs = []reflect.Value{reflect.ValueOf(&ParseError{
				Kind:         ErrCallback,
				Message:      fmt.Sprintf("alternate parser panic: %v", recovered),
				Offset:       rawLen,
				DetailOffset: rawLen,
			})}
		}
	}()
	return value.Call(args)
}

func parseOutputs(outs []reflect.Value, methods []string, raw []byte) (ParseResult, *ParseError) {
	result := ParseResult{
		Strategy: "alternate",
		Methods:  append([]string(nil), methods...),
		Raw:      raw,
	}

	first := outs[0]
	if !first.IsValid() || (isNilValue(first)) {
		return result, nil
	}

	if len(outs) > 1 {
		last := outs[len(outs)-1]
		if last.IsValid() && !isNilValue(last) && last.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			err := last.Interface().(error)
			result.Err = toParseError(err, len(raw))
		}
	}

	switch {
	case first.Type().AssignableTo(reflect.TypeOf(ParseResult{})):
		return first.Convert(reflect.TypeOf(ParseResult{})).Interface().(ParseResult), result.Err
	case first.Type().AssignableTo(reflect.TypeOf(&ParseResult{})):
		if parsed, ok := first.Convert(reflect.TypeOf(&ParseResult{})).Interface().(*ParseResult); ok && parsed != nil {
			return *parsed, parsed.Err
		}
	}
	if responses, ok := responsesFromSlice(first); ok {
		return resultFromRecords(methods, raw, responses, result.Err, false), result.Err
	}

	if first.Kind() == reflect.Ptr && !first.IsNil() {
		element := first.Elem()
		if pe, ok := element.Interface().(ParseError); ok {
			result.Err = &pe
			return result, result.Err
		}
	}

	if first.Kind() == reflect.Struct {
		for i := 0; i < first.NumField(); i++ {
			field := first.Type().Field(i)
			switch field.Name {
			case "Responses", "Segments", "Layout":
				if value, ok := responsesFromSlice(first.Field(i)); ok {
					return resultFromRecords(methods, raw, value, result.Err, false), result.Err
				}
			case "Err", "Error":
				fieldValue := first.Field(i)
				if fieldValue.IsValid() && !isNilValue(fieldValue) && fieldValue.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
					result.Err = toParseError(fieldValue.Interface().(error), len(raw))
				}
			}
		}
		if segment, ok := segmentFromStruct(first); ok {
			return resultFromRecords(methods, raw, responsesFromSegments([]Segment{segment}), result.Err, true), result.Err
		}
	}

	if first.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		result.Err = toParseError(first.Interface().(error), len(raw))
		return result, result.Err
	}

	result.Err = &ParseError{
		Kind:         ErrCallback,
		Offset:       len(raw),
		DetailOffset: len(raw),
		Message:      fmt.Sprintf("alternate parser returned unsupported type %s", first.Type()),
	}
	return result, result.Err
}

func resultWithError(methods []string, raw []byte, result ParseResult, perr *ParseError) ParseResult {
	if result.Methods == nil {
		result.Methods = append([]string(nil), methods...)
	}
	if result.Raw == nil {
		result.Raw = raw
	}
	if result.Strategy == "" {
		result.Strategy = "alternate"
	}
	if perr != nil {
		result.Err = perr
	}
	return result
}

func callbackFailure(methods []string, raw []byte, message string) ParseResult {
	return ParseResult{
		Strategy: "alternate",
		Methods:  append([]string(nil), methods...),
		Raw:      raw,
		Err: &ParseError{
			Kind:         ErrCallback,
			Offset:       len(raw),
			DetailOffset: len(raw),
			Message:      message,
		},
	}
}

func isNilValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func responsesFromSlice(value reflect.Value) ([]Response, bool) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Ptr) {
		if value.IsNil() {
			return nil, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return nil, false
	}
	switch {
	case value.Type().AssignableTo(reflect.TypeOf([]Response{})):
		return value.Convert(reflect.TypeOf([]Response{})).Interface().([]Response), true
	case value.Type().AssignableTo(reflect.TypeOf([]Segment{})):
		return responsesFromSegments(value.Convert(reflect.TypeOf([]Segment{})).Interface().([]Segment)), true
	}

	responses := make([]Response, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		segment, ok := segmentFromValue(value.Index(i))
		if !ok {
			return nil, false
		}
		responses = append(responses, responsesFromSegments([]Segment{segment})...)
	}
	return responses, true
}

func segmentFromStruct(value reflect.Value) (Segment, bool) {
	return segmentFromValue(value)
}

func segmentFromValue(value reflect.Value) (Segment, bool) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Ptr) {
		if value.IsNil() {
			return Segment{}, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return Segment{}, false
	}

	segment := Segment{RequestIndex: 0, Boundary: BoundaryUnknown}
	var haveStart, haveEnd bool
	if start, ok := intField(value, "Start", "Begin", "Offset", "From"); ok {
		segment.Start = start
		haveStart = true
	}
	if end, ok := intField(value, "End", "Stop", "Until", "To"); ok {
		segment.End = end
		haveEnd = true
	}
	if !haveStart || !haveEnd {
		if interval, ok := namedField(value, "Interval", "Range", "Span"); ok {
			if start, ok := intField(interval, "Start", "Begin", "Offset", "From"); ok {
				segment.Start = start
				haveStart = true
			}
			if end, ok := intField(interval, "End", "Stop", "Until", "To"); ok {
				segment.End = end
				haveEnd = true
			}
		}
	}
	if !haveStart || !haveEnd {
		return Segment{}, false
	}
	if request, ok := intField(value, "RequestIndex", "Request", "Owner", "RequestID", "RequestId"); ok {
		segment.RequestIndex = request
	}
	if status, ok := intField(value, "StatusCode", "Status", "Code"); ok {
		segment.StatusCode = status
	}
	if informational, ok := boolField(value, "Informational", "IsInformational"); ok {
		segment.Informational = informational
	}
	if boundary, ok := stringField(value, "Boundary", "BoundaryKind"); ok {
		segment.Boundary = BoundaryKind(boundary)
	}
	return segment, true
}

func namedField(value reflect.Value, names ...string) (reflect.Value, bool) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Ptr) {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	for _, name := range names {
		field, ok := value.Type().FieldByName(name)
		if !ok || field.PkgPath != "" {
			continue
		}
		return value.FieldByIndex(field.Index), true
	}
	return reflect.Value{}, false
}

func intField(value reflect.Value, names ...string) (int, bool) {
	field, ok := namedField(value, names...)
	if !ok {
		return 0, false
	}
	for field.IsValid() && (field.Kind() == reflect.Interface || field.Kind() == reflect.Ptr) {
		if field.IsNil() {
			return 0, false
		}
		field = field.Elem()
	}
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(field.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(field.Uint()), true
	default:
		return 0, false
	}
}

func boolField(value reflect.Value, names ...string) (bool, bool) {
	field, ok := namedField(value, names...)
	if !ok {
		return false, false
	}
	for field.IsValid() && (field.Kind() == reflect.Interface || field.Kind() == reflect.Ptr) {
		if field.IsNil() {
			return false, false
		}
		field = field.Elem()
	}
	if field.IsValid() && field.Kind() == reflect.Bool {
		return field.Bool(), true
	}
	return false, false
}

func stringField(value reflect.Value, names ...string) (string, bool) {
	field, ok := namedField(value, names...)
	if !ok {
		return "", false
	}
	for field.IsValid() && (field.Kind() == reflect.Interface || field.Kind() == reflect.Ptr) {
		if field.IsNil() {
			return "", false
		}
		field = field.Elem()
	}
	if field.IsValid() && field.Kind() == reflect.String {
		return field.String(), true
	}
	return "", false
}

func normalizeParseResult(result *ParseResult, methods []string, raw []byte) {
	if result.Strategy == "" {
		result.Strategy = "alternate"
	}
	if result.Methods == nil {
		result.Methods = append([]string(nil), methods...)
	}
	if result.Raw == nil {
		result.Raw = raw
	}
	for i := range result.Responses {
		resp := &result.Responses[i]
		resp.Index = i
		if resp.Boundary == "" {
			resp.Boundary = BoundaryUnknown
		}
		if resp.StatusCode >= 100 && resp.StatusCode <= 199 {
			resp.Informational = true
		}
		if resp.End == 0 {
			if resp.BodyEnd > 0 {
				resp.End = resp.BodyEnd
			} else if resp.HeaderEnd > 0 {
				resp.End = resp.HeaderEnd
			}
		}
		if resp.Start < 0 {
			resp.Start = 0
		}
		if resp.Start > len(raw) {
			resp.Start = len(raw)
		}
		if resp.End < resp.Start {
			resp.End = resp.Start
		}
		if resp.End > len(raw) {
			resp.End = len(raw)
		}
		if resp.HeaderEnd == 0 {
			resp.HeaderEnd = resp.Start
		}
		if resp.HeaderEnd < resp.Start {
			resp.HeaderEnd = resp.Start
		}
		if resp.HeaderEnd > resp.End {
			resp.HeaderEnd = resp.End
		}
		if resp.BodyStart == 0 {
			resp.BodyStart = resp.HeaderEnd
		}
		if resp.BodyStart < resp.Start {
			resp.BodyStart = resp.Start
		}
		if resp.BodyStart > resp.End {
			resp.BodyStart = resp.End
		}
		if resp.BodyEnd == 0 {
			resp.BodyEnd = resp.End
		}
		if resp.BodyEnd < resp.BodyStart {
			resp.BodyEnd = resp.BodyStart
		}
		if resp.BodyEnd > resp.End {
			resp.BodyEnd = resp.End
		}
		if resp.ExpectedEnd == 0 {
			resp.ExpectedEnd = -1
		}
		if resp.DeclaredLength == 0 && !hasHeader(resp.Headers, "Content-Length") {
			resp.DeclaredLength = -1
		}
		if resp.Raw == nil && resp.Start <= resp.End {
			resp.Raw = raw[resp.Start:resp.End]
		}
		if resp.Body == nil && resp.BodyStart <= resp.BodyEnd {
			resp.Body = raw[resp.BodyStart:resp.BodyEnd]
		}
	}
	if len(result.Responses) > 0 {
		intervalsOnly := true
		for _, resp := range result.Responses {
			if resp.HeaderEnd != resp.Start || resp.BodyStart != resp.Start {
				intervalsOnly = false
				break
			}
		}
		if intervalsOnly {
			result.Minimal = true
		}
	}
}

func hasHeader(headers []Header, name string) bool {
	for _, h := range headers {
		if equalFoldASCII(h.Name, name) {
			return true
		}
	}
	return false
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
