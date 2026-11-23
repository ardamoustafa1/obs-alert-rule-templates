package render

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	jsonnet "github.com/google/go-jsonnet"
)

// embeddedImporter resolves Jsonnet imports against an embedded filesystem
// only, so rendering never touches the network or the local disk beyond the
// manifest/extra-rules files the caller explicitly passes in.
type embeddedImporter struct {
	fs    fs.FS
	root  string
	mu    sync.Mutex
	cache map[string]jsonnet.Contents
}

func newEmbeddedImporter(files fs.FS, root string) *embeddedImporter {
	return &embeddedImporter{fs: files, root: root, cache: make(map[string]jsonnet.Contents)}
}

func (i *embeddedImporter) Import(importedFrom, importedPath string) (jsonnet.Contents, string, error) {
	base := importedFrom
	if base == "" {
		base = i.root
	}
	resolved := path.Clean(path.Join(path.Dir(base), importedPath))
	if resolved == "." || strings.HasPrefix(resolved, "../") || strings.Contains(resolved, "/../") {
		return jsonnet.Contents{}, "", fmt.Errorf("import escapes embedded root: %s", importedPath)
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if contents, ok := i.cache[resolved]; ok {
		return contents, resolved, nil
	}
	data, err := fs.ReadFile(i.fs, resolved)
	if err != nil {
		return jsonnet.Contents{}, "", fmt.Errorf("import %q from %q: %w", importedPath, importedFrom, err)
	}
	contents := jsonnet.MakeContentsRaw(data)
	i.cache[resolved] = contents
	return contents, resolved, nil
}
