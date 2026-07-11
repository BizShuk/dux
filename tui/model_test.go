package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bizshuk/disk-analyzer/svc"
)

func mkRow(path string, isDir bool, size int64, depth int) svc.EntryRow {
	return svc.EntryRow{
		Path:      path,
		IsDir:     isDir,
		SizeBytes: size,
		Depth:     depth,
	}
}

// TestBuildTree_FlatStructure verifies single-level rows all attach to the root.
func TestBuildTree_FlatStructure(t *testing.T) {
	rows := []svc.EntryRow{
		mkRow("/Users/shuk", true, 5000, 0),
		mkRow("/Users/shuk/Documents", true, 2000, 1),
		mkRow("/Users/shuk/Downloads", true, 3000, 1),
	}
	roots := buildTree(rows)
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	root := roots[0]
	if root.path != "/Users/shuk" {
		t.Errorf("expected root /Users/shuk, got %q", root.path)
	}
	if len(root.children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(root.children))
	}
	// Children sorted by size desc: Downloads(3000) before Documents(2000).
	if root.children[0].path != "/Users/shuk/Downloads" {
		t.Errorf("expected biggest child first; got %q", root.children[0].path)
	}
	if root.children[1].path != "/Users/shuk/Documents" {
		t.Errorf("expected Documents second; got %q", root.children[1].path)
	}
}

// TestBuildTree_NestedStructure verifies a 3-level tree wires parents correctly.
func TestBuildTree_NestedStructure(t *testing.T) {
	rows := []svc.EntryRow{
		mkRow("/Users/shuk", true, 10000, 0),
		mkRow("/Users/shuk/Library", true, 5000, 1),
		mkRow("/Users/shuk/Library/Caches", true, 2000, 2),
		mkRow("/Users/shuk/Library/Caches/foo.cache", false, 1500, 3),
		mkRow("/Users/shuk/Documents", true, 1000, 1),
	}
	roots := buildTree(rows)
	if len(roots) != 1 || roots[0].path != "/Users/shuk" {
		t.Fatalf("expected single root /Users/shuk, got %v", roots)
	}
	root := roots[0]
	library := findChild(root, "/Users/shuk/Library")
	if library == nil {
		t.Fatal("expected Library as child of root")
	}
	caches := findChild(library, "/Users/shuk/Library/Caches")
	if caches == nil {
		t.Fatal("expected Caches as child of Library")
	}
	foo := findChild(caches, "/Users/shuk/Library/Caches/foo.cache")
	if foo == nil {
		t.Fatal("expected foo.cache as child of Caches")
	}
	if foo.isDir {
		t.Error("foo.cache should be a file")
	}
}

// TestBuildTree_OrphanRows verifies rows whose parents are not in the input
// become top-level roots (defensive — the scanner emits all parents, but the
// tree builder should not panic if it gets a partial input).
func TestBuildTree_OrphanRows(t *testing.T) {
	rows := []svc.EntryRow{
		mkRow("/Users/shuk/Documents/file.txt", false, 100, 2),
	}
	roots := buildTree(rows)
	if len(roots) != 1 {
		t.Fatalf("orphan should become a root, got %d roots", len(roots))
	}
	if roots[0].path != "/Users/shuk/Documents/file.txt" {
		t.Errorf("expected orphan path as root, got %q", roots[0].path)
	}
}

// TestBuildTree_FilesAttachedToDirectories verifies regular files (not just
// directories) wire correctly to their parent directory.
func TestBuildTree_FilesAttachedToDirectories(t *testing.T) {
	rows := []svc.EntryRow{
		mkRow("/Users/shuk", true, 1000, 0),
		mkRow("/Users/shuk/Documents", true, 700, 1),
		mkRow("/Users/shuk/Documents/report.pdf", false, 500, 2),
		mkRow("/Users/shuk/Downloads/movie.mp4", false, 100, 2),
	}
	roots := buildTree(rows)
	root := roots[0]
	docs := findChild(root, "/Users/shuk/Documents")
	if docs == nil {
		t.Fatal("expected Documents child of root")
	}
	pdf := findChild(docs, "/Users/shuk/Documents/report.pdf")
	if pdf == nil {
		t.Fatal("expected report.pdf under Documents")
	}
	// movie.mp4 sits at /Users/shuk/Downloads/movie.mp4; its parent in the
	// tree is /Users/shuk (since Downloads itself isn't a row in this input)
	// — findParent walks up via filepath.Dir until it hits a known directory.
	mp4 := findChild(root, "/Users/shuk/Downloads/movie.mp4")
	if mp4 == nil {
		t.Fatal("expected movie.mp4 attached to root via walking-up parent")
	}
}

func findChild(n *treeNode, path string) *treeNode {
	for _, c := range n.children {
		if c.path == path {
			return c
		}
	}
	return nil
}

// TestRebuildFlat_RespectsExpansion verifies that collapsed dirs hide their
// children and that expand-all / collapse-all work as expected.
func TestRebuildFlat_RespectsExpansion(t *testing.T) {
	rows := []svc.EntryRow{
		mkRow("/Users/shuk", true, 5000, 0),
		mkRow("/Users/shuk/Documents", true, 2000, 1),
		mkRow("/Users/shuk/Documents/a.pdf", false, 100, 2),
		mkRow("/Users/shuk/Downloads", true, 3000, 1),
	}
	m := NewModel(rows, false, nil)

	// Default: root expanded, everything else collapsed.
	// Visible = root + its direct children = 3 rows.
	if len(m.flat) != 3 {
		t.Fatalf("default flat should be [root, docs, downloads] = 3 rows, got %d items", len(m.flat))
	}

	// Expand Documents (which contains a.pdf).
	m.expanded["/Users/shuk/Documents"] = true
	m.rebuildFlat()
	if len(m.flat) != 4 {
		t.Fatalf("after expanding Documents expected 4 rows (root, docs, a.pdf, downloads), got %d", len(m.flat))
	}

	// Collapse everything except roots (restores default view).
	m.collapseAll()
	// Visible = root + its direct children (each collapsed) = 3 rows.
	if len(m.flat) != 3 {
		t.Fatalf("after collapse-all expected 3 rows (root + 2 collapsed children), got %d", len(m.flat))
	}

	// Expand-all opens every directory.
	m.expandAll()
	if len(m.flat) != 4 {
		t.Fatalf("after expand-all expected 4 rows, got %d", len(m.flat))
	}
}

// TestHandleExpand_MovesToFirstChild verifies that pressing right on an
// already-expanded directory moves the cursor to its first child (standard
// tree-view UX).
func TestHandleExpand_MovesToFirstChild(t *testing.T) {
	rows := []svc.EntryRow{
		mkRow("/Users/shuk", true, 5000, 0),
		mkRow("/Users/shuk/Documents", true, 2000, 1),
		mkRow("/Users/shuk/Documents/a.pdf", false, 100, 2),
		mkRow("/Users/shuk/Downloads", true, 3000, 1),
	}
	m := NewModel(rows, false, nil)
	m.cursor = 0 // on root, which is expanded by default
	m.handleExpand()
	if m.cursor != 1 {
		t.Errorf("expected cursor to move to first child (index 1), got %d", m.cursor)
	}
}

// TestHandleCollapse_MovesToParent verifies that pressing left on a
// collapsed directory or a file moves the cursor up to the parent row.
func TestHandleCollapse_MovesToParent(t *testing.T) {
	rows := []svc.EntryRow{
		mkRow("/Users/shuk", true, 5000, 0),
		mkRow("/Users/shuk/Documents", true, 2000, 1),
		mkRow("/Users/shuk/Documents/a.pdf", false, 100, 2),
	}
	m := NewModel(rows, false, nil)
	// Expand Documents so a.pdf is visible.
	m.expanded["/Users/shuk/Documents"] = true
	m.rebuildFlat()
	// Cursor on a.pdf (index 2).
	m.cursor = 2
	m.handleCollapse()
	if m.cursor != 1 {
		t.Errorf("expected cursor to move to parent Documents (index 1), got %d", m.cursor)
	}
}

// TestDelete_RefusesPrivacyPath verifies the privacy-redact refuse-list.
// .ssh is in the default privacy_redact_paths; the file must NOT be moved
// even if it actually exists on disk.
func TestDelete_RefusesPrivacyPath(t *testing.T) {
	// Create a real .ssh-like file so os.Rename would otherwise succeed.
	tmp := t.TempDir()
	sensitive := tmp + "/.ssh/id_rsa_test"
	if err := os.MkdirAll(tmp+"/.ssh", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sensitive, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	rows := []svc.EntryRow{mkRow(sensitive, false, 6, 2)}
	m := NewModel(rows, false, []string{".ssh"})
	m.cursor = 0
	m.requestDeleteAtCursor()

	if !strings.Contains(m.statusMessage, "refused") {
		t.Errorf("expected refuse status, got %q", m.statusMessage)
	}
	if m.pendingDelete != nil {
		t.Errorf("privacy-path refusal should NOT set pendingDelete, got %p", m.pendingDelete)
	}
	if _, err := os.Stat(sensitive); err != nil {
		t.Errorf("privacy path was moved despite refusal: %v", err)
	}
}

// TestDelete_RefusesRedactedPlaceholder verifies synthetic "<redacted>"
// rows from privacy redaction can't be "deleted" (they don't correspond
// to a real filesystem path).
func TestDelete_RefusesRedactedPlaceholder(t *testing.T) {
	rows := []svc.EntryRow{mkRow("<redacted:contains-private>", false, 1, 1)}
	m := NewModel(rows, false, nil)
	m.cursor = 0
	m.requestDeleteAtCursor()
	if !strings.Contains(m.statusMessage, "redacted placeholder") {
		t.Errorf("expected redacted-placeholder refusal, got %q", m.statusMessage)
	}
	if m.pendingDelete != nil {
		t.Errorf("placeholder refusal should NOT set pendingDelete, got %p", m.pendingDelete)
	}
}

// TestDelete_RequestSetsPendingDelete verifies the first half of the
// two-step trash flow: requestDeleteAtCursor validates and parks the
// node in m.pendingDelete WITHOUT renaming anything yet.
func TestDelete_RequestSetsPendingDelete(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	srcDir := tmpHome + "/data"
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "victim.bin")
	if err := os.WriteFile(src, []byte("delete me"), 0o644); err != nil {
		t.Fatal(err)
	}

	rows := []svc.EntryRow{mkRow(src, false, 9, 1)}
	m := NewModel(rows, false, nil)
	m.cursor = 0
	m.requestDeleteAtCursor()

	if m.pendingDelete == nil {
		t.Fatal("expected pendingDelete to be set after request")
	}
	if m.pendingDelete.path != src {
		t.Errorf("pendingDelete.path = %q; want %q", m.pendingDelete.path, src)
	}
	if !strings.Contains(m.statusMessage, "y to confirm") {
		t.Errorf("expected confirm-prompt status, got %q", m.statusMessage)
	}
	// File MUST still exist on disk — request alone does not move.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source was moved at request stage: %v", err)
	}
}

// TestDelete_NonYKeyCancels verifies that pressing anything other than
// `y` while a delete is pending clears the pending state without
// moving the file.
func TestDelete_NonYKeyCancels(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	srcDir := tmpHome + "/data"
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "victim.bin")
	if err := os.WriteFile(src, []byte("delete me"), 0o644); err != nil {
		t.Fatal(err)
	}

	rows := []svc.EntryRow{mkRow(src, false, 9, 1)}
	m := NewModel(rows, false, nil)
	m.cursor = 0
	m.requestDeleteAtCursor()
	if m.pendingDelete == nil {
		t.Fatal("setup: pendingDelete not set")
	}

	// Drive the cancel path through handleKey with a non-`y` key.
	_, _ = m.handleKey("n")
	if m.pendingDelete != nil {
		t.Error("pendingDelete should be cleared after non-y key")
	}
	if !strings.Contains(m.statusMessage, "cancelled") {
		t.Errorf("expected cancel status, got %q", m.statusMessage)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source was moved despite cancel: %v", err)
	}
}

// TestDelete_MovesFileToTrash verifies the happy path: request sets
// pending, then `y` triggers executeDelete which renames the file into
// the system trash and detaches it from the tree. We use a tmp HOME
// so we don't pollute the user's actual ~/.Trash.
func TestDelete_MovesFileToTrash(t *testing.T) {
	tmpHome := t.TempDir()
	trashDir := filepath.Join(tmpHome, ".Trash")
	t.Setenv("HOME", tmpHome)

	srcDir := tmpHome + "/data"
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "victim.bin")
	if err := os.WriteFile(src, []byte("delete me"), 0o644); err != nil {
		t.Fatal(err)
	}

	rows := []svc.EntryRow{mkRow(src, false, 9, 1)}
	m := NewModel(rows, false, nil)
	m.cursor = 0
	m.requestDeleteAtCursor()
	if m.pendingDelete == nil {
		t.Fatal("setup: pendingDelete not set")
	}
	_, _ = m.handleKey("y")

	if !strings.HasPrefix(m.statusMessage, "moved to trash:") {
		t.Errorf("expected success status, got %q", m.statusMessage)
	}
	if m.pendingDelete != nil {
		t.Error("pendingDelete should be cleared after successful delete")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source file still exists after delete: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(trashDir, "victim.bin*"))
	if len(matches) == 0 {
		t.Errorf("expected file in trash at %s, found nothing", trashDir)
	}
	if len(m.flat) != 0 {
		t.Errorf("expected flat to be empty after delete, got %d rows", len(m.flat))
	}
}