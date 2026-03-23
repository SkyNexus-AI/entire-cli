package checkpoint

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/validation"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// writeCommittedMain writes metadata entries to the /main ref.
// This includes session metadata, prompts, and content hash — but NOT the
// raw transcript (full.jsonl), which goes to /full/current.
func (s *V2GitStore) writeCommittedMain(ctx context.Context, opts WriteCommittedOptions) error {
	if err := validateWriteOpts(opts); err != nil {
		return err
	}

	refName := plumbing.ReferenceName(paths.V2MainRefName)
	if err := s.ensureRef(refName); err != nil {
		return fmt.Errorf("failed to ensure /main ref: %w", err)
	}

	parentHash, rootTreeHash, err := s.getRefState(refName)
	if err != nil {
		return err
	}

	basePath := opts.CheckpointID.Path() + "/"
	checkpointPath := opts.CheckpointID.Path()

	// Read existing entries at this checkpoint's shard path
	entries, err := s.gs.flattenCheckpointEntries(rootTreeHash, checkpointPath)
	if err != nil {
		return err
	}

	// Build main session entries (metadata, prompts, content hash — no transcript)
	if err := s.writeMainCheckpointEntries(ctx, opts, basePath, entries); err != nil {
		return err
	}

	// Splice entries into root tree
	newTreeHash, err := s.gs.spliceCheckpointSubtree(rootTreeHash, opts.CheckpointID, basePath, entries)
	if err != nil {
		return err
	}

	commitMsg := fmt.Sprintf("Checkpoint: %s\n", opts.CheckpointID)
	return s.updateRef(refName, newTreeHash, parentHash, commitMsg, opts.AuthorName, opts.AuthorEmail)
}

// writeMainCheckpointEntries orchestrates writing session data to the /main ref.
// It mirrors GitStore.writeStandardCheckpointEntries but excludes raw transcript blobs.
func (s *V2GitStore) writeMainCheckpointEntries(ctx context.Context, opts WriteCommittedOptions, basePath string, entries map[string]object.TreeEntry) error {
	// Read existing summary to get current session count
	var existingSummary *CheckpointSummary
	metadataPath := basePath + paths.MetadataFileName
	if entry, exists := entries[metadataPath]; exists {
		existing, err := readJSONFromBlob[CheckpointSummary](s.repo, entry.Hash)
		if err == nil {
			existingSummary = existing
		}
	}

	// Determine session index
	sessionIndex := s.gs.findSessionIndex(ctx, basePath, existingSummary, entries, opts.SessionID)

	// Write session files (metadata, prompts, content hash — no transcript)
	sessionPath := fmt.Sprintf("%s%d/", basePath, sessionIndex)
	sessionFilePaths, err := s.writeMainSessionToSubdirectory(opts, sessionPath, entries)
	if err != nil {
		return err
	}

	// Build the sessions array
	var sessions []SessionFilePaths
	if existingSummary != nil {
		sessions = make([]SessionFilePaths, max(len(existingSummary.Sessions), sessionIndex+1))
		copy(sessions, existingSummary.Sessions)
	} else {
		sessions = make([]SessionFilePaths, 1)
	}
	sessions[sessionIndex] = sessionFilePaths

	// Write root CheckpointSummary
	return s.gs.writeCheckpointSummary(opts, basePath, entries, sessions)
}

// writeMainSessionToSubdirectory writes a single session's metadata, prompts, and
// content hash to a session subdirectory (0/, 1/, 2/, … indexed by session order
// within the checkpoint). Unlike the v1 equivalent, this does NOT write the raw
// transcript (full.jsonl) — that goes to /full/current.
func (s *V2GitStore) writeMainSessionToSubdirectory(opts WriteCommittedOptions, sessionPath string, entries map[string]object.TreeEntry) (SessionFilePaths, error) {
	filePaths := SessionFilePaths{}

	// Clear existing entries at this session path
	for key := range entries {
		if strings.HasPrefix(key, sessionPath) {
			delete(entries, key)
		}
	}

	// Write content hash from transcript (but not the transcript itself)
	if err := s.writeContentHash(opts, sessionPath, entries, &filePaths); err != nil {
		return filePaths, err
	}

	// Write prompts
	if len(opts.Prompts) > 0 {
		promptContent := redact.String(strings.Join(opts.Prompts, "\n\n---\n\n"))
		blobHash, err := CreateBlobFromContent(s.repo, []byte(promptContent))
		if err != nil {
			return filePaths, err
		}
		entries[sessionPath+paths.PromptFileName] = object.TreeEntry{
			Name: sessionPath + paths.PromptFileName,
			Mode: filemode.Regular,
			Hash: blobHash,
		}
		filePaths.Prompt = "/" + sessionPath + paths.PromptFileName
	}

	// Write session metadata
	sessionMetadata := CommittedMetadata{
		CheckpointID:                opts.CheckpointID,
		SessionID:                   opts.SessionID,
		Strategy:                    opts.Strategy,
		CreatedAt:                   time.Now().UTC(),
		Branch:                      opts.Branch,
		CheckpointsCount:            opts.CheckpointsCount,
		FilesTouched:                opts.FilesTouched,
		Agent:                       opts.Agent,
		Model:                       opts.Model,
		TurnID:                      opts.TurnID,
		IsTask:                      opts.IsTask,
		ToolUseID:                   opts.ToolUseID,
		TranscriptIdentifierAtStart: opts.TranscriptIdentifierAtStart,
		CheckpointTranscriptStart:   opts.CheckpointTranscriptStart,
		TranscriptLinesAtStart:      opts.CheckpointTranscriptStart,
		TokenUsage:                  opts.TokenUsage,
		SessionMetrics:              opts.SessionMetrics,
		InitialAttribution:          opts.InitialAttribution,
		Summary:                     redactSummary(opts.Summary),
		CLIVersion:                  versioninfo.Version,
	}

	metadataJSON, err := jsonutil.MarshalIndentWithNewline(sessionMetadata, "", "  ")
	if err != nil {
		return filePaths, fmt.Errorf("failed to marshal session metadata: %w", err)
	}
	metadataHash, err := CreateBlobFromContent(s.repo, metadataJSON)
	if err != nil {
		return filePaths, err
	}
	entries[sessionPath+paths.MetadataFileName] = object.TreeEntry{
		Name: sessionPath + paths.MetadataFileName,
		Mode: filemode.Regular,
		Hash: metadataHash,
	}
	filePaths.Metadata = "/" + sessionPath + paths.MetadataFileName

	return filePaths, nil
}

// writeContentHash computes and writes the content hash for the transcript
// without writing the transcript blobs themselves.
func (s *V2GitStore) writeContentHash(opts WriteCommittedOptions, sessionPath string, entries map[string]object.TreeEntry, filePaths *SessionFilePaths) error {
	transcript := opts.Transcript
	if len(transcript) == 0 {
		return nil
	}

	// Redact before hashing so the hash matches what /full/current stores
	redacted, err := redact.JSONLBytes(transcript)
	if err != nil {
		return fmt.Errorf("failed to redact transcript for content hash: %w", err)
	}

	contentHash := fmt.Sprintf("sha256:%x", sha256.Sum256(redacted))
	hashBlob, err := CreateBlobFromContent(s.repo, []byte(contentHash))
	if err != nil {
		return err
	}
	entries[sessionPath+paths.ContentHashFileName] = object.TreeEntry{
		Name: sessionPath + paths.ContentHashFileName,
		Mode: filemode.Regular,
		Hash: hashBlob,
	}
	filePaths.ContentHash = "/" + sessionPath + paths.ContentHashFileName

	return nil
}

// validateWriteOpts validates identifiers in WriteCommittedOptions.
func validateWriteOpts(opts WriteCommittedOptions) error {
	if opts.CheckpointID.IsEmpty() {
		return errors.New("invalid checkpoint options: checkpoint ID is required")
	}
	if err := validation.ValidateSessionID(opts.SessionID); err != nil {
		return fmt.Errorf("invalid checkpoint options: %w", err)
	}
	if err := validation.ValidateToolUseID(opts.ToolUseID); err != nil {
		return fmt.Errorf("invalid checkpoint options: %w", err)
	}
	if err := validation.ValidateAgentID(opts.AgentID); err != nil {
		return fmt.Errorf("invalid checkpoint options: %w", err)
	}
	return nil
}
