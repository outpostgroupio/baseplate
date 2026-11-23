package ci

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	actionPath = "../../action.yml"
	planPath   = "../../examples/github-actions/plan-on-pr.yml"
	applyPath  = "../../examples/github-actions/apply-on-merge.yml"
)

// parseValid asserts the file exists and is syntactically valid YAML.
func parseValid(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return b
}

// noInterpolationInScripts guards the script-injection footgun (C1): no run/script
// body may contain a ${{ }} expression, so inputs must arrive via env.
func noInterpolationInScripts(t *testing.T, node *yaml.Node) {
	t.Helper()
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			val := node.Content[i+1]
			if (key.Value == "run" || key.Value == "script") && val.Kind == yaml.ScalarNode {
				if strings.Contains(val.Value, "${{") {
					t.Fatalf("%q body must not contain ${{ }} (script-injection risk); pass inputs via env:\n%s", key.Value, val.Value)
				}
			}
			noInterpolationInScripts(t, val)
		}
		return
	}
	for _, c := range node.Content {
		noInterpolationInScripts(t, c)
	}
}

func TestActionComposite(t *testing.T) {
	b := parseValid(t, actionPath)
	var m struct {
		Runs struct {
			Using string `yaml:"using"`
			Steps []struct {
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"runs"`
		Outputs map[string]struct {
			Value string `yaml:"value"`
		} `yaml:"outputs"`
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Runs.Using != "composite" {
		t.Fatalf("runs.using must be composite, got %q", m.Runs.Using)
	}
	// C2: outputs wired to steps.run.outputs.*
	for _, k := range []string{"output", "changes"} {
		if !strings.Contains(m.Outputs[k].Value, "steps.run.outputs."+k) {
			t.Fatalf("output %q must map to steps.run.outputs.%s, got %q", k, k, m.Outputs[k].Value)
		}
	}
	// run step exists with id: run and handles plan exit codes
	var runStep string
	for _, s := range m.Runs.Steps {
		if s.ID == "run" {
			runStep = s.Run
		}
	}
	if runStep == "" {
		t.Fatal("action must have a step with id: run")
	}
	if !strings.Contains(runStep, "changes=true") || !strings.Contains(runStep, "changes=false") {
		t.Fatal("run step must set changes=true/false from plan exit codes")
	}
	// C1: no ${{ in any run body
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	noInterpolationInScripts(t, &root)
}

func TestApplyWorkflow(t *testing.T) {
	b := parseValid(t, applyPath)
	s := string(b)
	if !strings.Contains(s, "--auto-approve") {
		t.Fatal("apply-on-merge must pass --auto-approve")
	}
	if !strings.Contains(s, "concurrency:") {
		t.Fatal("apply-on-merge must set a concurrency group")
	}
	var root yaml.Node
	yaml.Unmarshal(b, &root)
	noInterpolationInScripts(t, &root)
}

func TestPlanWorkflow(t *testing.T) {
	b := parseValid(t, planPath)
	s := string(b)
	if !strings.Contains(s, "head.repo.fork == false") {
		t.Fatal("plan-on-pr must guard against fork PRs")
	}
	if !strings.Contains(s, "PLAN:") {
		t.Fatal("plan-on-pr must pass plan text to github-script via env (PLAN:)")
	}
	var root yaml.Node
	yaml.Unmarshal(b, &root)
	noInterpolationInScripts(t, &root)
}
