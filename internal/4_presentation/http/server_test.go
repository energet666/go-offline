package httphandlers

import (
	"fmt"
	"strings"
	"testing"
)

func TestDownloadStateLogsKeepHeadAndTail(t *testing.T) {
	var ds downloadState
	total := maxLogLines * 3
	for i := range total {
		ds.logf("line %d", i)
	}

	logs := ds.snapshot().Logs
	if len(logs) != maxLogLines+1 { // +1 for the elision marker
		t.Fatalf("got %d lines, want %d", len(logs), maxLogLines+1)
	}
	if !strings.HasSuffix(logs[0], "line 0") {
		t.Errorf("first line is %q, want the start of the job", logs[0])
	}
	if !strings.HasSuffix(logs[headLogLines-1], fmt.Sprintf("line %d", headLogLines-1)) {
		t.Errorf("head was not kept intact: %q", logs[headLogLines-1])
	}
	if !strings.Contains(logs[headLogLines], "пропущено") {
		t.Errorf("no elision marker after the head: %q", logs[headLogLines])
	}
	if !strings.HasSuffix(logs[len(logs)-1], fmt.Sprintf("line %d", total-1)) {
		t.Errorf("last line is %q, want the most recent one", logs[len(logs)-1])
	}
}

func TestDownloadStateLogsBelowLimitAreUntouched(t *testing.T) {
	var ds downloadState
	for i := range headLogLines {
		ds.logf("line %d", i)
	}

	logs := ds.snapshot().Logs
	if len(logs) != headLogLines {
		t.Fatalf("got %d lines, want %d", len(logs), headLogLines)
	}
	for _, line := range logs {
		if strings.Contains(line, "пропущено") {
			t.Fatalf("unexpected elision marker: %q", line)
		}
	}
}
