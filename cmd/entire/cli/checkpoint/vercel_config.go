package checkpoint

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/vercelconfig"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// MaybeMergeMetadataBranchVercelConfig ensures the metadata branch contains the
// Vercel deployment-disable rule when the repository has Vercel integration enabled.
// Existing vercel.json content is preserved and merged when present.
func MaybeMergeMetadataBranchVercelConfig(repo *git.Repository, rootTreeHash plumbing.Hash) (plumbing.Hash, error) {
	worktree, worktreeErr := repo.Worktree()
	if worktreeErr != nil {
		return rootTreeHash, nil
	}

	projectSettings, settingsErr := settings.LoadFromRepoRoot(worktree.Filesystem.Root())
	if settingsErr != nil || !projectSettings.Vercel {
		return rootTreeHash, nil
	}

	config := make(map[string]any)
	var existingContents string
	if rootTreeHash != plumbing.ZeroHash {
		tree, treeErr := repo.TreeObject(rootTreeHash)
		if treeErr != nil && !errors.Is(treeErr, plumbing.ErrObjectNotFound) {
			return plumbing.ZeroHash, fmt.Errorf("read metadata tree: %w", treeErr)
		}
		if treeErr == nil {
			file, fileErr := tree.File(vercelconfig.FileName)
			if fileErr == nil {
				contents, contentsErr := file.Contents()
				if contentsErr != nil {
					return plumbing.ZeroHash, fmt.Errorf("read %s from metadata branch: %w", vercelconfig.FileName, contentsErr)
				}
				existingContents = contents
				if unmarshalErr := json.Unmarshal([]byte(contents), &config); unmarshalErr != nil {
					config = make(map[string]any)
				}
			}
		}
	}

	vercelconfig.MergeDeploymentDisabled(config)
	output, err := vercelconfig.Marshal(config)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("marshal %s: %w", vercelconfig.FileName, err)
	}
	if string(output) == existingContents {
		return rootTreeHash, nil
	}

	blobHash, err := CreateBlobFromContent(repo, output)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("create %s blob: %w", vercelconfig.FileName, err)
	}

	newTreeHash, err := UpdateSubtree(repo, rootTreeHash, nil, []object.TreeEntry{
		{Name: vercelconfig.FileName, Mode: filemode.Regular, Hash: blobHash},
	}, UpdateSubtreeOptions{MergeMode: MergeKeepExisting})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("update metadata subtree with %s: %w", vercelconfig.FileName, err)
	}

	return newTreeHash, nil
}
