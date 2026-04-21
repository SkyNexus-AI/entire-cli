package cli

import (
	"testing"
	"time"
)

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

func TestComputeContributionStats_Throughput(t *testing.T) {
	t.Parallel()
	cps := []userCheckpoint{
		{InputTokens: intPtr(5000), OutputTokens: intPtr(3000)},
		{InputTokens: intPtr(2000), OutputTokens: intPtr(1000)},
	}
	stats := computeContributionStats(cps, nil)

	// avg tokens = (8000+3000)/2 = 5500, /1000 = 5.5k
	want := 5.5
	if diff := stats.Throughput - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("Throughput = %.2f, want %.2f", stats.Throughput, want)
	}
}

func TestComputeContributionStats_ThroughputSkipsNilTokens(t *testing.T) {
	t.Parallel()
	cps := []userCheckpoint{
		{InputTokens: intPtr(4000), OutputTokens: intPtr(2000)},
		{}, // no tokens — should be excluded from average
	}
	stats := computeContributionStats(cps, nil)

	want := 6.0 // 6000/1 / 1000
	if diff := stats.Throughput - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("Throughput = %.2f, want %.2f", stats.Throughput, want)
	}
}

func TestComputeContributionStats_IterationUsesSessionCount(t *testing.T) {
	t.Parallel()
	cps := []userCheckpoint{
		{SessionCount: intPtr(2), Steps: intPtr(10)},
		{SessionCount: intPtr(4), Steps: intPtr(20)},
	}
	stats := computeContributionStats(cps, nil)

	// Iteration should be avg(session_count) = (2+4)/2 = 3.0, NOT avg(steps)
	want := 3.0
	if diff := stats.Iteration - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("Iteration = %.2f, want %.2f (should use session_count, not steps)", stats.Iteration, want)
	}
}

func TestComputeContributionStats_ContinuityUsesMaxSteps(t *testing.T) {
	t.Parallel()
	cps := []userCheckpoint{
		{Steps: intPtr(30)},
		{Steps: intPtr(60)},
		{Steps: intPtr(15)},
	}
	stats := computeContributionStats(cps, nil)

	// max(steps) = 60, * 2 / 60 = 2.0 hours
	want := 2.0
	if diff := stats.ContinuityH - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("ContinuityH = %.2f, want %.2f", stats.ContinuityH, want)
	}
}

func TestComputeContributionStats_NilFieldsDefault(t *testing.T) {
	t.Parallel()
	cps := []userCheckpoint{{}} // all nil pointer fields
	stats := computeContributionStats(cps, nil)

	if stats.Iteration != 1.0 {
		t.Errorf("Iteration = %.2f, want 1.0 (nil session_count defaults to 1)", stats.Iteration)
	}
	// max(steps) defaults to 1, continuity = 1*2/60
	wantH := 2.0 / 60.0
	if diff := stats.ContinuityH - wantH; diff > 0.001 || diff < -0.001 {
		t.Errorf("ContinuityH = %.4f, want %.4f", stats.ContinuityH, wantH)
	}
	if stats.Throughput != 0 {
		t.Errorf("Throughput = %.2f, want 0 (no token data)", stats.Throughput)
	}
}

func TestComputeContributionStats_Empty(t *testing.T) {
	t.Parallel()
	stats := computeContributionStats(nil, nil)
	if stats.Tasks != 0 || stats.Throughput != 0 || stats.Iteration != 0 {
		t.Errorf("empty checkpoints should return zero stats, got %+v", stats)
	}
}

func TestComputeStreaks_Basic(t *testing.T) {
	t.Parallel()
	today := time.Now().Local()
	dates := []string{
		today.AddDate(0, 0, -2).Format(time.RFC3339),
		today.AddDate(0, 0, -1).Format(time.RFC3339),
		today.Format(time.RFC3339),
	}
	longest, current := computeStreaks(dates)
	if longest != 3 {
		t.Errorf("longest = %d, want 3", longest)
	}
	if current != 3 {
		t.Errorf("current = %d, want 3", current)
	}
}

func TestComputeStreaks_DedupsSameDay(t *testing.T) {
	t.Parallel()
	today := time.Now().Local()
	ts := today.Format(time.RFC3339)
	dates := []string{ts, ts, ts}

	longest, current := computeStreaks(dates)
	if longest != 1 {
		t.Errorf("longest = %d, want 1 (deduped)", longest)
	}
	if current != 1 {
		t.Errorf("current = %d, want 1", current)
	}
}

func TestComputeStreaks_InvalidTimestamps(t *testing.T) {
	t.Parallel()
	dates := []string{"not-a-date", "also-bad", ""}
	longest, current := computeStreaks(dates)
	if longest != 0 || current != 0 {
		t.Errorf("invalid timestamps should return 0,0; got %d,%d", longest, current)
	}
}

func TestComputeStreaks_CurrentStartsFromYesterday(t *testing.T) {
	t.Parallel()
	today := time.Now().Local()
	// Activity yesterday and day-before, but not today
	dates := []string{
		today.AddDate(0, 0, -2).Format(time.RFC3339),
		today.AddDate(0, 0, -1).Format(time.RFC3339),
	}
	_, current := computeStreaks(dates)
	if current != 2 {
		t.Errorf("current = %d, want 2 (should start from yesterday)", current)
	}
}

func TestComputeStreaks_NoCurrentIfGap(t *testing.T) {
	t.Parallel()
	today := time.Now().Local()
	// Activity 3 days ago only
	dates := []string{
		today.AddDate(0, 0, -3).Format(time.RFC3339),
	}
	_, current := computeStreaks(dates)
	if current != 0 {
		t.Errorf("current = %d, want 0 (gap of 2 days)", current)
	}
}

func TestNormalizeAgentString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"Claude Code", "claude"},
		{"claude-code", "claude"},
		{"claude", "claude"},
		{"Gemini CLI", "gemini"},
		{"gemini", "gemini"},
		{"copilot-cli", "copilot"},
		{"Copilot CLI", "copilot"},
		{"OpenCode", "opencode"},
		{"open-code", "opencode"},
		{"factoryai-droid", "droid"},
		{"Factory AI Droid", "droid"},
		{"FactoryAIDroid", "droid"},
		{"codex", "codex"},
		{"pi", "pi"},
		{"cursor", "cursor"},
		{"kiro", "kiro"},
		{"amp", "amp"},
		{"some-unknown-agent", "unknown"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := normalizeAgentString(tt.input)
			if got != tt.want {
				t.Errorf("normalizeAgentString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGroupCommitsByDay_SortsNewestFirst(t *testing.T) {
	t.Parallel()
	commits := []userCommit{
		{CommitSHA: "aaa", CommitDate: strPtr("2026-01-10T12:00:00Z")},
		{CommitSHA: "bbb", CommitDate: strPtr("2026-01-12T08:00:00Z")},
		{CommitSHA: "ccc", CommitDate: strPtr("2026-01-11T15:00:00Z")},
	}
	days := groupCommitsByDay(commits)

	if len(days) != 3 {
		t.Fatalf("got %d day groups, want 3", len(days))
	}
	// Expect newest first: 2026-01-12, 2026-01-11, 2026-01-10
	if days[0].Commits[0].CommitSHA != "bbb" {
		t.Errorf("first day should contain commit bbb (2026-01-12)")
	}
	if days[1].Commits[0].CommitSHA != "ccc" {
		t.Errorf("second day should contain commit ccc (2026-01-11)")
	}
	if days[2].Commits[0].CommitSHA != "aaa" {
		t.Errorf("third day should contain commit aaa (2026-01-10)")
	}
}

func TestGroupCommitsByDay_UnknownDatesLast(t *testing.T) {
	t.Parallel()
	commits := []userCommit{
		{CommitSHA: "bad", CommitDate: nil},
		{CommitSHA: "good", CommitDate: strPtr("2026-01-15T10:00:00Z")},
	}
	days := groupCommitsByDay(commits)

	if len(days) != 2 {
		t.Fatalf("got %d day groups, want 2", len(days))
	}
	if days[0].Date == dateUnknown {
		t.Errorf("unknown-date commits should sort last, but appeared first")
	}
	if days[1].Date != dateUnknown {
		t.Errorf("unknown-date commits should be last group, got %q", days[1].Date)
	}
}

func TestGroupCommitsByDay_UnparseableDateGoesToUnknown(t *testing.T) {
	t.Parallel()
	commits := []userCommit{
		{CommitSHA: "x", CommitDate: strPtr("not-a-date")},
	}
	days := groupCommitsByDay(commits)

	if len(days) != 1 || days[0].Date != dateUnknown {
		t.Errorf("unparseable date should be grouped under %q", dateUnknown)
	}
}

func TestParseFlexibleTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2026-01-15T10:00:00Z", false},
		{"2026-01-15T10:00:00.123456789Z", false},
		{"2026-01-15T10:00:00+02:00", false},
		{"not-a-date", true},
		{"", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			_, err := parseFlexibleTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseFlexibleTime(%q) err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestFormatCommitDate(t *testing.T) {
	t.Parallel()
	now := time.Now().Local()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	older := now.AddDate(0, 0, -5).Format("2006-01-02")
	future := now.AddDate(0, 0, 2).Format("2006-01-02")

	tests := []struct {
		name     string
		input    string
		contains string
		excludes string
	}{
		{"today", today, "(today)", ""},
		{"yesterday", yesterday, "(yesterday)", ""},
		{"older", older, "", "(today)"},
		{"future", future, "", "(today)"},
		{"invalid", "bad-date", "bad-date", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatCommitDate(tt.input)
			if tt.contains != "" && !containsStr(got, tt.contains) {
				t.Errorf("formatCommitDate(%q) = %q, want to contain %q", tt.input, got, tt.contains)
			}
			if tt.excludes != "" && containsStr(got, tt.excludes) {
				t.Errorf("formatCommitDate(%q) = %q, should not contain %q", tt.input, got, tt.excludes)
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && stringContains(s, sub)))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
