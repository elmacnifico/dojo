package cliargv

import "testing"

func TestHelpRequested(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"./suite"}, false},
		{[]string{"--help"}, true},
		{[]string{"-help"}, true},
		{[]string{"-h"}, true},
		{[]string{"run", "--help"}, true},
		{[]string{"run", "-h"}, true},
		{[]string{"./my_suite", "--help"}, true},
		{[]string{"--format", "json", "--help"}, true},
		{[]string{"--help=false"}, false},
		{[]string{"--helpx"}, false},
	}
	for _, tt := range tests {
		if got := HelpRequested(tt.argv); got != tt.want {
			t.Errorf("HelpRequested(%v) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}
