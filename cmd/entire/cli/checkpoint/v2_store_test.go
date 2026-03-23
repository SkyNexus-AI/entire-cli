package checkpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/stretchr/testify/require"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// initTestRepo creates a bare-minimum git repo with one commit (needed for HEAD).
func initTestRepo(t *testing.T) *git.Repository {
	t.Helper()
	dir := t.TempDir()

	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	wt, err := repo.Worktree()
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("init"), 0o644))
	_, err = wt.Add("README.md")
	require.NoError(t, err)
	_, err = wt.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@test.com"},
	})
	require.NoError(t, err)

	return repo
}

func TestNewV2Store(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2Store(repo)
	require.NotNil(t, store)
	require.Equal(t, repo, store.repo)
}

func TestV2Store_EnsureRef_CreatesNewRef(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2Store(repo)

	refName := plumbing.ReferenceName(paths.V2MainRefName)

	// Ref should not exist yet
	_, err := repo.Reference(refName, true)
	require.Error(t, err)

	// Ensure creates it
	require.NoError(t, store.ensureRef(refName))

	// Ref should now exist and point to a valid commit with an empty tree
	ref, err := repo.Reference(refName, true)
	require.NoError(t, err)

	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)

	tree, err := commit.Tree()
	require.NoError(t, err)
	require.Empty(t, tree.Entries, "initial tree should be empty")
}

func TestV2Store_EnsureRef_Idempotent(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2Store(repo)

	refName := plumbing.ReferenceName(paths.V2MainRefName)

	require.NoError(t, store.ensureRef(refName))
	ref1, err := repo.Reference(refName, true)
	require.NoError(t, err)

	// Second call should be a no-op — same commit hash
	require.NoError(t, store.ensureRef(refName))
	ref2, err := repo.Reference(refName, true)
	require.NoError(t, err)
	require.Equal(t, ref1.Hash(), ref2.Hash())
}

func TestV2Store_EnsureRef_DifferentRefs(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2Store(repo)

	mainRef := plumbing.ReferenceName(paths.V2MainRefName)
	fullRef := plumbing.ReferenceName(paths.V2FullCurrentRefName)

	require.NoError(t, store.ensureRef(mainRef))
	require.NoError(t, store.ensureRef(fullRef))

	// Both should exist independently
	_, err := repo.Reference(mainRef, true)
	require.NoError(t, err)
	_, err = repo.Reference(fullRef, true)
	require.NoError(t, err)
}

func TestV2Store_GetRefState_ReturnsParentAndTree(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2Store(repo)

	refName := plumbing.ReferenceName(paths.V2MainRefName)
	require.NoError(t, store.ensureRef(refName))

	parentHash, treeHash, err := store.getRefState(refName)
	require.NoError(t, err)
	require.NotEqual(t, plumbing.ZeroHash, parentHash, "parent hash should be non-zero")
	// Tree hash can be zero hash for empty tree or a valid hash — just verify no error
	_ = treeHash
}

func TestV2Store_GetRefState_ErrorsOnMissingRef(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2Store(repo)

	refName := plumbing.ReferenceName("refs/entire/nonexistent")
	_, _, err := store.getRefState(refName)
	require.Error(t, err)
}

func TestV2Store_UpdateRef_CreatesCommit(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewV2Store(repo)

	refName := plumbing.ReferenceName(paths.V2MainRefName)
	require.NoError(t, store.ensureRef(refName))

	parentHash, treeHash, err := store.getRefState(refName)
	require.NoError(t, err)

	// Build a tree with one file
	blobHash, err := CreateBlobFromContent(repo, []byte("hello"))
	require.NoError(t, err)

	entries := map[string]object.TreeEntry{
		"test.txt": {Name: "test.txt", Mode: 0o100644, Hash: blobHash},
	}
	newTreeHash, err := BuildTreeFromEntries(repo, entries)
	require.NoError(t, err)
	require.NotEqual(t, treeHash, newTreeHash)

	// Update the ref
	require.NoError(t, store.updateRef(refName, newTreeHash, parentHash, "test commit", "Test", "test@test.com"))

	// Verify the ref now points to a commit with our tree
	ref, err := repo.Reference(refName, true)
	require.NoError(t, err)
	require.NotEqual(t, parentHash, ref.Hash(), "ref should point to new commit")

	commit, err := repo.CommitObject(ref.Hash())
	require.NoError(t, err)
	require.Equal(t, newTreeHash, commit.TreeHash)
	require.Equal(t, "test commit", commit.Message)
	require.Len(t, commit.ParentHashes, 1)
	require.Equal(t, parentHash, commit.ParentHashes[0])
}
