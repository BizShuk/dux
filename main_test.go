package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRootCmdRegistersExpectedCommands(t *testing.T) {
	commandNames := make([]string, 0, len(RootCmd.Commands()))
	for _, command := range RootCmd.Commands() {
		commandNames = append(commandNames, command.Name())
	}

	assert.ElementsMatch(t, []string{
		"config",
		"diff",
		"du",
		"scan",
		"show",
		"tui",
	}, commandNames)
}
