package strategy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/remote"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// RepairV2GenerationMetadataResult describes archived v2 generation metadata
// repair work performed by RepairV2GenerationMetadata.
type RepairV2GenerationMetadataResult struct {
	Repaired []string
	Skipped  []string
	Failed   []string
	Warnings []string
}

// RepairV2GenerationMetadata rewrites generation.json for archived v2 /full/*
// generation refs using the timestamp envelope from raw transcripts. Remote
// archived refs are repaired with force-with-lease when they exist on the
// checkpoint remote.
func RepairV2GenerationMetadata(ctx context.Context) (*RepairV2GenerationMetadataResult, error) {
	repo, err := OpenRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}

	store := checkpoint.NewV2GitStore(repo, "origin")
	return repairV2GenerationMetadata(ctx, repo, store)
}

func repairV2GenerationMetadata(ctx context.Context, repo *git.Repository, store *checkpoint.V2GitStore) (*RepairV2GenerationMetadataResult, error) {
	candidates, tempRefs, warnings, err := listArchivedV2GenerationCandidates(ctx, repo, store)
	if err != nil {
		return nil, fmt.Errorf("failed to list archived generations: %w", err)
	}
	defer removeTempRefs(repo, tempRefs)

	result := &RepairV2GenerationMetadataResult{
		Warnings: warnings,
	}

	pushTarget, pushTargetErr := repairPushTarget(ctx, candidates)
	if pushTargetErr != nil {
		result.Warnings = append(result.Warnings, pushTargetErr.Error())
	}

	for _, candidate := range candidates {
		repaired, repairErr := repairOneV2GenerationMetadata(ctx, repo, store, candidate, pushTarget, pushTargetErr)
		if repairErr != nil {
			result.Failed = append(result.Failed, candidate.Name)
			result.Warnings = append(result.Warnings, fmt.Sprintf("generation %s: %v", candidate.Name, repairErr))
			continue
		}
		if repaired {
			result.Repaired = append(result.Repaired, candidate.Name)
		} else {
			result.Skipped = append(result.Skipped, candidate.Name)
		}
	}

	return result, nil
}

func repairOneV2GenerationMetadata(
	ctx context.Context,
	repo *git.Repository,
	store *checkpoint.V2GitStore,
	candidate archivedV2GenerationCandidate,
	pushTarget string,
	pushTargetErr error,
) (bool, error) {
	oldCommitHash, treeHash, refErr := store.GetRefState(candidate.RefName)
	if refErr != nil {
		return false, fmt.Errorf("cannot read ref: %w", refErr)
	}

	gen, found, timestampErr := store.ComputeGenerationRawTranscriptTimestamps(treeHash)
	if timestampErr != nil {
		return false, fmt.Errorf("failed to compute raw transcript timestamps: %w", timestampErr)
	}
	if !found {
		return false, nil
	}

	current, genErr := store.ReadGeneration(treeHash)
	if genErr != nil {
		return false, fmt.Errorf("failed to read generation.json: %w", genErr)
	}
	if generationMetadataEqual(current, gen) {
		return false, nil
	}

	newTreeHash, addErr := store.AddGenerationJSONToTree(treeHash, gen)
	if addErr != nil {
		return false, fmt.Errorf("failed to rewrite generation.json: %w", addErr)
	}
	if newTreeHash == treeHash {
		return false, nil
	}

	newCommitHash, commitErr := checkpoint.CreateCommit(ctx, repo, newTreeHash, oldCommitHash,
		fmt.Sprintf("Repair generation metadata: %s\n", candidate.Name),
		"Entire Migration", "migration@entire.dev")
	if commitErr != nil {
		return false, fmt.Errorf("failed to create repair commit: %w", commitErr)
	}

	if err := repo.Storer.SetReference(plumbing.NewHashReference(candidate.RefName, newCommitHash)); err != nil {
		return false, fmt.Errorf("failed to update ref %s: %w", candidate.RefName, err)
	}

	if candidate.RemoteOID == "" {
		return true, nil
	}
	if pushTargetErr != nil {
		return false, fmt.Errorf("failed to resolve remote for generation metadata repair push: %w", pushTargetErr)
	}
	if pushTarget == "" {
		return false, errors.New("no push target available for remote generation metadata repair")
	}

	remoteRefName := paths.V2FullRefPrefix + candidate.Name
	if err := pushRepairedV2Generation(ctx, pushTarget, candidate.RefName.String(), remoteRefName, candidate.RemoteOID); err != nil {
		return false, err
	}
	return true, nil
}

func repairPushTarget(ctx context.Context, candidates []archivedV2GenerationCandidate) (string, error) {
	for _, candidate := range candidates {
		if candidate.RemoteOID != "" {
			target, _, err := remote.PushURL(ctx, "origin")
			if err != nil {
				return "", fmt.Errorf("push URL: %w", err)
			}
			return target, nil
		}
	}
	return "", nil
}

func pushRepairedV2Generation(ctx context.Context, target, sourceRef, remoteRef, expectedOID string) error {
	extraArgs := []string{}
	if expectedOID != "" {
		extraArgs = append(extraArgs, fmt.Sprintf("--force-with-lease=%s:%s", remoteRef, expectedOID))
	}
	result, err := remote.PushWithOptions(ctx, remote.PushOptions{
		Remote:    target,
		RefSpecs:  []string{sourceRef + ":" + remoteRef},
		ExtraArgs: extraArgs,
	})
	if err != nil {
		output := strings.TrimSpace(result.Output)
		if output != "" {
			return fmt.Errorf("%s: %w", output, err)
		}
		return fmt.Errorf("push repaired generation ref %s: %w", remoteRef, err)
	}
	return nil
}

func generationMetadataEqual(left, right checkpoint.GenerationMetadata) bool {
	return left.OldestCheckpointAt.Equal(right.OldestCheckpointAt) &&
		left.NewestCheckpointAt.Equal(right.NewestCheckpointAt)
}
