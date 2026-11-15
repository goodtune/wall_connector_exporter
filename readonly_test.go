package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The exporter must only read a charger. This test fails when a Go file of
// the repository names an HTTP method other than GET, or a function of
// net/http that sends such a request.
func TestNoHTTPWriteRequest(t *testing.T) {
	write := regexp.MustCompile(`\b(MethodPost|MethodPut|MethodPatch|MethodDelete|PostForm)\b|\.Post\(|"(POST|PUT|PATCH|DELETE)"`)
	checked := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || path == "readonly_test.go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		if match := write.Find(data); match != nil {
			t.Errorf("%s names the HTTP write request %s", path, match)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no Go file was checked")
	}
}
