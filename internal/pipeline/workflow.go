// Package pipeline parses and validates workflow files
// (.wuling/workflows/*.yml) and exposes the parsed shape that the rest of the
// CI subsystem (store, dispatch, runner) operates on.
//
// The accepted syntax is a deliberate SUBSET of GitHub Actions plus a single
// extension — the `resource:` tier (low|medium|high). Keeping the dialect
// familiar means an operator who knows Actions can read a Wuling workflow
// without a manual, and Stage 4's AI/MCP integrations can reuse Actions
// knowledge.
//
// This file is intentionally free of any I/O or CGO (no git, no DB) so it can
// be unit-tested in isolation; discovery of workflow blobs from a repo lives
// with the trigger code that needs the git layer.
package pipeline

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidTier reports whether s is a legal resource tier.
func ValidTier(s string) bool {
	switch s {
	case "low", "medium", "high":
		return true
	}
	return false
}

// Workflow is a parsed workflow file.
type Workflow struct {
	Name string         `json:"name"`
	On   Triggers       `json:"on"`
	Jobs map[string]Job `json:"jobs"`
}

// Triggers is the parsed `on:` block. It accepts the scalar (`on: push`),
// sequence (`on: [push, pull_request]`), and mapping
// (`on: {push: {branches: [main]}}`) forms.
type Triggers struct {
	Push             *RefFilter `json:"push,omitempty"`
	PullRequest      *RefFilter `json:"pull_request,omitempty"`
	WorkflowDispatch bool       `json:"workflow_dispatch,omitempty"`
}

// RefFilter narrows an event to a set of branch globs. A nil filter (or empty
// Branches) means "any branch".
type RefFilter struct {
	Branches StringList `yaml:"branches" json:"branches,omitempty"`
}

// Job is one job in a workflow.
type Job struct {
	RunsOn    StringList        `yaml:"runs-on" json:"runs_on,omitempty"`
	Resource  string            `yaml:"resource" json:"resource,omitempty"`
	Container Container         `yaml:"container" json:"container,omitempty"`
	Needs     StringList        `yaml:"needs" json:"needs,omitempty"`
	Env       map[string]string `yaml:"env" json:"env,omitempty"`
	Steps     []Step            `yaml:"steps" json:"steps"`
}

// Step is one step in a job. Exactly one of Run / Uses must be set.
type Step struct {
	Name           string            `yaml:"name" json:"name,omitempty"`
	Run            string            `yaml:"run" json:"run,omitempty"`
	Uses           string            `yaml:"uses" json:"uses,omitempty"`
	With           map[string]string `yaml:"with" json:"with,omitempty"`
	Env            map[string]string `yaml:"env" json:"env,omitempty"`
	If             string            `yaml:"if" json:"if,omitempty"`
	TimeoutMinutes int               `yaml:"timeout-minutes" json:"timeout_minutes,omitempty"`
}

// Container is the image steps run inside. Accepts `container: node:20` and
// `container: { image: node:20 }`.
type Container struct {
	Image string `yaml:"image" json:"image,omitempty"`
}

// UnmarshalYAML accepts a scalar image name or a mapping with `image:`.
func (c *Container) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return value.Decode(&c.Image)
	}
	type raw Container
	return value.Decode((*raw)(c))
}

// StringList unmarshals from either a scalar string or a sequence of strings,
// so `runs-on: linux` and `runs-on: [linux, docker]` both work.
type StringList []string

// UnmarshalYAML implements the scalar-or-sequence flexibility.
func (s *StringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var str string
		if err := value.Decode(&str); err != nil {
			return err
		}
		*s = []string{str}
	case yaml.SequenceNode:
		var arr []string
		if err := value.Decode(&arr); err != nil {
			return err
		}
		*s = arr
	default:
		return fmt.Errorf("expected a string or list of strings")
	}
	return nil
}

// UnmarshalYAML for Triggers handles the scalar / sequence / mapping forms.
func (t *Triggers) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var name string
		if err := value.Decode(&name); err != nil {
			return err
		}
		t.set(name, nil)
	case yaml.SequenceNode:
		var names []string
		if err := value.Decode(&names); err != nil {
			return err
		}
		for _, n := range names {
			t.set(n, nil)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(value.Content); i += 2 {
			key := value.Content[i].Value
			val := value.Content[i+1]
			var rf *RefFilter
			if val != nil && val.Kind == yaml.MappingNode {
				rf = &RefFilter{}
				if err := val.Decode(rf); err != nil {
					return err
				}
			}
			t.set(key, rf)
		}
	default:
		return fmt.Errorf("invalid `on:` block")
	}
	return nil
}

// set records one event name with an optional filter. Unknown event names are
// ignored at parse time (Validate surfaces "no supported triggers" if nothing
// matched), so a workflow using a Stage-2 event doesn't hard-fail the parse.
func (t *Triggers) set(name string, rf *RefFilter) {
	switch strings.TrimSpace(name) {
	case "push":
		if rf == nil {
			rf = &RefFilter{}
		}
		t.Push = rf
	case "pull_request":
		if rf == nil {
			rf = &RefFilter{}
		}
		t.PullRequest = rf
	case "workflow_dispatch":
		t.WorkflowDispatch = true
	}
}

// Any reports whether the workflow declares at least one supported trigger.
func (t Triggers) Any() bool {
	return t.Push != nil || t.PullRequest != nil || t.WorkflowDispatch
}

// Parse unmarshals and validates a workflow file. KnownFields is enabled so a
// typo'd key (e.g. `step:` instead of `steps:`) is reported instead of being
// silently dropped.
func Parse(data []byte) (*Workflow, error) {
	var w Workflow
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if err := w.Validate(); err != nil {
		return nil, err
	}
	return &w, nil
}

// Validate enforces the Stage-1 schema constraints.
func (w *Workflow) Validate() error {
	if !w.On.Any() {
		return fmt.Errorf("workflow %q declares no supported triggers (push/pull_request/workflow_dispatch)", w.Name)
	}
	if len(w.Jobs) == 0 {
		return fmt.Errorf("workflow %q has no jobs", w.Name)
	}
	for name, job := range w.Jobs {
		if !isJobName(name) {
			return fmt.Errorf("invalid job id %q (must match ^[A-Za-z_][A-Za-z0-9_-]*$)", name)
		}
		if job.Resource != "" && !ValidTier(job.Resource) {
			return fmt.Errorf("job %q: resource must be one of low|medium|high", name)
		}
		if len(job.Steps) == 0 {
			return fmt.Errorf("job %q has no steps", name)
		}
		for i, st := range job.Steps {
			if (st.Run == "") == (st.Uses == "") {
				return fmt.Errorf("job %q step %d: exactly one of `run` or `uses` is required", name, i+1)
			}
			if st.Uses != "" && !isSupportedUses(st.Uses) {
				return fmt.Errorf("job %q step %d: unsupported action %q (Stage 1 supports actions/checkout, actions/upload-artifact, actions/cache)", name, i+1, st.Uses)
			}
			if st.If != "" && !isSupportedIf(st.If) {
				return fmt.Errorf("job %q step %d: unsupported `if` %q (supported: success()|failure()|always())", name, i+1, st.If)
			}
			if st.TimeoutMinutes < 0 {
				return fmt.Errorf("job %q step %d: timeout-minutes cannot be negative", name, i+1)
			}
		}
		for _, dep := range job.Needs {
			if _, ok := w.Jobs[dep]; !ok {
				return fmt.Errorf("job %q needs unknown job %q", name, dep)
			}
		}
	}
	if _, err := w.JobOrder(); err != nil {
		return err
	}
	return nil
}

// JobOrder returns job ids in a topological order (a job appears after every
// job it `needs`). It returns an error if the needs graph has a cycle.
func (w *Workflow) JobOrder() ([]string, error) {
	// Kahn's algorithm over the needs DAG.
	indeg := make(map[string]int, len(w.Jobs))
	adj := make(map[string][]string, len(w.Jobs))
	for name := range w.Jobs {
		indeg[name] = 0
	}
	for name, job := range w.Jobs {
		for _, dep := range job.Needs {
			adj[dep] = append(adj[dep], name)
			indeg[name]++
		}
	}
	// Seed with zero-indegree jobs, sorted for deterministic output.
	var ready []string
	for name, d := range indeg {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)

	var order []string
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		next := adj[n]
		sort.Strings(next)
		for _, m := range next {
			indeg[m]--
			if indeg[m] == 0 {
				// keep ready sorted
				ready = append(ready, m)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(w.Jobs) {
		return nil, fmt.Errorf("workflow %q has a cycle in job `needs`", w.Name)
	}
	return order, nil
}

// EffectiveTier returns the job's resource tier, falling back to defaultTier
// (and then "medium") when unset. A `tier:<x>` label on runs-on also wins over
// the default so a workflow can pin a tier purely via labels.
func (j Job) EffectiveTier(defaultTier string) string {
	if ValidTier(j.Resource) {
		return j.Resource
	}
	for _, l := range j.RunsOn {
		if rest, ok := strings.CutPrefix(l, "tier:"); ok && ValidTier(rest) {
			return rest
		}
	}
	if ValidTier(defaultTier) {
		return defaultTier
	}
	return "medium"
}

// MatchEvent reports whether the workflow should run for (event, branch).
// branch is the short branch name (e.g. "main"); it is consulted for the push
// and pull_request branch filters (for pull_request it is the PR target
// branch).
func (w *Workflow) MatchEvent(event, branch string) bool {
	switch event {
	case "push":
		if w.On.Push == nil {
			return false
		}
		return matchBranches(w.On.Push.Branches, branch)
	case "pull_request":
		if w.On.PullRequest == nil {
			return false
		}
		return matchBranches(w.On.PullRequest.Branches, branch)
	case "manual":
		return w.On.WorkflowDispatch
	}
	return false
}

// matchBranches returns true if branch matches any glob, or if the filter is
// empty (no filter = all branches).
func matchBranches(globs StringList, branch string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if ok, _ := path.Match(g, branch); ok {
			return true
		}
	}
	return false
}

func isJobName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// supportedActions are the built-in `uses` actions a runner knows how to
// execute in Stage 1. Keep this in sync with the Rust runner's executor.
var supportedActions = map[string]bool{
	"actions/checkout":        true,
	"actions/upload-artifact": true,
	"actions/cache":           true,
}

// isSupportedUses accepts a known built-in action, with or without a @ref.
func isSupportedUses(uses string) bool {
	base, _, _ := strings.Cut(uses, "@")
	return supportedActions[base]
}

// IsCheckout reports whether a step's `uses` is the checkout action.
func IsCheckout(uses string) bool {
	base, _, _ := strings.Cut(uses, "@")
	return base == "actions/checkout"
}

func isSupportedIf(expr string) bool {
	switch strings.TrimSpace(expr) {
	case "success()", "failure()", "always()":
		return true
	}
	return false
}
