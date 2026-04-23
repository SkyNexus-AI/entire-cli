package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

func TestWriteStandardCheckpointEntries_WarnsOnUnexpectedSessionZeroOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	repo, err := git.PlainInit(tmpDir, false)
	if err != nil {
		t.Fatalf("PlainInit() error = %v", err)
	}
	store := NewGitStore(repo)

	if err := logging.Init(context.Background(), ""); err != nil {
		t.Fatalf("logging.Init() error = %v", err)
	}

	checkpointID, err := id.Generate()
	if err != nil {
		t.Fatalf("id.Generate() error = %v", err)
	}
	basePath := checkpointID.Path() + "/"

	oldMetadata := CommittedMetadata{
		CheckpointID: checkpointID,
		SessionID:    "session-old",
		Strategy:     "manual-commit",
		CLIVersion:   versioninfo.Version,
	}
	oldMetadataJSON, err := jsonutil.MarshalIndentWithNewline(oldMetadata, "", "  ")
	if err != nil {
		t.Fatalf("marshal old metadata: %v", err)
	}
	oldMetadataHash, err := CreateBlobFromContent(repo, oldMetadataJSON)
	if err != nil {
		t.Fatalf("CreateBlobFromContent(old metadata) error = %v", err)
	}

	entries := map[string]object.TreeEntry{
		basePath + "0/" + paths.MetadataFileName: {
			Name: basePath + "0/" + paths.MetadataFileName,
			Mode: filemode.Regular,
			Hash: oldMetadataHash,
		},
	}

	opts := WriteCommittedOptions{
		CheckpointID: checkpointID,
		SessionID:    "session-new",
		Strategy:     "manual-commit",
		Transcript:   redact.AlreadyRedacted([]byte("{\"type\":\"user\",\"message\":\"hi\"}\n")),
		Prompts:      []string{"hi"},
	}

	if err := store.writeStandardCheckpointEntries(context.Background(), opts, basePath, entries); err != nil {
		t.Fatalf("writeStandardCheckpointEntries() error = %v", err)
	}
	logging.Close()

	logPath := filepath.Join(tmpDir, logging.LogsDir, "entire.log")
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", logPath, err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "checkpoint write overwrites session 0 with a different sessionID") {
		t.Fatalf("expected tripwire warning in log, got:\n%s", logText)
	}
	if !strings.Contains(logText, "session-old") || !strings.Contains(logText, "session-new") {
		t.Fatalf("expected log to include both session IDs, got:\n%s", logText)
	}
}
