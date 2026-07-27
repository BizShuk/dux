package delete

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mitchellh/go-homedir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteListExpandsHomePath(t *testing.T) {
	home, err := homedir.Dir()
	require.NoError(t, err)

	target := TargetItem{Path: "~/dux-home-expansion-test", Description: "Home path"}
	envTarget := TargetItem{Path: "$HOME/dux-home-expansion-test", Description: "Environment home path"}
	var out bytes.Buffer
	require.NoError(t, ExecuteList(context.Background(), &out, []TargetItem{target, envTarget}))
	assert.Contains(t, out.String(), filepath.Join(home, "dux-home-expansion-test"))
}

func TestDefaultTargetsIncludeAntigravityIDELogs(t *testing.T) {
	assert.Contains(t, DefaultTargets, TargetItem{
		Path:        "~/Library/Application Support/Antigravity IDE/logs",
		Description: "Antigravity IDE Logs (IDE 日誌 / IDE logs)",
	})
}

func TestFormatFullCmd(t *testing.T) {
	assert.Equal(t, `rm -rf "/tmp/foo"`, FormatFullCmd("/tmp/foo"))
}

func TestHumanSize(t *testing.T) {
	assert.Equal(t, "0B", HumanSize(0))
	assert.Equal(t, "1KB", HumanSize(1024))
	assert.Equal(t, "5MB", HumanSize(5*1024*1024))
	assert.Equal(t, "10GB", HumanSize(10*1024*1024*1024))
}

func TestExecuteList(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "DerivedData")
	require.NoError(t, os.MkdirAll(testPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(testPath, "cache.db"), []byte("test content"), 0644))

	targets := []TargetItem{
		{Path: testPath, Description: "Test Cache"},
	}

	var out bytes.Buffer
	err := ExecuteList(context.Background(), &out, targets)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "[列表模式 / List Mode]")
	assert.Contains(t, output, "Test Cache")
	assert.Contains(t, output, FormatFullCmd(testPath))
	assert.Contains(t, output, "待清理 (Pending)")

	// Ensure path was NOT deleted
	_, statErr := os.Stat(testPath)
	assert.NoError(t, statErr)
}

func TestExecuteDelete_InteractiveDefaultNo(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "DeviceLogs")
	require.NoError(t, os.MkdirAll(testPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(testPath, "log.txt"), []byte("log data"), 0644))

	targets := []TargetItem{
		{Path: testPath, Description: "Test Device Logs"},
	}

	var out bytes.Buffer
	// Simulate user hitting Enter (default N)
	err := ExecuteDelete(context.Background(), strings.NewReader("\n"), &out, targets, false)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "[y/N]:")
	assert.Contains(t, output, "已跳過 (Skipped)")

	// Path must still exist
	_, statErr := os.Stat(testPath)
	assert.NoError(t, statErr)
}

func TestExecuteDelete_InteractiveYes(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "iOS DeviceSupport")
	require.NoError(t, os.MkdirAll(testPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(testPath, "symbol.a"), []byte("data"), 0644))

	targets := []TargetItem{
		{Path: testPath, Description: "Test iOS Device Support"},
	}

	var out bytes.Buffer
	// Simulate user typing "y\n"
	err := ExecuteDelete(context.Background(), strings.NewReader("y\n"), &out, targets, false)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "已刪除 (Deleted)")

	// Path must be deleted
	_, statErr := os.Stat(testPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestExecuteDelete_ForceYes(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "UserData")
	require.NoError(t, os.MkdirAll(testPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(testPath, "user.json"), []byte("{}"), 0644))

	targets := []TargetItem{
		{Path: testPath, Description: "Test UserData"},
	}

	var out bytes.Buffer
	// Force yes flag (-y) without input
	err := ExecuteDelete(context.Background(), strings.NewReader(""), &out, targets, true)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "[直接刪除模式 / Direct Delete Mode]")
	assert.Contains(t, output, "已刪除 (Deleted)")

	// Path must be deleted
	_, statErr := os.Stat(testPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestTUI_ModelNavigationAndSelection(t *testing.T) {
	tmpDir := t.TempDir()
	testPath := filepath.Join(tmpDir, "TestDir")
	require.NoError(t, os.MkdirAll(testPath, 0755))

	targets := []TargetItem{
		{Path: testPath, Description: "Test Item"},
	}

	m := NewModel(targets)
	assert.Equal(t, 1, len(m.Items))
	assert.False(t, m.Items[0].Selected)

	// Toggle selection with space
	updatedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updatedModel.(Model)
	assert.True(t, m.Items[0].Selected)

	// Press 'd' to ask confirmation
	updatedModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updatedModel.(Model)
	assert.True(t, m.Confirming)

	// Press 'd' again to confirm delete
	updatedModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updatedModel.(Model)
	assert.False(t, m.Confirming)
	assert.True(t, m.Items[0].Deleted)

	// File should be deleted from filesystem
	_, statErr := os.Stat(testPath)
	assert.True(t, os.IsNotExist(statErr))
}
