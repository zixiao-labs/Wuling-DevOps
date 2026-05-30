package pipeline

import (
	"sort"
	"strings"

	"github.com/zixiao-labs/wuling-devops/internal/git"
)

// WorkflowDir is the in-repo directory scanned for workflow files.
const WorkflowDir = ".wuling/workflows"

// DiscoveredWorkflow pairs a workflow's repo path with its parse result. When
// a file fails to parse, Workflow is nil and ParseErr is set — the caller can
// then surface a failed run instead of silently skipping CI, which is what an
// author expects when they push a broken workflow.
type DiscoveredWorkflow struct {
	Path     string
	Workflow *Workflow
	ParseErr error
}

// Discover lists and parses .wuling/workflows/*.{yml,yaml} at the given commit.
// A missing directory yields an empty slice and no error (the repo simply has
// no CI). Results are sorted by path for deterministic run ordering.
func Discover(repoPath, commitOID string) ([]DiscoveredWorkflow, error) {
	treeOID, err := descend(repoPath, commitOID, strings.Split(WorkflowDir, "/"))
	if err != nil {
		if git.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if treeOID == "" {
		return nil, nil
	}
	entries, err := git.ReadTree(repoPath, treeOID)
	if err != nil {
		if git.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []DiscoveredWorkflow
	for _, e := range entries {
		if e.Kind != "blob" || !isWorkflowFile(e.Name) {
			continue
		}
		blob, err := git.ReadBlob(repoPath, e.OID)
		if err != nil {
			return nil, err
		}
		dw := DiscoveredWorkflow{Path: WorkflowDir + "/" + e.Name}
		if wf, perr := Parse(blob.Data); perr != nil {
			dw.ParseErr = perr
		} else {
			dw.Workflow = wf
		}
		out = append(out, dw)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// descend walks nested tree entries from a commit/tree OID down a path,
// returning the final tree OID or "" if any segment is missing.
func descend(repoPath, startOID string, dirs []string) (string, error) {
	cur := startOID
	for _, d := range dirs {
		entries, err := git.ReadTree(repoPath, cur)
		if err != nil {
			return "", err
		}
		next := ""
		for _, e := range entries {
			if e.Name == d && e.Kind == "tree" {
				next = e.OID
				break
			}
		}
		if next == "" {
			return "", nil
		}
		cur = next
	}
	return cur, nil
}

func isWorkflowFile(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}
