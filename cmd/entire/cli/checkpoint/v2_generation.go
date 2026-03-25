package checkpoint

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// DefaultMaxCheckpointsPerGeneration is the rotation threshold.
// When a generation reaches this many checkpoints, it is archived
// and a fresh /full/current is created.
const DefaultMaxCheckpointsPerGeneration = 100

// GenerationMetadata tracks the state of a /full/* generation.
// Stored at the tree root as generation.json and updated on every WriteCommitted.
// UpdateCommitted (stop-time finalization) does NOT update this file since it
// replaces an existing transcript rather than adding a new checkpoint.
type GenerationMetadata struct {
	// Generation is the sequence number (0 for /full/current, 1+ for archived).
	Generation int `json:"generation"`

	// CheckpointCount is the number of checkpoints in this generation.
	// Matches len(Checkpoints). Present per spec for quick reads by the
	// cleanup tool without parsing the full Checkpoints array.
	CheckpointCount int `json:"checkpoint_count"`

	// Checkpoints is the list of checkpoint IDs stored in this generation.
	// Used for finding which generation holds a specific checkpoint
	// without walking the tree.
	Checkpoints []string `json:"checkpoints"`

	// OldestCheckpointAt is the creation time of the earliest checkpoint.
	OldestCheckpointAt time.Time `json:"oldest_checkpoint_at"`

	// NewestCheckpointAt is the creation time of the most recent checkpoint.
	NewestCheckpointAt time.Time `json:"newest_checkpoint_at"`
}

// readGeneration reads generation.json from the given tree hash.
// Returns a zero-value GenerationMetadata if the file doesn't exist (new/empty generation).
func (s *V2GitStore) readGeneration(treeHash plumbing.Hash) (GenerationMetadata, error) {
	if treeHash == plumbing.ZeroHash {
		return GenerationMetadata{}, nil
	}

	tree, err := s.repo.TreeObject(treeHash)
	if err != nil {
		return GenerationMetadata{}, fmt.Errorf("failed to read tree: %w", err)
	}

	file, err := tree.File(paths.GenerationFileName)
	if err != nil {
		// File doesn't exist — empty/new generation
		return GenerationMetadata{}, nil
	}

	content, err := file.Contents()
	if err != nil {
		return GenerationMetadata{}, fmt.Errorf("failed to read %s: %w", paths.GenerationFileName, err)
	}

	var gen GenerationMetadata
	if err := json.Unmarshal([]byte(content), &gen); err != nil {
		return GenerationMetadata{}, fmt.Errorf("failed to parse %s: %w", paths.GenerationFileName, err)
	}

	return gen, nil
}

// readGenerationFromRef reads generation.json from the tree pointed to by the given ref.
func (s *V2GitStore) readGenerationFromRef(refName plumbing.ReferenceName) (GenerationMetadata, error) {
	_, treeHash, err := s.getRefState(refName)
	if err != nil {
		return GenerationMetadata{}, fmt.Errorf("failed to get ref state: %w", err)
	}
	return s.readGeneration(treeHash)
}

// writeGeneration marshals gen as generation.json and adds the blob entry to entries.
// Always syncs CheckpointCount = len(Checkpoints) before marshaling.
func (s *V2GitStore) writeGeneration(gen GenerationMetadata, entries map[string]object.TreeEntry) error {
	gen.CheckpointCount = len(gen.Checkpoints)

	data, err := jsonutil.MarshalIndentWithNewline(gen, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal %s: %w", paths.GenerationFileName, err)
	}

	blobHash, err := CreateBlobFromContent(s.repo, data)
	if err != nil {
		return fmt.Errorf("failed to create %s blob: %w", paths.GenerationFileName, err)
	}

	entries[paths.GenerationFileName] = object.TreeEntry{
		Name: paths.GenerationFileName,
		Mode: filemode.Regular,
		Hash: blobHash,
	}

	return nil
}

// updateGenerationForWrite reads the current generation metadata, appends the
// checkpoint ID (if not already present), and updates timestamps.
// Returns the updated metadata for the caller to write into the tree.
func (s *V2GitStore) updateGenerationForWrite(rootTreeHash plumbing.Hash, checkpointID id.CheckpointID, now time.Time) (GenerationMetadata, error) {
	gen, err := s.readGeneration(rootTreeHash)
	if err != nil {
		return GenerationMetadata{}, err
	}

	cpStr := checkpointID.String()

	// Only append if checkpoint ID is not already present (multi-session writes
	// to the same checkpoint should not duplicate the ID).
	found := false
	for _, existing := range gen.Checkpoints {
		if existing == cpStr {
			found = true
			break
		}
	}
	if !found {
		gen.Checkpoints = append(gen.Checkpoints, cpStr)
		gen.CheckpointCount = len(gen.Checkpoints)
	}

	gen.NewestCheckpointAt = now
	if gen.OldestCheckpointAt.IsZero() {
		gen.OldestCheckpointAt = now
	}

	return gen, nil
}

// addGenerationToRootTree adds generation.json to an existing root tree, returning
// a new root tree hash. Preserves all existing entries (shard directories, etc.).
func (s *V2GitStore) addGenerationToRootTree(rootTreeHash plumbing.Hash, gen GenerationMetadata) (plumbing.Hash, error) {
	gen.CheckpointCount = len(gen.Checkpoints)

	data, err := jsonutil.MarshalIndentWithNewline(gen, "", "  ")
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to marshal %s: %w", paths.GenerationFileName, err)
	}

	blobHash, err := CreateBlobFromContent(s.repo, data)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to create %s blob: %w", paths.GenerationFileName, err)
	}

	return UpdateSubtree(s.repo, rootTreeHash, nil, []object.TreeEntry{
		{Name: paths.GenerationFileName, Mode: filemode.Regular, Hash: blobHash},
	}, UpdateSubtreeOptions{MergeMode: MergeKeepExisting})
}
