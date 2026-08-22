package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/example/sessionguard/internal/model"
)

func TestFileTemplateIsIdempotent(t *testing.T) {
	root := t.TempDir()
	item := model.TemplateItem{ID: "x", Kind: "file", Target: filepath.Join("Desktop", "x.txt"), Content: "hello", Overwrite: true}
	changed, err := Apply(root, item)
	if err != nil || !changed {
		t.Fatalf("first apply: changed=%v err=%v", changed, err)
	}
	changed, err = Apply(root, item)
	if err != nil || changed {
		t.Fatalf("second apply: changed=%v err=%v", changed, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "Desktop", "x.txt"))
	if string(b) != "hello" {
		t.Fatalf("unexpected content %q", b)
	}
}

func TestTargetCannotEscapeProfile(t *testing.T) {
	_, err := Apply(t.TempDir(), model.TemplateItem{ID: "x", Kind: "file", Target: filepath.Join("..", "escape.txt"), Content: "x", Overwrite: true})
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
}
