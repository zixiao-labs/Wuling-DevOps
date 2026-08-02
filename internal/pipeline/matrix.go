package pipeline

// GitHub-Actions-compatible `strategy:` / `matrix` support.
//
// Expansion happens at run creation (pipelinestore.CreateRun calls
// Workflow.Expand), not at parse time and not at dispatch time. The dispatcher
// routes purely on row columns — resource_tier and runs_on — so a job written
// `runs-on: ${{ matrix.os }}` cannot be routed unless `os` is resolved before
// the pipeline_jobs row is written. For the same reason `${{ matrix.* }}` is
// interpolated server-side into the persisted JobSpec, while
// `${{ secrets.* }}` / `${{ env.* }}` are left byte-identical for the runner.
//
// This file is pure computation: no I/O, no DB, no CGO.

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxMatrixJobs caps one job's expanded legs. Matches GitHub Actions'
// documented limit. Without it a three-axis matrix in a pushed workflow file
// turns one `git push` into a million-row transaction in CreateRun: every leg
// is one pipeline_jobs INSERT plus one pipeline_steps INSERT per step, all in
// a single transaction.
const MaxMatrixJobs = 256

// MaxRunJobs caps a whole run's expanded jobs across every matrix, so a
// workflow cannot dodge MaxMatrixJobs by declaring many capped jobs.
const MaxRunJobs = 1024

// maxJobNameRunes bounds a leg's display name. pipeline_jobs.name is TEXT so
// there is no hard DB limit, but an unbounded name is a UI hazard and the
// UNIQUE (run_id, name) constraint makes a pathological name everyone's
// problem — a duplicate aborts the whole CreateRun transaction.
const maxJobNameRunes = 200

// ----------------------------------------------------------------------------
// parsed shape
// ----------------------------------------------------------------------------

// Strategy is a job's `strategy:` block. It is a plain struct (no custom
// unmarshaller) so Parse's KnownFields(true) still catches typos like
// `fail_fast:` or `maxParallel:`.
type Strategy struct {
	Matrix      *Matrix `yaml:"matrix" json:"matrix,omitempty"`
	FailFast    *bool   `yaml:"fail-fast" json:"fail_fast,omitempty"`
	MaxParallel int     `yaml:"max-parallel" json:"max_parallel,omitempty"`
}

// FailFastOr reports the effective fail-fast. GitHub's default is true, so an
// absent key and an explicit `fail-fast: false` must stay distinguishable —
// hence *bool rather than bool.
func (s *Strategy) FailFastOr() bool {
	if s == nil || s.FailFast == nil {
		return true
	}
	return *s.FailFast
}

// MaxParallelOr returns the concurrency cap for one job's legs; 0 = unlimited.
func (s *Strategy) MaxParallelOr() int {
	if s == nil {
		return 0
	}
	return s.MaxParallel
}

// matrixOrNil returns the strategy's matrix, tolerating a nil Strategy so
// Expand needs no branch for jobs without one.
func (s *Strategy) matrixOrNil() *Matrix {
	if s == nil {
		return nil
	}
	return s.Matrix
}

// Matrix is `strategy.matrix`: named axes plus include/exclude. AxisOrder is an
// explicit field because Go maps have no order, yet declaration order fixes
// BOTH the cartesian iteration order and the `build (ubuntu, 18)` name suffix —
// and the whole Workflow is JSON-marshalled into pipeline_runs.definition,
// where a map's key order would be lost.
type Matrix struct {
	AxisOrder []string                 `json:"axis_order"`
	Axes      map[string][]MatrixValue `json:"axes"`
	Include   []MatrixEntry            `json:"include,omitempty"`
	Exclude   []MatrixEntry            `json:"exclude,omitempty"`
}

// MatrixEntry is one include/exclude row: axis name -> value, order preserved.
type MatrixEntry struct {
	KeyOrder []string               `json:"key_order"`
	Values   map[string]MatrixValue `json:"values"`
}

// MatrixValue is one value on an axis.
//
// GitHub permits strings, numbers, booleans and objects. Rather than carry
// `any` (which loses YAML's source text on a JSON round-trip and gives no
// deterministic rendering), every value is normalised at parse time into a
// canonical Text plus, for containers, a FLATTENED dotted field map. Text
// preserves the YAML source token, so `18` interpolates as "18" and `1.10`
// stays "1.10" — matching GitHub, and avoiding float reformatting. Fields makes
// `${{ matrix.cfg.arch }}` a plain map lookup.
type MatrixValue struct {
	Text   string            `json:"text"`
	Fields map[string]string `json:"fields,omitempty"` // "arch" -> "arm64", "a.b" -> "c"
}

// UnmarshalYAML decodes the axis mapping in declaration order. `include` and
// `exclude` are the only reserved keys; every other key is an axis, so there is
// no unknown-key rejection to do here (which is just as well — a custom
// unmarshaller bypasses the decoder's KnownFields flag for its subtree, exactly
// as Container.UnmarshalYAML already does).
func (m *Matrix) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("strategy.matrix must be a mapping of axis name to value list")
	}
	m.Axes = map[string][]MatrixValue{}
	for i := 0; i+1 < len(value.Content); i += 2 {
		key, val := value.Content[i].Value, value.Content[i+1]
		switch key {
		case "include":
			e, err := decodeMatrixEntries(val)
			if err != nil {
				return fmt.Errorf("strategy.matrix.include: %w", err)
			}
			m.Include = e
		case "exclude":
			e, err := decodeMatrixEntries(val)
			if err != nil {
				return fmt.Errorf("strategy.matrix.exclude: %w", err)
			}
			m.Exclude = e
		default:
			if !isAxisName(key) {
				return fmt.Errorf("invalid matrix axis %q (must match ^[A-Za-z_][A-Za-z0-9_-]*$)", key)
			}
			if _, dup := m.Axes[key]; dup {
				return fmt.Errorf("duplicate matrix axis %q", key)
			}
			if val.Kind != yaml.SequenceNode {
				return fmt.Errorf("matrix axis %q must be a list of values", key)
			}
			vals := make([]MatrixValue, 0, len(val.Content))
			for _, n := range val.Content {
				var mv MatrixValue
				if err := mv.fromNode(n); err != nil {
					return fmt.Errorf("matrix axis %q: %w", key, err)
				}
				vals = append(vals, mv)
			}
			m.AxisOrder = append(m.AxisOrder, key)
			m.Axes[key] = vals
		}
	}
	return nil
}

// decodeMatrixEntries decodes an `include:` / `exclude:` sequence of mappings,
// preserving key order within each entry.
func decodeMatrixEntries(n *yaml.Node) ([]MatrixEntry, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("must be a list of mappings")
	}
	out := make([]MatrixEntry, 0, len(n.Content))
	for i, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("entry %d must be a mapping", i+1)
		}
		if len(item.Content) == 0 {
			return nil, fmt.Errorf("entry %d is empty", i+1)
		}
		e := MatrixEntry{Values: map[string]MatrixValue{}}
		for k := 0; k+1 < len(item.Content); k += 2 {
			key := item.Content[k].Value
			if !isAxisName(key) {
				return nil, fmt.Errorf("entry %d: invalid key %q (must match ^[A-Za-z_][A-Za-z0-9_-]*$)", i+1, key)
			}
			if _, dup := e.Values[key]; dup {
				return nil, fmt.Errorf("entry %d: duplicate key %q", i+1, key)
			}
			var mv MatrixValue
			if err := mv.fromNode(item.Content[k+1]); err != nil {
				return nil, fmt.Errorf("entry %d key %q: %w", i+1, key, err)
			}
			e.KeyOrder = append(e.KeyOrder, key)
			e.Values[key] = mv
		}
		out = append(out, e)
	}
	return out, nil
}

// fromNode normalises one YAML node. A scalar keeps its SOURCE text; a mapping
// or sequence is flattened into dotted Fields, with the container itself
// rendered as compact JSON for the name suffix / bare interpolation.
func (v *MatrixValue) fromNode(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode {
		if n.Alias == nil {
			return fmt.Errorf("unresolved YAML alias")
		}
		n = n.Alias
	}
	switch n.Kind {
	case yaml.ScalarNode:
		v.Text = n.Value // "ubuntu" | "18" | "true" | "1.10"
		return nil
	case yaml.MappingNode, yaml.SequenceNode:
		v.Fields = map[string]string{}
		if err := flattenNode(n, "", v.Fields); err != nil {
			return err
		}
		j, err := compactJSONOfNode(n)
		if err != nil {
			return err
		}
		v.Text = j
		return nil
	}
	return fmt.Errorf("unsupported matrix value")
}

// flattenNode records every addressable sub-path of a container value as a
// dotted key, so `${{ matrix.cfg.arch }}` is a plain map lookup. Sequence
// elements are addressed by their index ("0", "1", …).
func flattenNode(n *yaml.Node, prefix string, out map[string]string) error {
	switch n.Kind {
	case yaml.AliasNode:
		if n.Alias == nil {
			return fmt.Errorf("unresolved YAML alias")
		}
		return flattenNode(n.Alias, prefix, out)
	case yaml.ScalarNode:
		if prefix != "" {
			out[prefix] = n.Value
		}
		return nil
	case yaml.SequenceNode:
		for i, child := range n.Content {
			if err := flattenChild(strconv.Itoa(i), child, prefix, out); err != nil {
				return err
			}
		}
		return nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := flattenChild(n.Content[i].Value, n.Content[i+1], prefix, out); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported matrix value")
}

// flattenChild records one child under prefix. A nested container records both
// its own compact JSON (so `${{ matrix.cfg.sub }}` renders something) and its
// expanded sub-paths.
func flattenChild(key string, child *yaml.Node, prefix string, out map[string]string) error {
	p := key
	if prefix != "" {
		p = prefix + "." + key
	}
	if child.Kind == yaml.AliasNode {
		if child.Alias == nil {
			return fmt.Errorf("unresolved YAML alias")
		}
		child = child.Alias
	}
	if child.Kind == yaml.ScalarNode {
		out[p] = child.Value
		return nil
	}
	j, err := compactJSONOfNode(child)
	if err != nil {
		return err
	}
	out[p] = j
	return flattenNode(child, p, out)
}

// compactJSONOfNode renders a YAML node as compact JSON. Numeric and boolean
// scalars keep their source text where that text is already valid JSON, so
// `1.10` does not silently become `1.1`.
func compactJSONOfNode(n *yaml.Node) (string, error) {
	var b strings.Builder
	if err := writeJSONNode(&b, n); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeJSONNode(b *strings.Builder, n *yaml.Node) error {
	switch n.Kind {
	case yaml.AliasNode:
		if n.Alias == nil {
			return fmt.Errorf("unresolved YAML alias")
		}
		return writeJSONNode(b, n.Alias)
	case yaml.ScalarNode:
		return writeJSONScalar(b, n)
	case yaml.SequenceNode:
		b.WriteByte('[')
		for i, child := range n.Content {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSONNode(b, child); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	case yaml.MappingNode:
		b.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			key, err := json.Marshal(n.Content[i].Value)
			if err != nil {
				return err
			}
			b.Write(key)
			b.WriteByte(':')
			if err := writeJSONNode(b, n.Content[i+1]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		return nil
	}
	return fmt.Errorf("unsupported matrix value")
}

func writeJSONScalar(b *strings.Builder, n *yaml.Node) error {
	switch n.Tag {
	case "!!null":
		b.WriteString("null")
		return nil
	case "!!bool":
		var v bool
		if err := n.Decode(&v); err != nil {
			return err
		}
		b.WriteString(strconv.FormatBool(v))
		return nil
	case "!!int", "!!float":
		// YAML admits forms JSON does not (0x1f, 1_000, .inf); fall back to a
		// decoded round-trip for those.
		if json.Valid([]byte(n.Value)) {
			b.WriteString(n.Value)
			return nil
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return err
		}
		enc, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(enc)
		return nil
	}
	enc, err := json.Marshal(n.Value)
	if err != nil {
		return err
	}
	b.Write(enc)
	return nil
}

// isAxisName reuses the job-id rule for matrix axis and include/exclude keys.
func isAxisName(s string) bool { return isJobName(s) }

// ----------------------------------------------------------------------------
// expansion
// ----------------------------------------------------------------------------

// MatrixContext is one leg's flat matrix context: key (possibly dotted) ->
// canonical text. nil for a job with no matrix. Persisted verbatim as
// pipeline_jobs.matrix.
type MatrixContext map[string]string

// MatrixCombination is one resolved leg. Keys is the display order: declared
// axes first (AxisOrder), then any include-added keys in first-seen order. Only
// ROOT keys appear here — dotted sub-keys live in Values but are never part of
// a name suffix.
type MatrixCombination struct {
	Keys   []string
	Values MatrixContext
}

func (c MatrixCombination) clone() MatrixCombination {
	out := MatrixCombination{
		Keys:   make([]string, len(c.Keys)),
		Values: make(MatrixContext, len(c.Values)),
	}
	copy(out.Keys, c.Keys)
	for k, v := range c.Values {
		out.Values[k] = v
	}
	return out
}

// set records a root key's value plus its flattened sub-paths. Re-setting a key
// clears the sub-paths of the value it replaces so no stale dotted key lingers.
func (c *MatrixCombination) set(key string, v MatrixValue) {
	if c.Values == nil {
		c.Values = MatrixContext{}
	}
	if _, exists := c.Values[key]; exists {
		prefix := key + "."
		for k := range c.Values {
			if strings.HasPrefix(k, prefix) {
				delete(c.Values, k)
			}
		}
	} else {
		c.Keys = append(c.Keys, key)
	}
	c.Values[key] = v.Text
	for sub, text := range v.Fields {
		c.Values[key+"."+sub] = text
	}
}

// Combinations resolves the matrix per GitHub's documented rules: cartesian
// product, then exclude, then include (exclude first, so include can add
// combinations back). Returns an error when the result is empty or exceeds
// MaxMatrixJobs, so a broken or oversized matrix fails at Discover time like a
// `needs` cycle does rather than producing a bad run.
func (m *Matrix) Combinations() ([]MatrixCombination, error) {
	if m == nil {
		return nil, fmt.Errorf("strategy.matrix is empty")
	}
	if len(m.AxisOrder) == 0 && len(m.Include) == 0 {
		return nil, fmt.Errorf("strategy.matrix defines no axes and no include entries")
	}

	// Size the cartesian product BEFORE materialising it: an unbounded matrix
	// is a write-amplification bomb in CreateRun, and materialising it first
	// would mean allocating the bomb in order to reject it. float64 cannot
	// overflow at these magnitudes, so the reported count stays meaningful.
	size := 1.0
	for _, axis := range m.AxisOrder {
		n := len(m.Axes[axis])
		if n == 0 {
			return nil, fmt.Errorf("strategy.matrix axis %q has no values", axis)
		}
		size *= float64(n)
	}
	if size > float64(MaxMatrixJobs) {
		return nil, fmt.Errorf("strategy.matrix expands to %.0f combinations, which exceeds the cap of %d", size, MaxMatrixJobs)
	}

	// 1. cartesian product, last axis varying fastest (odometer / row-major).
	// With no axes the base set is the EMPTY slice, not one empty combination:
	// that is what makes an include-only matrix produce exactly len(Include)
	// jobs, matching GitHub.
	var combos []MatrixCombination
	if len(m.AxisOrder) > 0 {
		combos = []MatrixCombination{{Values: MatrixContext{}}}
		for _, axis := range m.AxisOrder {
			values := m.Axes[axis]
			next := make([]MatrixCombination, 0, len(combos)*len(values))
			for _, base := range combos {
				for _, v := range values {
					c := base.clone()
					c.set(axis, v)
					next = append(next, c)
				}
			}
			combos = next
		}
	}

	// 2. exclude — a partial match removes. Applied before include per GitHub's
	// docs ("this allows you to use include to add back combinations").
	if len(m.Exclude) > 0 {
		kept := combos[:0]
		for _, c := range combos {
			if !anyEntryMatches(m.Exclude, c) {
				kept = append(kept, c)
			}
		}
		combos = kept
	}

	// 3. include — merge into every BASE combination it applies to, else append
	// as a standalone leg. Only the post-exclude cartesian set is a merge
	// candidate: a leg appended by an earlier include is never a target for a
	// later one, which is what makes GitHub's `- {fruit: banana}` followed by
	// `- {fruit: banana, animal: cat}` two jobs rather than one.
	baseLen := len(combos)
	for _, inc := range m.Include {
		applied := false
		for i := 0; i < baseLen; i++ {
			if !includeApplies(inc, combos[i], m.Axes) {
				continue
			}
			applied = true
			for _, k := range inc.KeyOrder {
				if _, isAxis := m.Axes[k]; isAxis {
					continue // original axis values are never overwritten
				}
				combos[i].set(k, inc.Values[k])
			}
		}
		if !applied {
			c := MatrixCombination{Values: MatrixContext{}}
			for _, k := range inc.KeyOrder {
				c.set(k, inc.Values[k])
			}
			combos = append(combos, c)
		}
	}

	if len(combos) == 0 {
		return nil, fmt.Errorf("strategy.matrix produced no combinations (every combination was excluded)")
	}
	if len(combos) > MaxMatrixJobs {
		return nil, fmt.Errorf("strategy.matrix expands to %d combinations, which exceeds the cap of %d", len(combos), MaxMatrixJobs)
	}
	return combos, nil
}

// anyEntryMatches reports whether any entry's key/value pairs are all present
// in c — the exclude test.
func anyEntryMatches(entries []MatrixEntry, c MatrixCombination) bool {
	for _, e := range entries {
		match := true
		for _, k := range e.KeyOrder {
			got, ok := c.Values[k]
			if !ok || got != e.Values[k].Text {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// includeApplies reports whether an include entry can be merged into c: every
// key of the entry that names a DECLARED AXIS must already hold that value.
// Keys that are not axes (including keys added by an earlier include) impose no
// constraint and may be overwritten.
func includeApplies(inc MatrixEntry, c MatrixCombination, axes map[string][]MatrixValue) bool {
	for _, k := range inc.KeyOrder {
		if _, isAxis := axes[k]; !isAxis {
			continue
		}
		if c.Values[k] != inc.Values[k].Text {
			return false
		}
	}
	return true
}

// ----------------------------------------------------------------------------
// interpolation
// ----------------------------------------------------------------------------

// hasExpr reports whether s carries any `${{ … }}` expression. Used by Validate
// to defer checks that can only be made once the matrix is expanded.
func hasExpr(s string) bool { return strings.Contains(s, "${{") }

// scanExprs walks every `${{ … }}` expression in s, calling fn with the trimmed
// expression body.
func scanExprs(s string, fn func(expr string)) {
	for {
		i := strings.Index(s, "${{")
		if i < 0 {
			return
		}
		rest := s[i+3:]
		j := strings.Index(rest, "}}")
		if j < 0 {
			return
		}
		fn(strings.TrimSpace(rest[:j]))
		s = rest[j+2:]
	}
}

// matrixPath reports whether expr is a bare `matrix.<path>` reference and
// returns the dotted path after the prefix. The grammar is deliberately tiny:
// no functions, no operators, no nesting — anything else is passed through
// untouched so the runner keeps seeing it verbatim.
func matrixPath(expr string) (string, bool) {
	rest, ok := strings.CutPrefix(expr, "matrix.")
	if !ok || rest == "" {
		return "", false
	}
	for _, seg := range strings.Split(rest, ".") {
		if !isRefSegment(seg) {
			return "", false
		}
	}
	return rest, true
}

// isRefSegment accepts an axis-shaped name or a bare sequence index.
func isRefSegment(s string) bool {
	if isAxisName(s) {
		return true
	}
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Interpolate replaces every `${{ matrix.<path> }}` in s with its value from
// ctx. A path that resolves to nothing yields the empty string (GitHub's
// semantics for a missing context property; Validate has already guaranteed the
// ROOT key exists in at least one combination, so a miss here means this
// particular leg genuinely lacks an include-added key).
//
// Expressions naming any OTHER context — `${{ secrets.X }}`, `${{ env.X }}`,
// `${{ github.X }}` — are left BYTE-IDENTICAL so the runner can still resolve
// them. The `matrix.` prefix requirement guarantees the two never fight, even
// if a matrix axis and a secret share a name.
func Interpolate(s string, ctx MatrixContext) string {
	if !strings.Contains(s, "${{") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, "${{")
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		rest := s[i+3:]
		j := strings.Index(rest, "}}")
		if j < 0 {
			b.WriteString(s[i:]) // unterminated: verbatim
			break
		}
		if path, ok := matrixPath(strings.TrimSpace(rest[:j])); ok {
			b.WriteString(ctx[path])
		} else {
			b.WriteString(s[i : i+3+j+2])
		}
		s = rest[j+2:]
	}
	return b.String()
}

// interpValues returns a fresh map with every VALUE interpolated (keys are
// never expressions). Mirrors copyStringMap: nil in, nil out.
func interpValues(m map[string]string, ctx MatrixContext) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = Interpolate(v, ctx)
	}
	return out
}

// matrixRefs returns the ROOT matrix keys referenced by `${{ matrix.* }}` in
// any interpolated position of the job, in first-seen order. Validate uses it
// to reject a reference no combination can satisfy.
func (j Job) matrixRefs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		scanExprs(s, func(expr string) {
			path, ok := matrixPath(expr)
			if !ok {
				return
			}
			root, _, _ := strings.Cut(path, ".")
			if !seen[root] {
				seen[root] = true
				out = append(out, root)
			}
		})
	}
	add(j.Name)
	add(j.Resource)
	add(j.Container.Image)
	for _, l := range j.RunsOn {
		add(l)
	}
	for _, v := range j.Env {
		add(v)
	}
	for _, s := range j.Steps {
		add(s.Name)
		add(s.Run)
		for _, v := range s.With {
			add(v)
		}
		for _, v := range s.Env {
			add(v)
		}
	}
	return out
}

// Resolve returns a leg-local copy of j with `${{ matrix.* }}` substituted.
// Steps, Env, and every step's With/Env are freshly allocated: Job is a shallow
// value in Workflow.Jobs, so writing through the shared slice/maps would leak
// leg N's values into leg N+1 (and into the run's definition snapshot, which is
// marshalled from the same Workflow).
//
// Deliberately NOT interpolated:
//   - Needs   — addresses YAML job ids, resolved via job_key at dispatch.
//   - Step.If — a 3-literal whitelist (isSupportedIf) that Validate already
//     checked; interpolating it would let a matrix value smuggle past
//     validation.
func (j Job) Resolve(ctx MatrixContext) Job {
	out := j
	out.Name = Interpolate(j.Name, ctx)
	out.Resource = Interpolate(j.Resource, ctx)
	out.Container.Image = Interpolate(j.Container.Image, ctx)
	out.RunsOn = make(StringList, len(j.RunsOn))
	for i, l := range j.RunsOn {
		out.RunsOn[i] = Interpolate(l, ctx)
	}
	out.Env = interpValues(j.Env, ctx)
	out.Steps = make([]Step, len(j.Steps))
	for i, s := range j.Steps {
		s.Name = Interpolate(s.Name, ctx)
		s.Run = Interpolate(s.Run, ctx)
		s.With = interpValues(s.With, ctx)
		s.Env = interpValues(s.Env, ctx)
		out.Steps[i] = s // If / Uses / TimeoutMinutes untouched
	}
	out.Needs = j.Needs // shared read-only, never interpolated
	return out
}

// ----------------------------------------------------------------------------
// naming
// ----------------------------------------------------------------------------

// LegName renders a leg's display name. When the job declares `name:`, the
// author's (already interpolated) name wins verbatim — GitHub does not append a
// suffix in that case. Otherwise: "<id> (v1, v2)" over c.Keys in order, using
// each value's canonical Text. A leg with no keys renders as just "<id>".
func LegName(id string, j Job, c MatrixCombination) string {
	if strings.TrimSpace(j.Name) != "" {
		return j.Name
	}
	if len(c.Keys) == 0 {
		return id
	}
	parts := make([]string, 0, len(c.Keys))
	for _, k := range c.Keys {
		parts = append(parts, c.Values[k])
	}
	return id + " (" + strings.Join(parts, ", ") + ")"
}

// uniqueLegName truncates name to maxJobNameRunes and appends " #2", " #3" … to
// duplicates, recording the result in seen. Two distinct combinations can
// render the same text (two objects with the same canonical JSON, say), and
// pipeline_jobs has UNIQUE (run_id, name) — a duplicate would abort the whole
// CreateRun transaction, not just one job, so this must never be skipped.
func uniqueLegName(seen map[string]int, name string) string {
	base := truncateName(name)
	candidate := base
	for i := 2; ; i++ {
		if _, taken := seen[candidate]; !taken {
			seen[candidate] = i - 1
			return candidate
		}
		suffix := " #" + strconv.Itoa(i)
		candidate = truncateRunes(base, maxJobNameRunes-len([]rune(suffix))) + suffix
	}
}

// truncateName caps a name at maxJobNameRunes, rune-safe, with an 8-hex FNV-1a
// digest of the FULL name so truncation cannot itself manufacture a collision.
func truncateName(s string) string {
	r := []rune(s)
	if len(r) <= maxJobNameRunes {
		return s
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	suffix := fmt.Sprintf("…%08x", h.Sum32())
	return truncateRunes(s, maxJobNameRunes-len([]rune(suffix))) + suffix
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// ----------------------------------------------------------------------------
// Workflow.Expand
// ----------------------------------------------------------------------------

// ExpandedJob is one concrete pipeline_jobs row: a job, or one leg of its
// matrix, with `${{ matrix.* }}` already resolved.
type ExpandedJob struct {
	Key         string        // YAML job id, shared by every leg    -> pipeline_jobs.job_key
	Name        string        // display name, unique within the run -> pipeline_jobs.name
	Ordinal     int           // 1-based position in Expand's output -> pipeline_jobs.ordinal
	Matrix      MatrixContext // nil for a job with no matrix        -> pipeline_jobs.matrix
	RunsOn      []string      // interpolated
	Tier        string        // interpolated, then EffectiveTier applied
	Needs       []string      // logical job KEYS — never interpolated
	FailFast    bool
	MaxParallel int
	Spec        JobSpec // fully interpolated
}

// Expand resolves every job's strategy.matrix into concrete legs in
// `needs`-topological order (every leg of a job precedes every leg of any job
// that needs it), interpolating `${{ matrix.* }}` into runs-on, resource,
// container, env and steps. defaultTier is the org fallback consumed by
// EffectiveTier. It is the single entry point CreateRun uses.
func (w *Workflow) Expand(defaultTier string) ([]ExpandedJob, error) {
	order, err := w.JobOrder() // topo-sort of LOGICAL job ids
	if err != nil {
		return nil, err
	}
	out := make([]ExpandedJob, 0, len(order))
	seen := map[string]int{}
	for _, id := range order {
		job := w.Jobs[id]
		combos := []MatrixCombination{{}} // one empty leg when there is no matrix
		if m := job.Strategy.matrixOrNil(); m != nil {
			combos, err = m.Combinations()
			if err != nil {
				return nil, fmt.Errorf("job %q: %w", id, err)
			}
		}
		for _, c := range combos {
			rj := job.Resolve(c.Values)
			legName := LegName(id, rj, c)
			// A `resource:` carrying an expression skipped Validate's tier
			// check; the resolved value must still be a real tier. Failing
			// loudly beats EffectiveTier's silent fallback, which would route a
			// typo'd matrix tier to the org default with no error at all.
			if rj.Resource != "" && !ValidTier(rj.Resource) {
				return nil, fmt.Errorf("job %q leg %q: resource resolved to %q, must be one of low|medium|high", id, legName, rj.Resource)
			}
			if len(out) >= MaxRunJobs {
				return nil, fmt.Errorf("workflow %q expands to more than %d jobs (cap reached at job %q)", w.Name, MaxRunJobs, id)
			}
			out = append(out, ExpandedJob{
				Key:         id,
				Name:        uniqueLegName(seen, legName),
				Ordinal:     len(out) + 1,
				Matrix:      c.Values,
				RunsOn:      rj.RunsOn,
				Tier:        rj.EffectiveTier(defaultTier), // AFTER interpolation, so `tier:${{ matrix.t }}` works
				Needs:       job.Needs,
				FailFast:    job.Strategy.FailFastOr(),
				MaxParallel: job.Strategy.MaxParallelOr(),
				Spec:        rj.Spec(),
			})
		}
	}
	return out, nil
}
