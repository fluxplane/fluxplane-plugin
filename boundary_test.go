package fluxplaneplugin_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSDKDoesNotImportCoreDexOrPluginsRegistry(t *testing.T) {
	forbidden := []string{
		"github.com/fluxplane/fluxplane-" + "core",
		"github.com/fluxplane/fluxplane-" + "dex",
		"github.com/fluxplane/fluxplane-" + "plugins",
	}
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			pathValue := strings.Trim(imported.Path.Value, "\"")
			for _, needle := range forbidden {
				if strings.HasPrefix(pathValue, needle) {
					t.Fatalf("%s imports forbidden module %s", path, needle)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
