package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bizshuk/dux/du"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultScanRoots(t *testing.T) {
	assert.Equal(t, []string{"/home/test", "/tmp", "/var", "/Applications", "/usr"},
		defaultScanRoots("/home/test"))
}

func TestScanAllRootsRunsConcurrentlyAndMergesEntries(t *testing.T) {
	roots := []string{"/one", "/two", "/three"}
	started := make(chan string, len(roots))
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	usage := func(_ context.Context, opts du.Options) (*du.Result, error) {
		root := opts.Paths[0]
		started <- root
		<-release
		return &du.Result{Entries: []du.EntryInfo{{Path: root}}}, nil
	}

	type outcome struct {
		entries  []du.EntryInfo
		failures int
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		entries, failures, err := scanAllRoots(context.Background(), roots, du.Options{}, usage)
		done <- outcome{entries: entries, failures: failures, err: err}
	}()

	seen := make([]string, 0, len(roots))
	for range roots {
		select {
		case root := <-started:
			seen = append(seen, root)
		case <-time.After(time.Second):
			t.Fatal("roots did not start concurrently")
		}
	}
	close(release)

	result := <-done
	require.NoError(t, result.err)
	assert.Zero(t, result.failures)
	assert.ElementsMatch(t, roots, seen)
	require.Len(t, result.entries, len(roots))
	assert.ElementsMatch(t, roots, []string{
		result.entries[0].Path,
		result.entries[1].Path,
		result.entries[2].Path,
	})
}

func TestScanAllRootsKeepsSuccessfulEntries(t *testing.T) {
	usage := func(_ context.Context, opts du.Options) (*du.Result, error) {
		if opts.Paths[0] == "/bad" {
			return nil, errors.New("unavailable")
		}
		return &du.Result{Entries: []du.EntryInfo{{Path: opts.Paths[0]}}}, nil
	}

	entries, failures, err := scanAllRoots(
		context.Background(), []string{"/good", "/bad"}, du.Options{}, usage,
	)

	require.NoError(t, err)
	assert.Equal(t, 1, failures)
	require.Len(t, entries, 1)
	assert.Equal(t, "/good", entries[0].Path)
}

func TestScanAllRootsErrorsWhenEveryRootFails(t *testing.T) {
	usage := func(context.Context, du.Options) (*du.Result, error) {
		return nil, errors.New("unavailable")
	}

	entries, failures, err := scanAllRoots(
		context.Background(), []string{"/one", "/two"}, du.Options{}, usage,
	)

	assert.Nil(t, entries)
	assert.Equal(t, 2, failures)
	assert.EqualError(t, err, "scan: all 2 roots failed")
}
