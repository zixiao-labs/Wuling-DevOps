package pipeline

// JobSpec is the self-contained execution unit handed to a runner at acquire
// time. It is intentionally decoupled from the YAML-facing Job shape: the
// store persists a JobSpec as the job's `definition` JSONB at run-creation, so
// a runner gets everything it needs to execute without re-reading the repo,
// and a re-run stays reproducible even if the workflow file later changes.
type JobSpec struct {
	Container string            `json:"container,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Steps     []StepSpec        `json:"steps"`
}

// StepSpec is one executable step. Exactly one of Run / Uses is set (enforced
// by Workflow.Validate before a JobSpec is ever built).
type StepSpec struct {
	Name           string            `json:"name,omitempty"`
	Run            string            `json:"run,omitempty"`
	Uses           string            `json:"uses,omitempty"`
	With           map[string]string `json:"with,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	If             string            `json:"if,omitempty"`
	TimeoutMinutes int               `json:"timeout_minutes,omitempty"`
}

// Spec projects a parsed Job into its executable JobSpec.
func (j Job) Spec() JobSpec {
	steps := make([]StepSpec, len(j.Steps))
	for i, s := range j.Steps {
		steps[i] = StepSpec{
			Name:           s.Name,
			Run:            s.Run,
			Uses:           s.Uses,
			With:           s.With,
			Env:            s.Env,
			If:             s.If,
			TimeoutMinutes: s.TimeoutMinutes,
		}
	}
	return JobSpec{Container: j.Container.Image, Env: j.Env, Steps: steps}
}

// StepDisplayName returns a human label for a step, falling back to the run
// snippet or the action name when `name:` is absent — mirrors how GitHub
// labels unnamed steps.
func (s StepSpec) StepDisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	if s.Uses != "" {
		return s.Uses
	}
	// first non-empty line of the run script, trimmed
	for _, line := range splitLines(s.Run) {
		if line != "" {
			if len(line) > 60 {
				return line[:60] + "…"
			}
			return line
		}
	}
	return "step"
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, trimSpace(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, trimSpace(cur))
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
