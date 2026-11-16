package visualcheck

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxPages bounds how many pages one review photographs.
const MaxPages = 3

// FindPages picks the pages to judge in a worktree: the configured
// repository-relative HTML files that exist inside it, else index.html at the
// root (or under public/, site/, dist/ or docs/), else up to MaxPages HTML
// files at the root. A repository without pages yields none, and then there
// is nothing to review visually.
func FindPages(worktree string, configured []string) []Page {
	root, err := filepath.Abs(worktree)
	if err != nil {
		return nil
	}
	var pages []Page
	add := func(relative string) bool {
		relative = strings.TrimSpace(filepath.ToSlash(relative))
		if relative == "" || len(pages) >= MaxPages {
			return false
		}
		clean := filepath.Clean(filepath.FromSlash(relative))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return false
		}
		ext := strings.ToLower(filepath.Ext(clean))
		if ext != ".html" && ext != ".htm" {
			return false
		}
		file := filepath.Join(root, clean)
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || !inside(root, file) {
			return false
		}
		for _, page := range pages {
			if page.File == file {
				return false
			}
		}
		pages = append(pages, Page{Label: filepath.ToSlash(clean), File: file})
		return true
	}
	for _, relative := range configured {
		add(relative)
	}
	if len(pages) > 0 {
		return pages
	}
	for _, candidate := range []string{"index.html", "public/index.html", "site/index.html", "dist/index.html", "docs/index.html"} {
		if add(candidate) {
			return pages
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		add(name)
	}
	return pages
}

// inside reports whether file, with every link resolved, is within root: a
// directory link must not lead the browser out of the worktree.
func inside(root, file string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	realFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(realRoot, realFile)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
