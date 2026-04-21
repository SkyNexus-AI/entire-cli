package cli

// API response types for the /api/v1/stats/* endpoints used by `entire stats`.

// userCheckpoint represents a single checkpoint returned by the checkpoints API.
type userCheckpoint struct {
	CheckpointID string  `json:"checkpoint_id"`
	CommitSHA    string  `json:"commit_sha"`
	CommitMsg    string  `json:"commit_message"`
	CommitDate   string  `json:"commit_date"`
	Additions    int     `json:"additions"`
	Deletions    int     `json:"deletions"`
	FilesChanged int     `json:"files_changed"`
	RepoFullName string  `json:"repo_full_name"`
	Branch       string  `json:"branch"`
	IsPrivate    bool    `json:"is_private"`
	Agent        *string `json:"agent"`
	Steps        *int    `json:"steps"`
	SessionCount *int    `json:"session_count"`
	InputTokens  *int    `json:"input_tokens"`
	OutputTokens *int    `json:"output_tokens"`
}

// userCheckpointsResponse is the API response for GET /api/v1/stats/checkpoints.
type userCheckpointsResponse struct {
	Checkpoints []userCheckpoint `json:"checkpoints"`
	StreakDates []string         `json:"streakDates"`
	Timeframe   string           `json:"timeframe"`
	UpdatedAt   string           `json:"updated_at"`
}

// userCommitCheckpoint is checkpoint info nested inside a commit.
type userCommitCheckpoint struct {
	CheckpointID string   `json:"checkpoint_id"`
	Prompt       *string  `json:"prompt"`
	Agent        string   `json:"agent"`
	Agents       []string `json:"agents"`
	SessionCount int      `json:"session_count"`
	TotalSteps   int      `json:"total_steps"`
}

// userCommit represents a single commit returned by the commits API.
type userCommit struct {
	CommitSHA              string                 `json:"commit_sha"`
	CommitMsg              *string                `json:"commit_message"`
	CommitAuthorUsername   *string                `json:"commit_author_username"`
	CommitDate             *string                `json:"commit_date"`
	Additions              int                    `json:"additions"`
	Deletions              int                    `json:"deletions"`
	FilesChanged           int                    `json:"files_changed"`
	Checkpoints            []userCommitCheckpoint `json:"checkpoints"`
	RepoFullName           string                 `json:"repo_full_name"`
	IsPrivate              bool                   `json:"is_private"`
	CheckpointRepoFullName *string                `json:"checkpoint_repo_full_name"`
}

// userCommitsResponse is the API response for GET /api/v1/stats/commits.
type userCommitsResponse struct {
	Commits   []userCommit `json:"commits"`
	Timeframe string       `json:"timeframe"`
	UpdatedAt string       `json:"updated_at"`
}

// Computed types used for rendering.

type contributionStats struct {
	Tasks         int
	Throughput    float64 // avg tokens/checkpoint in thousands
	Iteration     float64 // avg session_count per checkpoint
	ContinuityH   float64 // peak session length in hours
	Streak        int     // longest consecutive days
	CurrentStreak int     // current streak from today
}

type repoContribution struct {
	Repo   string
	Total  int
	Agents map[string]int // agentID -> checkpoint count
}

// hourlyPoint is a single data point for the contribution chart.
type hourlyPoint struct {
	Date    string // "2006-01-02"
	Hour    int
	Value   int // step count
	AgentID string
}

// commitDay groups commits by date for display.
type commitDay struct {
	Date    string
	Commits []userCommit
}
