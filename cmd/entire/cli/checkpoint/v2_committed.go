package checkpoint

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/validation"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// WriteCommitted writes a committed checkpoint to both v2 refs:
//   - /main: metadata, prompts, content hash (no raw transcript)
//   - /full/current: raw transcript only (replaces previous content)
//
// This is the public entry point for v2 dual-writes. The session index is
// determined from the /main ref and passed to the /full/current write to
// keep both refs consistent.
func (s *V2GitStore) WriteCommitted(ctx context.Context, opts WriteCommittedOptions) error {
	sessionIndex, err := s.writeCommittedMain(ctx, opts)
	if err != nil {
		return fmt.Errorf("v2 /main write failed: %w", err)
	}

	if err := s.writeCommittedFullTranscript(ctx, opts, sessionIndex); err != nil {
		return fmt.Errorf("v2 /full/current write failed: %w", err)
	}

	return nil
}

// writeCommittedMain writes metadata entries to the /main ref.
// This includes session metadata, prompts, and content hash — but NOT the
// raw transcript (full.jsonl), which goes to /full/current.
// Returns the session index used, so the caller can pass it to writeCommittedFullTranscript.
func (s *V2GitStore) writeCommittedMain(ctx context.Context, opts WriteCommittedOptions) (int, error) {
	if err := validateWriteOpts(opts); err != nil {
		return 0, err
	}

	refName := plumbing.ReferenceName(paths.V2MainRefName)
	if err := s.ensureRef(refName); err != nil {
		return 0, fmt.Errorf("failed to ensure /main ref: %w", err)
	}

	parentHash, rootTreeHash, err := s.getRefState(refName)
	if err != nil {
		return 0, err
	}

	basePath := opts.CheckpointID.Path() + "/"
	checkpointPath := opts.CheckpointID.Path()

	// Read existing entries at this checkpoint's shard path
	entries, err := s.gs.flattenCheckpointEntries(rootTreeHash, checkpointPath)
	if err != nil {
		return 0, err
	}

	// Build main session entries (metadata, prompts, content hash — no transcript)
	sessionIndex, err := s.writeMainCheckpointEntries(ctx, opts, basePath, entries)
	if err != nil {
		return 0, err
	}

	// Splice entries into root tree
	newTreeHash, err := s.gs.spliceCheckpointSubtree(rootTreeHash, opts.CheckpointID, basePath, entries)
	if err != nil {
		return 0, err
	}

	commitMsg := fmt.Sprintf("Checkpoint: %s\n", opts.CheckpointID)
	if err := s.updateRef(refName, newTreeHash, parentHash, commitMsg, opts.AuthorName, opts.AuthorEmail); err != nil {
		return 0, err
	}
	return sessionIndex, nil
}

// writeMainCheckpointEntries orchestrates writing session data to the /main ref.
// It mirrors GitStore.writeStandardCheckpointEntries but excludes raw transcript blobs.
// Returns the session index used, for coordination with writeCommittedFullTranscript.
func (s *V2GitStore) writeMainCheckpointEntries(ctx context.Context, opts WriteCommittedOptions, basePath string, entries map[string]object.TreeEntry) (int, error) {
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
		return 0, err
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
	if err := s.gs.writeCheckpointSummary(opts, basePath, entries, sessions); err != nil {
		return 0, err
	}
	return sessionIndex, nil
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

// writeContentHash computes and writes the content hash for already-redacted transcript bytes.
func (s *V2GitStore) writeContentHash(redactedTranscript []byte, sessionPath string, entries map[string]object.TreeEntry) error {
	contentHash := fmt.Sprintf("sha256:%x", sha256.Sum256(redactedTranscript))
	hashBlob, err := CreateBlobFromContent(s.repo, []byte(contentHash))
	if err != nil {
		return err
	}
	entries[sessionPath+paths.ContentHashFileName] = object.TreeEntry{
		Name: sessionPath + paths.ContentHashFileName,
		Mode: filemode.Regular,
		Hash: hashBlob,
	}
	return nil
}

// writeCommittedFullTranscript writes the raw transcript to the /full/current ref.
// Each write replaces the entire tree — /full/current only ever contains the
// transcript for the most recently written checkpoint. Older transcripts are
// discarded; generation rotation (future work) will archive them before replacement.
//
// sessionIndex is the session slot (0-based), determined by the caller to stay
// consistent with the /main ref's session numbering.
// This is a no-op if opts.Transcript is empty (and opts.TranscriptPath is unset).
func (s *V2GitStore) writeCommittedFullTranscript(ctx context.Context, opts WriteCommittedOptions, sessionIndex int) error {
	transcript := opts.Transcript
	if len(transcript) == 0 && opts.TranscriptPath != "" {
		var readErr error
		transcript, readErr = os.ReadFile(opts.TranscriptPath)
		if readErr != nil {
			transcript = nil
		}
	}
	if len(transcript) == 0 {
		return nil // No transcript to write
	}

	if err := validateWriteOpts(opts); err != nil {
		return err
	}

	refName := plumbing.ReferenceName(paths.V2FullCurrentRefName)
	if err := s.ensureRef(refName); err != nil {
		return fmt.Errorf("failed to ensure /full/current ref: %w", err)
	}

	parentHash, _, err := s.getRefState(refName)
	if err != nil {
		return err
	}

	// Build a fresh tree with only this checkpoint's transcript (no accumulation).
	basePath := opts.CheckpointID.Path() + "/"
	sessionPath := fmt.Sprintf("%s%d/", basePath, sessionIndex)

	entries := make(map[string]object.TreeEntry)
	redactedTranscript, err := s.writeTranscriptBlobs(ctx, transcript, opts.Agent, sessionPath, entries)
	if err != nil {
		return err
	}

	// Write content hash alongside the transcript it references
	if err := s.writeContentHash(redactedTranscript, sessionPath, entries); err != nil {
		return err
	}

	newTreeHash, err := BuildTreeFromEntries(s.repo, entries)
	if err != nil {
		return fmt.Errorf("failed to build /full/current tree: %w", err)
	}

	commitMsg := fmt.Sprintf("Checkpoint: %s\n", opts.CheckpointID)
	return s.updateRef(refName, newTreeHash, parentHash, commitMsg, opts.AuthorName, opts.AuthorEmail)
}

// writeTranscriptBlobs writes redacted, chunked transcript blobs to entries.
// Returns the redacted transcript bytes so the caller can compute the content hash.
func (s *V2GitStore) writeTranscriptBlobs(ctx context.Context, transcript []byte, agentType types.AgentType, sessionPath string, entries map[string]object.TreeEntry) ([]byte, error) {
	// Redact secrets before chunking
	redacted, err := redact.JSONLBytes(transcript)
	if err != nil {
		return nil, fmt.Errorf("failed to redact transcript: %w", err)
	}

	chunks, err := agent.ChunkTranscript(ctx, redacted, agentType)
	if err != nil {
		return nil, fmt.Errorf("failed to chunk transcript: %w", err)
	}

	for i, chunk := range chunks {
		chunkPath := sessionPath + agent.ChunkFileName(paths.TranscriptFileName, i)
		blobHash, err := CreateBlobFromContent(s.repo, chunk)
		if err != nil {
			return nil, err
		}
		entries[chunkPath] = object.TreeEntry{
			Name: chunkPath,
			Mode: filemode.Regular,
			Hash: blobHash,
		}
	}

	return redacted, nil
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
