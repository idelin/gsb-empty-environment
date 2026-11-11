package llhls

import (
	"bufio"
	"strconv"
	"strings"
	"testing"
)

type parsedPart struct {
	uri         string
	duration    float64
	independent bool
}

type parsedSegment struct {
	extinf float64
	uri    string
}

type parsedPlaylist struct {
	lines         []string
	version       int
	target        int
	mediaSequence uint64
	partTarget    int
	serverControl string
	skip          int
	hasSkip       bool
	segments      []parsedSegment
	parts         []parsedPart
	preloadURI    string
}

func parsePlaylist(t *testing.T, body string) parsedPlaylist {
	t.Helper()
	pl := parsedPlaylist{lines: strings.Split(strings.TrimRight(body, "\n"), "\n")}
	if len(pl.lines) == 0 || pl.lines[0] != "#EXTM3U" {
		t.Fatalf("playlist must start with #EXTM3U, got %q", body)
	}

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "#EXT-X-VERSION:"):
			pl.version = atoiField(t, line)
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			pl.target = atoiField(t, line)
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			pl.mediaSequence = uint64(atoiField(t, line))
		case strings.HasPrefix(line, "#EXT-X-PART-INF:"):
			pl.partTarget = atoiNamed(t, line, "PART-TARGET")
		case strings.HasPrefix(line, "#EXT-X-SERVER-CONTROL:"):
			pl.serverControl = line
		case strings.HasPrefix(line, "#EXT-X-SKIP:"):
			pl.hasSkip = true
			pl.skip = atoiNamed(t, line, "SKIPPED-SEGMENTS")
		case strings.HasPrefix(line, "#EXTINF:"):
			pl.segments = append(pl.segments, parsedSegment{
				extinf: extinfoAtou(t, line),
			})
		case strings.HasPrefix(line, "#EXT-X-PART:"):
			pl.parts = append(pl.parts, parsedPart{
				uri:         atosNamed(t, line, "URI"),
				duration:    atofNamed(t, line, "DURATION"),
				independent: strings.Contains(line, "INDEPENDENT=YES"),
			})
		case strings.HasPrefix(line, "#EXT-X-PRELOAD-HINT:"):
			pl.preloadURI = atosNamed(t, line, "URI")
		case strings.HasPrefix(line, "#"):
			// Other tags are tolerated.
		default:
			if len(pl.segments) > 0 && pl.segments[len(pl.segments)-1].uri == "" {
				pl.segments[len(pl.segments)-1].uri = line
			}
		}
	}
	return pl
}

func atoiField(t *testing.T, line string) int {
	t.Helper()
	v, err := strconv.Atoi(strings.SplitN(line, ":", 2)[1])
	if err != nil {
		t.Fatalf("bad integer in %q: %v", line, err)
	}
	return v
}

func namedValue(line, key string) (string, bool) {
	for _, field := range strings.Split(strings.SplitN(line, ":", 2)[1], ",") {
		kv := strings.SplitN(field, "=", 2)
		if kv[0] == key {
			return strings.Trim(kv[1], "\""), true
		}
	}
	return "", false
}

func atoiNamed(t *testing.T, line, key string) int {
	t.Helper()
	v, ok := namedValue(line, key)
	if !ok {
		t.Fatalf("missing %s in %q", key, line)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("bad %s in %q: %v", key, line, err)
	}
	return n
}

func atofNamed(t *testing.T, line, key string) float64 {
	t.Helper()
	v, ok := namedValue(line, key)
	if !ok {
		t.Fatalf("missing %s in %q", key, line)
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		t.Fatalf("bad %s in %q: %v", key, line, err)
	}
	return f
}

func atosNamed(t *testing.T, line, key string) string {
	t.Helper()
	v, ok := namedValue(line, key)
	if !ok {
		t.Fatalf("missing %s in %q", key, line)
	}
	return v
}

func extinfoAtou(t *testing.T, line string) float64 {
	t.Helper()
	value := strings.SplitN(line, ":", 2)[1]
	if i := strings.IndexByte(value, ','); i >= 0 {
		value = value[:i]
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("bad EXTINF in %q: %v", line, err)
	}
	return f
}
