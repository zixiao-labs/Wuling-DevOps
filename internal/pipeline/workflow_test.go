package pipeline

import (
	"reflect"
	"testing"
)

const sampleCI = `
name: CI
on:
  push:
    branches: [main, "release/*"]
  pull_request:
  workflow_dispatch:
jobs:
  build:
    runs-on: [linux, docker]
    resource: medium
    container: node:20
    env:
      CI: "true"
    steps:
      - name: Checkout
        uses: actions/checkout@v4
      - name: Build
        run: npm ci && npm run build
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  test:
    needs: [build]
    runs-on: linux
    resource: low
    steps:
      - run: npm test
`

func TestParseSample(t *testing.T) {
	w, err := Parse([]byte(sampleCI))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if w.Name != "CI" {
		t.Errorf("name = %q", w.Name)
	}
	if w.On.Push == nil || !reflect.DeepEqual([]string(w.On.Push.Branches), []string{"main", "release/*"}) {
		t.Errorf("push branches = %+v", w.On.Push)
	}
	if w.On.PullRequest == nil || !w.On.WorkflowDispatch {
		t.Error("expected pull_request + workflow_dispatch triggers")
	}
	build := w.Jobs["build"]
	if !reflect.DeepEqual([]string(build.RunsOn), []string{"linux", "docker"}) {
		t.Errorf("build runs-on = %+v", build.RunsOn)
	}
	if build.Container.Image != "node:20" {
		t.Errorf("container image = %q", build.Container.Image)
	}
	if len(build.Steps) != 2 || !IsCheckout(build.Steps[0].Uses) {
		t.Errorf("build steps = %+v", build.Steps)
	}
	if build.Steps[1].Env["NPM_TOKEN"] != "${{ secrets.NPM_TOKEN }}" {
		t.Errorf("step env not preserved: %+v", build.Steps[1].Env)
	}
}

func TestJobOrderTopological(t *testing.T) {
	w, err := Parse([]byte(sampleCI))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	order, err := w.JobOrder()
	if err != nil {
		t.Fatalf("JobOrder: %v", err)
	}
	// build must come before test
	idx := map[string]int{}
	for i, n := range order {
		idx[n] = i
	}
	if idx["build"] > idx["test"] {
		t.Errorf("expected build before test, got %v", order)
	}
}

func TestCycleDetected(t *testing.T) {
	const cyclic = `
name: x
on: push
jobs:
  a:
    needs: [b]
    steps: [{run: "echo a"}]
  b:
    needs: [a]
    steps: [{run: "echo b"}]
`
	if _, err := Parse([]byte(cyclic)); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestEffectiveTier(t *testing.T) {
	cases := []struct {
		job  Job
		def  string
		want string
	}{
		{Job{Resource: "high"}, "low", "high"},
		{Job{RunsOn: StringList{"linux", "tier:low"}}, "medium", "low"},
		{Job{}, "high", "high"},
		{Job{}, "", "medium"},
		{Job{Resource: "bogus"}, "low", "low"}, // invalid resource ignored
	}
	for i, c := range cases {
		if got := c.job.EffectiveTier(c.def); got != c.want {
			t.Errorf("case %d: got %q want %q", i, got, c.want)
		}
	}
}

func TestMatchEvent(t *testing.T) {
	w, _ := Parse([]byte(sampleCI))
	if !w.MatchEvent("push", "main") {
		t.Error("should match push to main")
	}
	if !w.MatchEvent("push", "release/1.2") {
		t.Error("should match push to release/1.2")
	}
	if w.MatchEvent("push", "feature/x") {
		t.Error("should NOT match push to feature/x")
	}
	if !w.MatchEvent("pull_request", "") {
		t.Error("should match pull_request")
	}
	if !w.MatchEvent("manual", "") {
		t.Error("should match manual (workflow_dispatch)")
	}
}

func TestValidationErrors(t *testing.T) {
	bad := []string{
		// no triggers
		"name: x\njobs:\n  a:\n    steps: [{run: echo}]\n",
		// no jobs
		"name: x\non: push\njobs: {}\n",
		// step with neither run nor uses
		"name: x\non: push\njobs:\n  a:\n    steps:\n      - name: nop\n",
		// step with both run and uses
		"name: x\non: push\njobs:\n  a:\n    steps:\n      - {run: echo, uses: actions/checkout}\n",
		// unsupported action
		"name: x\non: push\njobs:\n  a:\n    steps:\n      - uses: docker/build-push-action@v5\n",
		// bad tier
		"name: x\non: push\njobs:\n  a:\n    resource: huge\n    steps: [{run: echo}]\n",
		// unknown field (typo)
		"name: x\non: push\njobs:\n  a:\n    step: [{run: echo}]\n",
	}
	for i, src := range bad {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("case %d: expected error, got nil", i)
		}
	}
}

func TestOnScalarAndList(t *testing.T) {
	w, err := Parse([]byte("name: x\non: push\njobs:\n  a:\n    steps: [{run: echo}]\n"))
	if err != nil {
		t.Fatalf("scalar on: %v", err)
	}
	if w.On.Push == nil {
		t.Error("scalar `on: push` did not set Push")
	}
	w2, err := Parse([]byte("name: x\non: [push, pull_request]\njobs:\n  a:\n    steps: [{run: echo}]\n"))
	if err != nil {
		t.Fatalf("list on: %v", err)
	}
	if w2.On.Push == nil || w2.On.PullRequest == nil {
		t.Error("list `on` did not set both triggers")
	}
}
