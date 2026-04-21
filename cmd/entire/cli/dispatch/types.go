package dispatch

import (
	"errors"
	"time"
)

var errDispatchMissingMarkdown = errors.New("dispatch generation returned no markdown")

// Dispatch is the rendered, in-memory representation returned from either server or local mode.
type Dispatch struct {
	Window        Window
	CoveredRepos  []string
	Repos         []RepoGroup
	Totals        Totals
	Warnings      Warnings
	GeneratedText string

	RequestedGenerate bool
	Generated         bool
}

type Window struct {
	NormalizedSince   time.Time
	NormalizedUntil   time.Time
	FirstCheckpointAt time.Time
	LastCheckpointAt  time.Time
}

type RepoGroup struct {
	FullName string
	Sections []Section
}

type Section struct {
	Label   string
	Bullets []Bullet
}

type Bullet struct {
	CheckpointID string
	Text         string
	Source       string
	Branch       string
	CreatedAt    time.Time
	Labels       []string
}

type Totals struct {
	Checkpoints         int
	UsedCheckpointCount int
	Branches            int
	FilesTouched        int
}

type Warnings struct {
	AccessDeniedCount  int
	PendingCount       int
	FailedCount        int
	UnknownCount       int
	UncategorizedCount int
}
