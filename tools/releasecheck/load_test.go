package releasecheck_test

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// root is the repository root, relative to this package.
const root = "../.."

type workflow struct {
	Name        string
	On          map[string]any `yaml:"on"`
	Permissions any
	Jobs        map[string]job
}

type job struct {
	Name        string
	RunsOn      any `yaml:"runs-on"`
	Needs       any
	If          string
	Permissions any
	Strategy    any
	Env         map[string]string
	Steps       []step
}

type step struct {
	Name string
	ID   string
	If   string
	Uses string
	Run  string
	With map[string]string
	Env  map[string]string
}

// read returns a repository file.
func read(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

// decode parses a repository YAML file into v.
func decode(t *testing.T, rel string, v any) {
	t.Helper()
	if err := yaml.Unmarshal(read(t, rel), v); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
}

// loadWorkflow parses .github/workflows/<name>.
func loadWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	var w workflow
	decode(t, filepath.Join(".github", "workflows", name), &w)
	return w
}

// workflowFiles lists every workflow file in the repository.
func workflowFiles(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, pat := range []string{"*.yml", "*.yaml"} {
		m, err := filepath.Glob(filepath.Join(root, ".github", "workflows", pat))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range m {
			names = append(names, filepath.Base(p))
		}
	}
	if len(names) == 0 {
		t.Fatal("no workflows found")
	}
	return names
}

// stepsUsing returns the steps of j that use the action at path (owner/repo
// or owner/repo/subdir), whatever the ref.
func stepsUsing(j job, path string) []step {
	var out []step
	for _, s := range j.Steps {
		if s.Uses != "" && actionPath(s.Uses) == path {
			out = append(out, s)
		}
	}
	return out
}

func actionPath(uses string) string {
	for i := range len(uses) {
		if uses[i] == '@' {
			return uses[:i]
		}
	}
	return uses
}
