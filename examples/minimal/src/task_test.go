package tasks

import "testing"

func TestTitleIsTrimmed(t *testing.T) {
	title, err := CreateTask("  Read a record  ")
	if err != nil || title != "Read a record" {
		t.Fatalf("CreateTask = (%q, %v)", title, err)
	}
}

func TestBlankTitleIsRefused(t *testing.T) {
	title, err := CreateTask(" \t ")
	if err == nil || title != "" {
		t.Fatalf("CreateTask = (%q, %v), want empty title and error", title, err)
	}
}
