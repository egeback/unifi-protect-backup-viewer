// Package filenameparse extracts camera name, start and end timestamps from
// the filenames UniFi Protect's NAS export produces, e.g.:
//
//	"Baksidan - Frrdet 8-8-2026, 2.04.06pm GMT+2 - 8-8-2026, 2.04.27pm GMT+2.mp4"
//
// The part before the first date is the camera's display name as written by
// the SMB export, which strips any non-ASCII character (å/ä/ö) entirely
// rather than transliterating it — that's why "Förrådet" becomes "Frrdet".
package filenameparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var filenamePattern = regexp.MustCompile(
	`^(.+?) (\d{1,2})-(\d{1,2})-(\d{4}), (\d{1,2})\.(\d{2})\.(\d{2})(am|pm) GMT([+-]\d+) - ` +
		`(\d{1,2})-(\d{1,2})-(\d{4}), (\d{1,2})\.(\d{2})\.(\d{2})(am|pm) GMT([+-]\d+)\.mp4$`,
)

// Result holds the metadata recovered from a clip filename.
type Result struct {
	CameraName string
	Start      time.Time
	End        time.Time
}

// Parse extracts camera name and start/end timestamps from a clip's base
// filename (not a full path). It returns an error if the filename doesn't
// match the expected UniFi Protect NAS export format.
func Parse(filename string) (Result, error) {
	m := filenamePattern.FindStringSubmatch(filename)
	if m == nil {
		return Result{}, fmt.Errorf("filename %q does not match UniFi Protect export format", filename)
	}

	start, err := parseTimestamp(m[2:10])
	if err != nil {
		return Result{}, fmt.Errorf("parsing start timestamp in %q: %w", filename, err)
	}
	end, err := parseTimestamp(m[10:18])
	if err != nil {
		return Result{}, fmt.Errorf("parsing end timestamp in %q: %w", filename, err)
	}

	return Result{
		CameraName: strings.TrimSpace(m[1]),
		Start:      start,
		End:        end,
	}, nil
}

// parseTimestamp consumes the 8 capture groups for one timestamp:
// month, day, year, hour(1-12), minute, second, am/pm, gmt-offset.
func parseTimestamp(g []string) (time.Time, error) {
	month, _ := strconv.Atoi(g[0])
	day, _ := strconv.Atoi(g[1])
	year, _ := strconv.Atoi(g[2])
	hour, _ := strconv.Atoi(g[3])
	minute, _ := strconv.Atoi(g[4])
	second, _ := strconv.Atoi(g[5])
	ampm := g[6]
	offsetHours, err := strconv.Atoi(g[7])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid GMT offset %q: %w", g[7], err)
	}

	if hour < 1 || hour > 12 {
		return time.Time{}, fmt.Errorf("hour %d out of 1-12 range", hour)
	}
	hour24 := hour % 12
	if ampm == "pm" {
		hour24 += 12
	}

	loc := time.FixedZone(fmt.Sprintf("GMT%+d", offsetHours), offsetHours*3600)
	return time.Date(year, time.Month(month), day, hour24, minute, second, 0, loc), nil
}

// NormalizeCameraName strips every non-ASCII-letter/digit rune and
// lowercases the rest, so names can be compared across the mangled
// filename form and the real name reported by the Protect API — e.g.
// "Baksidan - Förrådet" and "Baksidan - Frrdet" both normalize to
// "baksidanfrrdet".
func NormalizeCameraName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r > 127 {
			continue // dropped entirely by the SMB export, so drop here too
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	return b.String()
}
