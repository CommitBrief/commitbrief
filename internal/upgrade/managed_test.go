// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCommandPerMethod(t *testing.T) {
	cases := []struct {
		m    Method
		want []string
	}{
		{MethodHomebrew, []string{"brew", "upgrade", "commitbrief"}},
		{MethodScoop, []string{"scoop", "update", "commitbrief"}},
		{MethodGoInstall, []string{"go", "install", "github.com/CommitBrief/commitbrief/cmd/commitbrief@latest"}},
		{MethodManual, nil},
	}
	for _, c := range cases {
		if got := Command(c.m); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("Command(%q) = %v, want %v", c.m, got, c.want)
		}
	}
}

func TestRunReportsMissingTool(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"commitbrief-no-such-tool", "upgrade"}, &out, &errOut)
	if !errors.Is(err, ErrToolMissing) {
		t.Fatalf("error = %v, want ErrToolMissing", err)
	}
}

func TestRunStreamsOutput(t *testing.T) {
	var out, errOut bytes.Buffer
	// `go version` exists wherever the test suite itself can run.
	if err := Run(context.Background(), []string{"go", "version"}, &out, &errOut); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("expected the delegated command's stdout to be streamed")
	}
}

func TestRunPropagatesFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"go", "this-is-not-a-go-subcommand"}, &out, &errOut)
	if err == nil {
		t.Fatal("Run() error = nil, want the delegated command's failure")
	}
	if errors.Is(err, ErrToolMissing) {
		t.Fatalf("error = %v, want an exit failure, not ErrToolMissing", err)
	}
}

func TestRunRejectsEmptyArgv(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), nil, &out, &errOut); err == nil {
		t.Fatal("Run(nil) error = nil, want a rejection")
	}
}
