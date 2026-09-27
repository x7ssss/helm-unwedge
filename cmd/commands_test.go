package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand(t *testing.T) {
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--help"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("rootCmd --help failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "helm-unwedge") {
		t.Errorf("expected helm-unwedge in help output, got: %s", out)
	}
	if !strings.Contains(out, "analyze") || !strings.Contains(out, "heal") || !strings.Contains(out, "auto") || !strings.Contains(out, "list-stuck") {
		t.Errorf("subcommands missing in help output: %s", out)
	}
}

func TestAnalyzeCommandValidation(t *testing.T) {
	analyzeCmd := newAnalyzeCmd()
	analyzeCmd.SetArgs([]string{}) // No release arg
	err := analyzeCmd.Execute()
	if err == nil {
		t.Errorf("expected error when no release name provided to analyze")
	}
}

func TestHealCommandValidation(t *testing.T) {
	healCmd := newHealCmd()
	healCmd.SetArgs([]string{}) // No release arg
	err := healCmd.Execute()
	if err == nil {
		t.Errorf("expected error when no release name provided to heal")
	}
}

func TestAutoCommandValidation(t *testing.T) {
	autoCmd := newAutoCmd()
	autoCmd.SetArgs([]string{}) // Missing required --release
	err := autoCmd.Execute()
	if err == nil {
		t.Errorf("expected error when --release flag is missing in auto")
	}
}
