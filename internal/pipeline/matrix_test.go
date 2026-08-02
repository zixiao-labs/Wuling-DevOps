package pipeline

import (
	"reflect"
	"strings"
	"testing"
)

// mustParse parses a workflow fixture or fails the test.
func mustParse(t *testing.T, src string) *Workflow {
	t.Helper()
	w, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return w
}

// mv builds a scalar matrix value.
func mv(s string) MatrixValue { return MatrixValue{Text: s} }

// entry builds an include/exclude entry with a deterministic key order.
func entry(kv ...string) MatrixEntry {
	e := MatrixEntry{Values: map[string]MatrixValue{}}
	for i := 0; i+1 < len(kv); i += 2 {
		e.KeyOrder = append(e.KeyOrder, kv[i])
		e.Values[kv[i]] = mv(kv[i+1])
	}
	return e
}

// axes builds a Matrix from an ordered (name, values...) description.
func axes(order []string, vals map[string][]string) *Matrix {
	m := &Matrix{AxisOrder: order, Axes: map[string][]MatrixValue{}}
	for k, vs := range vals {
		out := make([]MatrixValue, 0, len(vs))
		for _, v := range vs {
			out = append(out, mv(v))
		}
		m.Axes[k] = out
	}
	return m
}

// render flattens combinations to "k=v,k=v" strings in Keys order, which is
// exactly the ordering guarantee the display name depends on.
func render(combos []MatrixCombination) []string {
	out := make([]string, 0, len(combos))
	for _, c := range combos {
		parts := make([]string, 0, len(c.Keys))
		for _, k := range c.Keys {
			parts = append(parts, k+"="+c.Values[k])
		}
		out = append(out, strings.Join(parts, ","))
	}
	return out
}

// ----------------------------------------------------------------------------
// combination expansion
// ----------------------------------------------------------------------------

func TestCombinations(t *testing.T) {
	cases := []struct {
		name   string
		matrix *Matrix
		want   []string
	}{
		{
			name:   "single axis",
			matrix: axes([]string{"os"}, map[string][]string{"os": {"ubuntu", "windows"}}),
			want:   []string{"os=ubuntu", "os=windows"},
		},
		{
			name: "cartesian, last axis varies fastest",
			matrix: axes([]string{"os", "node"}, map[string][]string{
				"os": {"ubuntu", "windows"}, "node": {"16", "18"},
			}),
			want: []string{
				"os=ubuntu,node=16", "os=ubuntu,node=18",
				"os=windows,node=16", "os=windows,node=18",
			},
		},
		{
			name: "three axes keep declaration order in Keys",
			matrix: axes([]string{"a", "b", "c"}, map[string][]string{
				"a": {"1"}, "b": {"2"}, "c": {"3", "4"},
			}),
			want: []string{"a=1,b=2,c=3", "a=1,b=2,c=4"},
		},
		{
			name: "exclude removes on partial match",
			matrix: func() *Matrix {
				m := axes([]string{"os", "node"}, map[string][]string{
					"os": {"ubuntu", "windows"}, "node": {"16", "18"},
				})
				m.Exclude = []MatrixEntry{entry("os", "windows")}
				return m
			}(),
			want: []string{"os=ubuntu,node=16", "os=ubuntu,node=18"},
		},
		{
			name: "exclude on a full key set removes exactly one",
			matrix: func() *Matrix {
				m := axes([]string{"os", "node"}, map[string][]string{
					"os": {"ubuntu", "windows"}, "node": {"16", "18"},
				})
				m.Exclude = []MatrixEntry{entry("os", "windows", "node", "16")}
				return m
			}(),
			want: []string{"os=ubuntu,node=16", "os=ubuntu,node=18", "os=windows,node=18"},
		},
		{
			name: "include merges into matching combinations",
			matrix: func() *Matrix {
				m := axes([]string{"os"}, map[string][]string{"os": {"ubuntu", "windows"}})
				m.Include = []MatrixEntry{entry("os", "ubuntu", "npm", "6")}
				return m
			}(),
			want: []string{"os=ubuntu,npm=6", "os=windows"},
		},
		{
			name: "include with only new keys applies to every combination",
			matrix: func() *Matrix {
				m := axes([]string{"os"}, map[string][]string{"os": {"ubuntu", "windows"}})
				m.Include = []MatrixEntry{entry("color", "green")}
				return m
			}(),
			want: []string{"os=ubuntu,color=green", "os=windows,color=green"},
		},
		{
			name: "include that matches nothing is appended standalone",
			matrix: func() *Matrix {
				m := axes([]string{"os"}, map[string][]string{"os": {"ubuntu"}})
				m.Include = []MatrixEntry{entry("os", "macos", "xcode", "15")}
				return m
			}(),
			want: []string{"os=ubuntu", "os=macos,xcode=15"},
		},
		{
			name: "include-only matrix produces exactly len(include) legs",
			matrix: &Matrix{
				Axes:    map[string][]MatrixValue{},
				Include: []MatrixEntry{entry("os", "ubuntu"), entry("os", "windows")},
			},
			want: []string{"os=ubuntu", "os=windows"},
		},
		{
			name: "a later include overwrites an include-added key but never an axis",
			matrix: func() *Matrix {
				m := axes([]string{"os"}, map[string][]string{"os": {"ubuntu"}})
				m.Include = []MatrixEntry{
					entry("npm", "6"),
					entry("os", "ubuntu", "npm", "8"),
				}
				return m
			}(),
			want: []string{"os=ubuntu,npm=8"},
		},
		{
			name: "exclude runs before include, so include can add a combination back",
			matrix: func() *Matrix {
				m := axes([]string{"os"}, map[string][]string{"os": {"ubuntu", "windows"}})
				m.Exclude = []MatrixEntry{entry("os", "windows")}
				m.Include = []MatrixEntry{entry("os", "windows", "note", "back")}
				return m
			}(),
			want: []string{"os=ubuntu", "os=windows,note=back"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.matrix.Combinations()
			if err != nil {
				t.Fatalf("Combinations: %v", err)
			}
			if !reflect.DeepEqual(render(got), c.want) {
				t.Errorf("got %v\nwant %v", render(got), c.want)
			}
		})
	}
}

// TestCombinationsGitHubDocExample reproduces the canonical include example
// from GitHub's own matrix documentation, verbatim. It is the tightest
// available check that merge-vs-append, ordering, and "a standalone include is
// not a target for a later include" all match upstream.
func TestCombinationsGitHubDocExample(t *testing.T) {
	m := axes([]string{"fruit", "animal"}, map[string][]string{
		"fruit": {"apple", "pear"}, "animal": {"cat", "dog"},
	})
	m.Include = []MatrixEntry{
		entry("color", "green"),
		entry("color", "pink", "animal", "cat"),
		entry("fruit", "apple", "shape", "circle"),
		entry("fruit", "banana"),
		entry("fruit", "banana", "animal", "cat"),
	}
	got, err := m.Combinations()
	if err != nil {
		t.Fatalf("Combinations: %v", err)
	}
	want := []string{
		"fruit=apple,animal=cat,color=pink,shape=circle",
		"fruit=apple,animal=dog,color=green,shape=circle",
		"fruit=pear,animal=cat,color=pink",
		"fruit=pear,animal=dog,color=green",
		"fruit=banana",
		"fruit=banana,animal=cat",
	}
	if !reflect.DeepEqual(render(got), want) {
		t.Errorf("got  %v\nwant %v", render(got), want)
	}
}

func TestCombinationsErrors(t *testing.T) {
	cases := []struct {
		name    string
		matrix  *Matrix
		wantSub string
	}{
		{
			name:    "nil matrix",
			matrix:  nil,
			wantSub: "empty",
		},
		{
			name:    "no axes and no include",
			matrix:  &Matrix{Axes: map[string][]MatrixValue{}},
			wantSub: "no axes and no include",
		},
		{
			name:    "axis with no values",
			matrix:  axes([]string{"os"}, map[string][]string{"os": {}}),
			wantSub: `axis "os" has no values`,
		},
		{
			name: "everything excluded",
			matrix: func() *Matrix {
				m := axes([]string{"os"}, map[string][]string{"os": {"ubuntu"}})
				m.Exclude = []MatrixEntry{entry("os", "ubuntu")}
				return m
			}(),
			wantSub: "no combinations",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.matrix.Combinations()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not contain %q", err, c.wantSub)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// caps
// ----------------------------------------------------------------------------

func TestCombinationCap(t *testing.T) {
	// 100 x 100 x 100 — the write-amplification bomb the cap exists to stop.
	// It must be rejected WITHOUT materialising a million combinations.
	big := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = "v" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		}
		return out
	}
	m := axes([]string{"a", "b", "c"}, map[string][]string{
		"a": big(100), "b": big(100), "c": big(100),
	})
	_, err := m.Combinations()
	if err == nil {
		t.Fatal("expected the cartesian product to be capped")
	}
	if !strings.Contains(err.Error(), "1000000") || !strings.Contains(err.Error(), "256") {
		t.Errorf("cap error should name the count and the cap, got %q", err)
	}

	// Exactly at the cap is allowed; one over is not.
	atCap := axes([]string{"a"}, map[string][]string{"a": big(MaxMatrixJobs)})
	if _, err := atCap.Combinations(); err != nil {
		t.Errorf("a matrix of exactly %d combinations must be allowed: %v", MaxMatrixJobs, err)
	}
	overCap := axes([]string{"a"}, map[string][]string{"a": big(MaxMatrixJobs + 1)})
	if _, err := overCap.Combinations(); err == nil {
		t.Errorf("a matrix of %d combinations must be rejected", MaxMatrixJobs+1)
	}

	// Include can push a legal product over the cap too.
	viaInclude := axes([]string{"a"}, map[string][]string{"a": big(MaxMatrixJobs)})
	viaInclude.Include = []MatrixEntry{entry("b", "extra")}
	viaInclude.Include[0] = entry("a", "nope", "b", "extra") // matches nothing -> appended
	if _, err := viaInclude.Combinations(); err == nil {
		t.Error("include pushing the total past the cap must be rejected")
	}
}

func TestValidateEnforcesCombinationCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("name: x\non: push\njobs:\n  build:\n    strategy:\n      matrix:\n        a:\n")
	for i := 0; i < 300; i++ {
		b.WriteString("          - v")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(string(rune('a' + i/26)))
		b.WriteString("\n")
	}
	b.WriteString("    steps: [{run: echo}]\n")
	_, err := Parse([]byte(b.String()))
	if err == nil {
		t.Fatal("expected Validate to reject a 300-combination matrix")
	}
	if !strings.Contains(err.Error(), `job "build"`) || !strings.Contains(err.Error(), "300") {
		t.Errorf("cap error must name the job and the count, got %q", err)
	}
}

func TestRunJobCap(t *testing.T) {
	// Five jobs of MaxMatrixJobs legs each clears the per-job cap but blows the
	// per-run cap.
	w := &Workflow{Name: "big", On: Triggers{WorkflowDispatch: true}, Jobs: map[string]Job{}}
	vals := make([]string, MaxMatrixJobs)
	for i := range vals {
		vals[i] = "v" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	for _, id := range []string{"j1", "j2", "j3", "j4", "j5"} {
		w.Jobs[id] = Job{
			Strategy: &Strategy{Matrix: axes([]string{"a"}, map[string][]string{"a": vals})},
			Steps:    []Step{{Run: "echo"}},
		}
	}
	if err := w.Validate(); err == nil {
		t.Fatal("expected Validate to reject a run of 1280 jobs")
	} else if !strings.Contains(err.Error(), "1280") || !strings.Contains(err.Error(), "1024") {
		t.Errorf("run cap error must name the count and the cap, got %q", err)
	}
	if _, err := w.Expand("medium"); err == nil {
		t.Error("Expand must independently enforce the run cap")
	}
}

// ----------------------------------------------------------------------------
// deterministic naming
// ----------------------------------------------------------------------------

func TestLegNaming(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		job   Job
		combo MatrixCombination
		want  string
	}{
		{
			name:  "no matrix keys renders the bare id",
			id:    "build",
			combo: MatrixCombination{},
			want:  "build",
		},
		{
			name:  "suffix follows Keys order",
			id:    "build",
			combo: MatrixCombination{Keys: []string{"os", "node"}, Values: MatrixContext{"os": "ubuntu", "node": "18"}},
			want:  "build (ubuntu, 18)",
		},
		{
			name:  "include-added key extends the suffix",
			id:    "build",
			combo: MatrixCombination{Keys: []string{"os", "node", "npm"}, Values: MatrixContext{"os": "ubuntu", "node": "18", "npm": "6"}},
			want:  "build (ubuntu, 18, 6)",
		},
		{
			name:  "an explicit job name wins verbatim, with no suffix",
			id:    "build",
			job:   Job{Name: "Build on ubuntu"},
			combo: MatrixCombination{Keys: []string{"os"}, Values: MatrixContext{"os": "ubuntu"}},
			want:  "Build on ubuntu",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LegName(c.id, c.job, c.combo); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestUniqueLegName(t *testing.T) {
	seen := map[string]int{}
	got := []string{
		uniqueLegName(seen, "build (x)"),
		uniqueLegName(seen, "build (x)"),
		uniqueLegName(seen, "build (x)"),
		uniqueLegName(seen, "build (y)"),
	}
	want := []string{"build (x)", "build (x) #2", "build (x) #3", "build (y)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}

	// Truncation is rune-safe, capped, and digest-suffixed so two distinct long
	// names sharing a prefix cannot collapse onto one another.
	long1 := strings.Repeat("字", 400) + "A"
	long2 := strings.Repeat("字", 400) + "B"
	n1 := uniqueLegName(seen, long1)
	n2 := uniqueLegName(seen, long2)
	if len([]rune(n1)) != maxJobNameRunes {
		t.Errorf("truncated name is %d runes, want %d", len([]rune(n1)), maxJobNameRunes)
	}
	if n1 == n2 {
		t.Errorf("two distinct long names truncated to the same value %q", n1)
	}
	if strings.HasSuffix(n1, "#2") || strings.HasSuffix(n2, "#2") {
		t.Errorf("digest suffix should have avoided the dedupe path: %q / %q", n1, n2)
	}
}

func TestExpandDedupesCollidingLegNames(t *testing.T) {
	// A duplicated axis value renders the same display name twice; without the
	// dedupe the UNIQUE (run_id, name) constraint would abort the whole run.
	w := mustParse(t, `
name: x
on: push
jobs:
  build:
    strategy:
      matrix:
        os: [ubuntu, ubuntu]
    steps: [{run: echo}]
`)
	legs, err := w.Expand("medium")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(legs) != 2 {
		t.Fatalf("want 2 legs, got %d", len(legs))
	}
	if legs[0].Name != "build (ubuntu)" || legs[1].Name != "build (ubuntu) #2" {
		t.Errorf("names = %q, %q", legs[0].Name, legs[1].Name)
	}
}

// ----------------------------------------------------------------------------
// interpolation
// ----------------------------------------------------------------------------

func TestInterpolate(t *testing.T) {
	ctx := MatrixContext{
		"os":       "ubuntu",
		"node":     "18",
		"cfg":      `{"arch":"arm64"}`,
		"cfg.arch": "arm64",
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text is untouched", "npm ci", "npm ci"},
		{"axis reference", "${{ matrix.os }}", "ubuntu"},
		{"no inner spaces", "${{matrix.os}}", "ubuntu"},
		{"dotted sub-path", "${{ matrix.cfg.arch }}", "arm64"},
		{"bare container renders canonical JSON", "${{ matrix.cfg }}", `{"arch":"arm64"}`},
		{"embedded in a larger string", "build-${{ matrix.os }}-${{ matrix.node }}", "build-ubuntu-18"},
		{"missing key yields empty", "[${{ matrix.nope }}]", "[]"},
		{"secrets are byte-identical", "${{ secrets.NPM_TOKEN }}", "${{ secrets.NPM_TOKEN }}"},
		{"env is byte-identical", "${{ env.FOO }}", "${{ env.FOO }}"},
		{"github context is byte-identical", "${{ github.sha }}", "${{ github.sha }}"},
		{"a function call is byte-identical", "${{ format(matrix.os) }}", "${{ format(matrix.os) }}"},
		{"bare matrix without a path is byte-identical", "${{ matrix }}", "${{ matrix }}"},
		{"unterminated expression is byte-identical", "${{ matrix.os", "${{ matrix.os"},
		{"mixed contexts resolve independently", "${{ matrix.os }}:${{ secrets.X }}", "ubuntu:${{ secrets.X }}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Interpolate(c.in, ctx); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestValueNormalisation(t *testing.T) {
	w := mustParse(t, `
name: x
on: push
jobs:
  build:
    strategy:
      matrix:
        node: [16, 1.10, true]
        cfg:
          - {arch: arm64, tags: [a, b]}
    steps:
      - run: |
          echo ${{ matrix.node }} ${{ matrix.cfg }} ${{ matrix.cfg.arch }} ${{ matrix.cfg.tags.1 }}
`)
	legs, err := w.Expand("medium")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	want := []string{
		`echo 16 {"arch":"arm64","tags":["a","b"]} arm64 b`,
		`echo 1.10 {"arch":"arm64","tags":["a","b"]} arm64 b`,
		`echo true {"arch":"arm64","tags":["a","b"]} arm64 b`,
	}
	if len(legs) != len(want) {
		t.Fatalf("want %d legs, got %d", len(want), len(legs))
	}
	for i, leg := range legs {
		if got := strings.TrimSpace(leg.Spec.Steps[0].Run); got != want[i] {
			t.Errorf("leg %d run = %q want %q", i, got, want[i])
		}
	}
}

// ----------------------------------------------------------------------------
// deep-copy isolation
// ----------------------------------------------------------------------------

func TestResolveDeepCopiesPerLeg(t *testing.T) {
	src := `
name: x
on: push
jobs:
  build:
    runs-on: [linux, "${{ matrix.os }}"]
    container: "node:${{ matrix.node }}"
    env:
      TARGET: ${{ matrix.os }}
      KEEP: ${{ secrets.NPM_TOKEN }}
    strategy:
      matrix:
        os: [ubuntu, windows]
        node: [18]
    steps:
      - name: Build ${{ matrix.os }}
        run: make ${{ matrix.os }}
        env:
          STEP_TARGET: ${{ matrix.os }}
        with: {}
`
	w := mustParse(t, src)
	legs, err := w.Expand("medium")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(legs) != 2 {
		t.Fatalf("want 2 legs, got %d", len(legs))
	}

	// Each leg must carry its OWN interpolated values. A shallow copy of Job
	// shares Steps/Env/Step.Env with w.Jobs["build"], so leg 2 would otherwise
	// read leg 1's already-substituted text and both legs would agree.
	if legs[0].Spec.Env["TARGET"] != "ubuntu" || legs[1].Spec.Env["TARGET"] != "windows" {
		t.Errorf("job env leaked between legs: %q / %q",
			legs[0].Spec.Env["TARGET"], legs[1].Spec.Env["TARGET"])
	}
	if legs[0].Spec.Steps[0].Env["STEP_TARGET"] != "ubuntu" || legs[1].Spec.Steps[0].Env["STEP_TARGET"] != "windows" {
		t.Errorf("step env leaked between legs: %q / %q",
			legs[0].Spec.Steps[0].Env["STEP_TARGET"], legs[1].Spec.Steps[0].Env["STEP_TARGET"])
	}
	if legs[0].Spec.Steps[0].Run != "make ubuntu" || legs[1].Spec.Steps[0].Run != "make windows" {
		t.Errorf("step run leaked between legs: %q / %q",
			legs[0].Spec.Steps[0].Run, legs[1].Spec.Steps[0].Run)
	}
	if legs[0].Spec.Steps[0].Name != "Build ubuntu" || legs[1].Spec.Steps[0].Name != "Build windows" {
		t.Errorf("step name leaked between legs: %q / %q",
			legs[0].Spec.Steps[0].Name, legs[1].Spec.Steps[0].Name)
	}
	if !reflect.DeepEqual(legs[0].RunsOn, []string{"linux", "ubuntu"}) ||
		!reflect.DeepEqual(legs[1].RunsOn, []string{"linux", "windows"}) {
		t.Errorf("runs-on leaked between legs: %v / %v", legs[0].RunsOn, legs[1].RunsOn)
	}
	if legs[0].Spec.Container != "node:18" {
		t.Errorf("container = %q", legs[0].Spec.Container)
	}
	if legs[0].Spec.Env["KEEP"] != "${{ secrets.NPM_TOKEN }}" {
		t.Errorf("secrets expression must survive expansion byte-identical, got %q", legs[0].Spec.Env["KEEP"])
	}

	// The parsed Workflow must be untouched: it is marshalled into
	// pipeline_runs.definition as the AUTHORED shape.
	orig := w.Jobs["build"]
	if orig.Env["TARGET"] != "${{ matrix.os }}" {
		t.Errorf("Expand mutated the parsed job env: %q", orig.Env["TARGET"])
	}
	if orig.Steps[0].Run != "make ${{ matrix.os }}" {
		t.Errorf("Expand mutated the parsed step run: %q", orig.Steps[0].Run)
	}
	if orig.Steps[0].Env["STEP_TARGET"] != "${{ matrix.os }}" {
		t.Errorf("Expand mutated the parsed step env: %q", orig.Steps[0].Env["STEP_TARGET"])
	}
	if !reflect.DeepEqual([]string(orig.RunsOn), []string{"linux", "${{ matrix.os }}"}) {
		t.Errorf("Expand mutated the parsed runs-on: %v", orig.RunsOn)
	}

	// Writing through one leg must not reach the other.
	legs[0].Spec.Env["TARGET"] = "mutated"
	legs[0].Spec.Steps[0].Env["STEP_TARGET"] = "mutated"
	if legs[1].Spec.Env["TARGET"] == "mutated" || legs[1].Spec.Steps[0].Env["STEP_TARGET"] == "mutated" {
		t.Error("legs share map backing storage")
	}
}

func TestResolveKeepsNilMapsNil(t *testing.T) {
	j := Job{Steps: []Step{{Run: "echo"}}}
	got := j.Resolve(MatrixContext{"os": "ubuntu"})
	if got.Env != nil {
		t.Errorf("nil job env became %v", got.Env)
	}
	if got.Steps[0].Env != nil || got.Steps[0].With != nil {
		t.Errorf("nil step maps became %v / %v", got.Steps[0].Env, got.Steps[0].With)
	}
}

// ----------------------------------------------------------------------------
// Expand
// ----------------------------------------------------------------------------

func TestExpandTopologicalWithMatrix(t *testing.T) {
	w := mustParse(t, `
name: x
on: push
jobs:
  test:
    needs: [build]
    steps: [{run: npm test}]
  build:
    strategy:
      fail-fast: false
      max-parallel: 2
      matrix:
        os: [ubuntu, windows]
    steps: [{run: make}]
`)
	legs, err := w.Expand("low")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	var names, keys []string
	for _, l := range legs {
		names = append(names, l.Name)
		keys = append(keys, l.Key)
	}
	wantNames := []string{"build (ubuntu)", "build (windows)", "test"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names = %v want %v", names, wantNames)
	}
	if !reflect.DeepEqual(keys, []string{"build", "build", "test"}) {
		t.Errorf("keys = %v", keys)
	}
	for i, l := range legs {
		if l.Ordinal != i+1 {
			t.Errorf("leg %d ordinal = %d", i, l.Ordinal)
		}
		if l.Tier != "low" {
			t.Errorf("leg %d tier = %q, want the default", i, l.Tier)
		}
	}
	// strategy knobs are denormalised onto every leg of their own job only
	if legs[0].FailFast || legs[1].FailFast {
		t.Error("fail-fast: false must reach both build legs")
	}
	if legs[0].MaxParallel != 2 || legs[1].MaxParallel != 2 {
		t.Errorf("max-parallel = %d / %d", legs[0].MaxParallel, legs[1].MaxParallel)
	}
	if !legs[2].FailFast || legs[2].MaxParallel != 0 {
		t.Errorf("a job without strategy must default to fail-fast=true, max-parallel=0, got %v/%d",
			legs[2].FailFast, legs[2].MaxParallel)
	}
	// `needs` addresses the logical job key, never the leg display name.
	if !reflect.DeepEqual(legs[2].Needs, []string{"build"}) {
		t.Errorf("needs = %v, want the logical key", legs[2].Needs)
	}
	// Every leg carries its own matrix context; a job without a matrix has none.
	if legs[0].Matrix["os"] != "ubuntu" || legs[1].Matrix["os"] != "windows" {
		t.Errorf("matrix contexts = %v / %v", legs[0].Matrix, legs[1].Matrix)
	}
	if len(legs[2].Matrix) != 0 {
		t.Errorf("a job with no matrix must carry an empty context, got %v", legs[2].Matrix)
	}
}

func TestExpandTierFromMatrix(t *testing.T) {
	w := mustParse(t, `
name: x
on: push
jobs:
  build:
    resource: ${{ matrix.tier }}
    strategy:
      matrix:
        tier: [low, high]
    steps: [{run: make}]
`)
	legs, err := w.Expand("medium")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if legs[0].Tier != "low" || legs[1].Tier != "high" {
		t.Errorf("tiers = %q / %q", legs[0].Tier, legs[1].Tier)
	}

	// A matrix value that is not a tier must FAIL rather than silently falling
	// back to the org default via EffectiveTier.
	bad := mustParse(t, `
name: x
on: push
jobs:
  build:
    resource: ${{ matrix.tier }}
    strategy:
      matrix:
        tier: [low, enormous]
    steps: [{run: make}]
`)
	_, err = bad.Expand("medium")
	if err == nil {
		t.Fatal("expected an unresolvable tier to fail Expand")
	}
	if !strings.Contains(err.Error(), "enormous") || !strings.Contains(err.Error(), `job "build"`) {
		t.Errorf("error should name the job and the bad value, got %q", err)
	}
}

func TestExpandTierLabelFromMatrix(t *testing.T) {
	w := mustParse(t, `
name: x
on: push
jobs:
  build:
    runs-on: [linux, "tier:${{ matrix.t }}"]
    strategy:
      matrix:
        t: [high]
    steps: [{run: make}]
`)
	legs, err := w.Expand("low")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if legs[0].Tier != "high" {
		t.Errorf("tier = %q, want high from the interpolated tier: label", legs[0].Tier)
	}
}

// ----------------------------------------------------------------------------
// parsing + validation
// ----------------------------------------------------------------------------

func TestParseStrategy(t *testing.T) {
	w := mustParse(t, `
name: x
on: push
jobs:
  build:
    name: Build ${{ matrix.os }}
    strategy:
      fail-fast: false
      max-parallel: 3
      matrix:
        os: [ubuntu, windows]
        include:
          - os: ubuntu
            npm: 6
        exclude:
          - os: windows
    steps: [{run: make}]
`)
	job := w.Jobs["build"]
	if job.Name != "Build ${{ matrix.os }}" {
		t.Errorf("job name = %q", job.Name)
	}
	if job.Strategy.FailFastOr() {
		t.Error("fail-fast: false was not parsed")
	}
	if job.Strategy.MaxParallelOr() != 3 {
		t.Errorf("max-parallel = %d", job.Strategy.MaxParallelOr())
	}
	if !reflect.DeepEqual(job.Strategy.Matrix.AxisOrder, []string{"os"}) {
		t.Errorf("axis order = %v", job.Strategy.Matrix.AxisOrder)
	}
	if len(job.Strategy.Matrix.Include) != 1 || job.Strategy.Matrix.Include[0].Values["npm"].Text != "6" {
		t.Errorf("include = %+v", job.Strategy.Matrix.Include)
	}
	if len(job.Strategy.Matrix.Exclude) != 1 {
		t.Errorf("exclude = %+v", job.Strategy.Matrix.Exclude)
	}
	// An explicit `name:` wins over the generated suffix on every leg.
	legs, err := w.Expand("medium")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(legs) != 1 || legs[0].Name != "Build ubuntu" {
		t.Errorf("legs = %+v", legs)
	}
}

func TestStrategyDefaults(t *testing.T) {
	var nilStrategy *Strategy
	if !nilStrategy.FailFastOr() {
		t.Error("a nil strategy must default fail-fast to true")
	}
	if nilStrategy.MaxParallelOr() != 0 {
		t.Error("a nil strategy must default max-parallel to 0 (unlimited)")
	}
	if nilStrategy.matrixOrNil() != nil {
		t.Error("a nil strategy must have a nil matrix")
	}
	empty := &Strategy{}
	if !empty.FailFastOr() {
		t.Error("an absent fail-fast key must default to true")
	}
}

func TestStrategyValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{
			name: "matrix reference with no matrix",
			src: `name: x
on: push
jobs:
  a:
    steps: [{run: "echo ${{ matrix.os }}"}]
`,
			wantSub: "declares no strategy.matrix",
		},
		{
			name: "matrix reference no combination defines",
			src: `name: x
on: push
jobs:
  a:
    strategy:
      matrix:
        os: [ubuntu]
    steps: [{run: "echo ${{ matrix.node }}"}]
`,
			wantSub: "no matrix combination defines node",
		},
		{
			name: "exclude names a non-axis key",
			src: `name: x
on: push
jobs:
  a:
    strategy:
      matrix:
        os: [ubuntu]
        exclude:
          - node: "18"
    steps: [{run: echo}]
`,
			wantSub: "is not a matrix axis",
		},
		{
			name: "negative max-parallel",
			src: `name: x
on: push
jobs:
  a:
    strategy:
      max-parallel: -1
      matrix:
        os: [ubuntu]
    steps: [{run: echo}]
`,
			wantSub: "max-parallel cannot be negative",
		},
		{
			name: "unknown strategy key is still a hard parse error",
			src: `name: x
on: push
jobs:
  a:
    strategy:
      fail_fast: false
      matrix:
        os: [ubuntu]
    steps: [{run: echo}]
`,
			wantSub: "fail_fast",
		},
		{
			name: "matrix axis must be a list",
			src: `name: x
on: push
jobs:
  a:
    strategy:
      matrix:
        os: ubuntu
    steps: [{run: echo}]
`,
			wantSub: "must be a list of values",
		},
		{
			name: "every combination excluded",
			src: `name: x
on: push
jobs:
  a:
    strategy:
      matrix:
        os: [ubuntu]
        exclude:
          - os: ubuntu
    steps: [{run: echo}]
`,
			wantSub: "no combinations",
		},
		{
			name: "a plain resource typo is still rejected at parse time",
			src: `name: x
on: push
jobs:
  a:
    resource: huge
    steps: [{run: echo}]
`,
			wantSub: "resource must be one of",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.src))
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not contain %q", err, c.wantSub)
			}
		})
	}
}

func TestStrategyWithoutMatrixIsLegal(t *testing.T) {
	w := mustParse(t, `
name: x
on: push
jobs:
  a:
    strategy:
      fail-fast: false
    steps: [{run: echo}]
`)
	legs, err := w.Expand("medium")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(legs) != 1 || legs[0].Name != "a" || legs[0].FailFast {
		t.Errorf("legs = %+v", legs)
	}
}

func TestMatrixRefsAcrossEveryInterpolatedPosition(t *testing.T) {
	j := Job{
		Name:      "${{ matrix.a }}",
		Resource:  "${{ matrix.b }}",
		Container: Container{Image: "${{ matrix.c }}"},
		RunsOn:    StringList{"${{ matrix.d }}"},
		Env:       map[string]string{"K": "${{ matrix.e }}"},
		Steps: []Step{{
			Name: "${{ matrix.f }}",
			Run:  "${{ matrix.g.sub }}",
			With: map[string]string{"w": "${{ matrix.h }}"},
			Env:  map[string]string{"E": "${{ secrets.NOPE }}"},
		}},
	}
	got := j.matrixRefs()
	want := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if len(got) != len(want) {
		t.Fatalf("refs = %v want %v", got, want)
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r] = true
	}
	for _, r := range want {
		if !seen[r] {
			t.Errorf("missing ref %q in %v", r, got)
		}
	}
}
