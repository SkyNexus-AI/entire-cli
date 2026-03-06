package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// perfStep represents a single timed step within a perf span.
type perfStep struct {
	Name       string
	DurationMs int64
	Error      bool
}

// perfEntry represents a parsed performance trace log entry.
type perfEntry struct {
	Op         string
	DurationMs int64
	Error      bool
	Time       time.Time
	Steps      []perfStep
}

// parsePerfEntry parses a JSON log line into a perfEntry.
// Returns nil if the line is not valid JSON or is not a perf entry (msg != "perf").
func parsePerfEntry(line string) *perfEntry {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil
	}

	// Check that msg == "perf"
	var msg string
	if msgRaw, ok := raw["msg"]; !ok {
		return nil
	} else if err := json.Unmarshal(msgRaw, &msg); err != nil || msg != "perf" {
		return nil
	}

	entry := &perfEntry{}

	// Extract op
	if opRaw, ok := raw["op"]; ok {
		if err := json.Unmarshal(opRaw, &entry.Op); err != nil {
			return nil
		}
	}

	// Extract duration_ms
	if dRaw, ok := raw["duration_ms"]; ok {
		if err := json.Unmarshal(dRaw, &entry.DurationMs); err != nil {
			return nil
		}
	}

	// Extract error flag
	if errRaw, ok := raw["error"]; ok {
		if err := json.Unmarshal(errRaw, &entry.Error); err != nil {
			return nil
		}
	}

	// Extract time
	if tRaw, ok := raw["time"]; ok {
		var ts string
		if err := json.Unmarshal(tRaw, &ts); err == nil {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				entry.Time = parsed
			}
		}
	}

	// Extract steps by finding keys matching "steps.*_ms"
	stepDurations := make(map[string]int64)
	stepErrors := make(map[string]bool)

	for key, val := range raw {
		if strings.HasPrefix(key, "steps.") && strings.HasSuffix(key, "_ms") {
			name := strings.TrimPrefix(key, "steps.")
			name = strings.TrimSuffix(name, "_ms")

			var ms int64
			if err := json.Unmarshal(val, &ms); err == nil {
				stepDurations[name] = ms
			}
		} else if strings.HasPrefix(key, "steps.") && strings.HasSuffix(key, "_err") {
			name := strings.TrimPrefix(key, "steps.")
			name = strings.TrimSuffix(name, "_err")

			var errFlag bool
			if err := json.Unmarshal(val, &errFlag); err == nil {
				stepErrors[name] = errFlag
			}
		}
	}

	// Build steps slice sorted alphabetically by name
	steps := make([]perfStep, 0, len(stepDurations))
	for name, ms := range stepDurations {
		steps = append(steps, perfStep{
			Name:       name,
			DurationMs: ms,
			Error:      stepErrors[name],
		})
	}
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].Name < steps[j].Name
	})

	entry.Steps = steps

	return entry
}

// collectPerfEntries reads a JSONL log file and returns the last N perf entries,
// ordered newest first. If hookFilter is non-empty, only entries with a matching
// Op field are included.
func collectPerfEntries(logFile string, last int, hookFilter string) ([]perfEntry, error) {
	f, err := os.Open(logFile) //nolint:gosec // logFile is a CLI-resolved path, not user-supplied input
	if err != nil {
		return nil, fmt.Errorf("opening perf log: %w", err)
	}
	defer f.Close()

	var entries []perfEntry

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		entry := parsePerfEntry(scanner.Text())
		if entry == nil {
			continue
		}
		if hookFilter != "" && entry.Op != hookFilter {
			continue
		}
		entries = append(entries, *entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading perf log: %w", err)
	}

	// Take the last N entries
	if len(entries) > last {
		entries = entries[len(entries)-last:]
	}

	// Reverse so newest entries are first
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return entries, nil
}

// renderPerfEntries writes a formatted table of perf entries to w.
// If entries is empty, it prints a help message about enabling perf traces.
func renderPerfEntries(w io.Writer, entries []perfEntry) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No perf entries found.")
		fmt.Fprintln(w, `Perf traces are logged at DEBUG level. Make sure ENTIRE_LOG_LEVEL=DEBUG is set`)
		fmt.Fprintln(w, `in your shell profile, or set log_level to "DEBUG" in .entire/settings.json.`)
		return
	}

	for i, entry := range entries {
		if i > 0 {
			fmt.Fprintln(w)
		}

		// Header line: op  duration  [timestamp]
		header := fmt.Sprintf("%s  %dms", entry.Op, entry.DurationMs)
		if !entry.Time.IsZero() {
			header += "  " + entry.Time.Format(time.RFC3339)
		}
		fmt.Fprintln(w, header)
		fmt.Fprintln(w)

		if len(entry.Steps) == 0 {
			continue
		}

		// Compute max step name width (at least len("STEP"))
		nameWidth := len("STEP")
		for _, s := range entry.Steps {
			if len(s.Name) > nameWidth {
				nameWidth = len(s.Name)
			}
		}

		// Column header
		fmt.Fprintf(w, "  %-*s  %8s\n", nameWidth, "STEP", "DURATION")

		// Step rows
		for _, s := range entry.Steps {
			dur := fmt.Sprintf("%dms", s.DurationMs)
			line := fmt.Sprintf("  %-*s  %8s", nameWidth, s.Name, dur)
			if s.Error {
				line += "  x"
			}
			fmt.Fprintln(w, line)
		}
	}
}
