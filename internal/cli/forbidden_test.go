package cli

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tool talks to the providers directly (D1, approved). What it must never do
// is pull in a provider SDK (the request/response surface has to stay ours, with
// the API version written into every record) or shell out to another measurement
// tool.
var forbiddenImports = []string{
	"github.com/openai/openai-go",
	"github.com/sashabaranov/go-openai",
	"google.golang.org/genai",
	"cloud.google.com/go/vertexai",
	"github.com/anthropics/anthropic-sdk-go",
	"github.com/cohere-ai",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func walkGoFiles(t *testing.T, visit func(path string, content []byte)) int {
	t.Helper()
	root := repoRoot(t)
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".delta", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		count++
		visit(path, content)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return count
}

func TestNoProviderSDKIsImported(t *testing.T) {
	checked := walkGoFiles(t, func(path string, content []byte) {
		file, err := parser.ParseFile(token.NewFileSet(), path, content, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, spec := range file.Imports {
			importPath := strings.Trim(spec.Path.Value, `"`)
			for _, forbidden := range forbiddenImports {
				if strings.HasPrefix(importPath, forbidden) {
					t.Errorf("%s imports the provider SDK %s; the adapters must speak the API directly", path, importPath)
				}
			}
		}
	})
	if checked < 20 {
		t.Fatalf("only %d Go files were inspected; the scan is not looking at the repository", checked)
	}
}

func TestNoExternalMeasurementToolIsShelledOut(t *testing.T) {
	needle := "net" + "loom" // built at run time so this file does not match itself
	self := "forbidden_test.go"
	walkGoFiles(t, func(path string, content []byte) {
		if strings.HasSuffix(path, self) {
			return
		}
		if strings.Contains(strings.ToLower(string(content)), needle) {
			t.Errorf("%s references %q; the tool must not delegate measurement to another binary", path, needle)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, content, parser.ImportsOnly)
		if err != nil {
			return
		}
		for _, spec := range file.Imports {
			if strings.Trim(spec.Path.Value, `"`) == "os/exec" {
				t.Errorf("%s imports os/exec; every call is made in-process", path)
			}
		}
	})
}

func TestHTTPLayerIsUsedForTheProviders(t *testing.T) {
	// The other side of the same invariant: the adapters must go through net/http,
	// so the API surface and its version are explicit in our own code.
	found := map[string]bool{}
	walkGoFiles(t, func(path string, content []byte) {
		if strings.Contains(string(content), `"net/http"`) {
			found[filepath.Base(filepath.Dir(path))] = true
		}
	})
	for _, want := range []string{"httpclient", "openai", "gemini"} {
		if !found[want] {
			t.Errorf("package %s does not use net/http; the adapters are supposed to speak HTTP", want)
		}
	}
}
