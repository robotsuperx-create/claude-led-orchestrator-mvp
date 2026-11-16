package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

var itemIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidItemID is true for an id the library could have made.
func ValidItemID(id string) bool { return itemIDPattern.MatchString(id) }

// library keeps items under <root>/items/<id>/: item.json beside the files.
// Folders are owner-only. Writes go through a temporary file and a rename,
// so an item on disk is always whole.
type library struct {
	root string
	mu   sync.Mutex
}

func (l *library) itemsDir() string { return filepath.Join(l.root, "items") }

func (l *library) itemDir(id string) string { return filepath.Join(l.itemsDir(), id) }

func (l *library) workflowsDir() string { return filepath.Join(l.root, "workflows") }

// ensure makes the library's folders.
func (l *library) ensure() error {
	for _, dir := range []string{l.root, l.itemsDir(), l.workflowsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (l *library) save(item Item) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.saveLocked(item)
}

func (l *library) saveLocked(item Item) error {
	if !ValidItemID(item.ID) {
		return ErrInvalidRequest
	}
	dir := l.itemDir(item.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".item-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, filepath.Join(dir, "item.json"))
}

func (l *library) get(id string) (Item, error) {
	if !ValidItemID(id) {
		return Item{}, ErrNotFound
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readLocked(id)
}

func (l *library) readLocked(id string) (Item, error) {
	data, err := os.ReadFile(filepath.Join(l.itemDir(id), "item.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	var item Item
	if err := json.Unmarshal(data, &item); err != nil || item.ID != id {
		return Item{}, fmt.Errorf("media: item %s is unreadable", id)
	}
	if item.Files == nil {
		item.Files = []File{}
	}
	return item, nil
}

// update reads an item, changes it and writes it back, under the lock.
func (l *library) update(id string, change func(*Item)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	item, err := l.readLocked(id)
	if err != nil {
		return err
	}
	change(&item)
	return l.saveLocked(item)
}

// list returns every readable item, newest first.
func (l *library) list() ([]Item, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := os.ReadDir(l.itemsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !ValidItemID(entry.Name()) {
			continue
		}
		item, err := l.readLocked(entry.Name())
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].CreatedAt.After(items[b].CreatedAt) })
	return items, nil
}

// remove deletes an item's folder and everything in it.
func (l *library) remove(id string) error {
	if !ValidItemID(id) {
		return ErrNotFound
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	dir := l.itemDir(id)
	if _, err := os.Stat(filepath.Join(dir, "item.json")); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return os.RemoveAll(dir)
}
