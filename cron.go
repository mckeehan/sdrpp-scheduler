package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpr represents a parsed 5-field cron expression.
// Fields: minute hour day-of-month month day-of-week
type CronExpr struct {
	minutes     []int // 0-59
	hours       []int // 0-23
	daysOfMonth []int // 1-31
	months      []int // 1-12
	daysOfWeek  []int // 0-6 (0=Sunday)
}

// ParseCron parses a standard 5-field cron expression.
// Supports: numbers, ranges (a-b), lists (a,b,c), steps (*/n, a-b/n), wildcard (*).
// Special aliases: @hourly @daily @midnight @weekly @monthly
func ParseCron(expr string) (*CronExpr, error) {
	switch strings.TrimSpace(expr) {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@monthly":
		expr = "0 0 1 * *"
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected 5 fields, got %d in %q", len(fields), expr)
	}

	c := &CronExpr{}
	var err error

	if c.minutes, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("minutes field: %w", err)
	}
	if c.hours, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("hours field: %w", err)
	}
	if c.daysOfMonth, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("day-of-month field: %w", err)
	}
	if c.months, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("months field: %w", err)
	}
	if c.daysOfWeek, err = parseField(fields[4], 0, 6); err != nil {
		return nil, fmt.Errorf("day-of-week field: %w", err)
	}

	return c, nil
}

// Next returns the next time this cron expression fires after `from`.
// It advances field by field: month → day → hour → minute, rolling over
// as needed. This avoids the infinite-loop edge-case of time-based stepping.
func (c *CronExpr) Next(from time.Time) time.Time {
	// Start searching from the next whole minute after `from`.
	t := from.Add(time.Minute).Truncate(time.Minute)

	// Bound the search to avoid pathological cases (e.g. "0 0 31 2 *").
	limit := from.AddDate(5, 0, 0)

	// We track whether we just rolled a field; when we roll a larger field
	// we must re-check all smaller fields from their first valid value.
SEARCH:
	for t.Before(limit) {
		// ── Month ────────────────────────────────────────────────────────
		if !inSlice(c.months, int(t.Month())) {
			// Skip to the 1st of the next valid month.
			t = nextValidMonth(t, c.months)
			if t.IsZero() {
				return time.Time{}
			}
			continue SEARCH
		}

		// ── Day ──────────────────────────────────────────────────────────
		// Standard cron semantics: if BOTH dom and dow are non-wildcard,
		// the day matches when EITHER condition is true (OR semantics).
		// If only one is non-wildcard, only that condition applies.
		domWild := len(c.daysOfMonth) == 31 // field was "*"
		dowWild := len(c.daysOfWeek) == 7   // field was "*"

		var dayMatch bool
		switch {
		case domWild && dowWild:
			dayMatch = true
		case domWild:
			dayMatch = inSlice(c.daysOfWeek, int(t.Weekday()))
		case dowWild:
			dayMatch = inSlice(c.daysOfMonth, t.Day())
		default:
			// Both restricted: OR semantics.
			dayMatch = inSlice(c.daysOfMonth, t.Day()) || inSlice(c.daysOfWeek, int(t.Weekday()))
		}

		if !dayMatch {
			// Advance to start of next day; recheck month.
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue SEARCH
		}

		// ── Hour ─────────────────────────────────────────────────────────
		if !inSlice(c.hours, t.Hour()) {
			next := firstValueGTE(c.hours, t.Hour())
			if next < 0 {
				// No valid hour today; go to next day.
				t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			} else {
				// Jump to first matching hour, reset minute to zero.
				t = time.Date(t.Year(), t.Month(), t.Day(), next, 0, 0, 0, t.Location())
			}
			continue SEARCH
		}

		// ── Minute ───────────────────────────────────────────────────────
		if !inSlice(c.minutes, t.Minute()) {
			next := firstValueGTE(c.minutes, t.Minute())
			if next < 0 {
				// No valid minute this hour; go to next hour, reset minute.
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			} else {
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), next, 0, 0, t.Location())
			}
			continue SEARCH
		}

		// All fields matched.
		return t
	}

	return time.Time{} // no match within the search window
}

// NextCronTime parses expr and returns the next time it fires after now.
// Returns zero time on parse error.
func NextCronTime(expr string) time.Time {
	c, err := ParseCron(expr)
	if err != nil {
		return time.Time{}
	}
	return c.Next(time.Now())
}

// ── helpers ──────────────────────────────────────────────────────────────────

// parseField expands a single cron field string into a sorted slice of ints.
func parseField(field string, min, max int) ([]int, error) {
	seen := make(map[int]bool)
	var result []int

	for _, part := range strings.Split(field, ",") {
		values, err := parseFieldPart(part, min, max)
		if err != nil {
			return nil, err
		}
		for _, v := range values {
			if !seen[v] {
				seen[v] = true
				result = append(result, v)
			}
		}
	}

	// Simple insertion sort (fields are small).
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result, nil
}

// parseFieldPart handles one comma-separated part: *, N, N-M, */step, N-M/step.
func parseFieldPart(part string, min, max int) ([]int, error) {
	step := 1
	if idx := strings.Index(part, "/"); idx >= 0 {
		s, err := strconv.Atoi(part[idx+1:])
		if err != nil || s <= 0 {
			return nil, fmt.Errorf("invalid step in %q", part)
		}
		step = s
		part = part[:idx]
	}

	var low, high int

	switch {
	case part == "*":
		low, high = min, max

	case strings.Contains(part, "-"):
		idx := strings.Index(part, "-")
		a, err1 := strconv.Atoi(part[:idx])
		b, err2 := strconv.Atoi(part[idx+1:])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid range %q", part)
		}
		low, high = a, b

	default:
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid value %q", part)
		}
		if v < min || v > max {
			return nil, fmt.Errorf("value %d out of range [%d, %d]", v, min, max)
		}
		return []int{v}, nil
	}

	if low < min || high > max || low > high {
		return nil, fmt.Errorf("range %d-%d out of bounds [%d, %d]", low, high, min, max)
	}

	var result []int
	for i := low; i <= high; i += step {
		result = append(result, i)
	}
	return result, nil
}

// inSlice returns true if v is present in s.
func inSlice(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// firstValueGTE returns the first value in sorted slice s that is >= v,
// or -1 if no such value exists.
func firstValueGTE(s []int, v int) int {
	for _, x := range s {
		if x >= v {
			return x
		}
	}
	return -1
}

// nextValidMonth returns the first day of the next month in c.months after t.
// Returns zero time if no valid month is found within 5 years.
func nextValidMonth(t time.Time, months []int) time.Time {
	// Advance month by month until we hit a valid one.
	// Use AddDate to correctly handle month-length differences.
	candidate := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
	limit := t.AddDate(5, 0, 0)
	for candidate.Before(limit) {
		if inSlice(months, int(candidate.Month())) {
			return candidate
		}
		candidate = time.Date(candidate.Year(), candidate.Month()+1, 1, 0, 0, 0, 0, candidate.Location())
	}
	return time.Time{}
}
