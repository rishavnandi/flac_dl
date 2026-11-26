package main

import (
	"reflect"
	"testing"
)

func TestSplitFlags(t *testing.T) {
	cases := []struct {
		args  []string
		flags []string
		rest  []string
	}{
		{[]string{"-n", "5", "tron", "legacy"}, []string{"-n", "5"}, []string{"tron", "legacy"}},
		{[]string{"tron", "legacy", "-n", "5"}, []string{"-n", "5"}, []string{"tron", "legacy"}},
		{[]string{"tron", "--lrc", "-q", "flac"}, []string{"--lrc", "-q", "flac"}, []string{"tron"}},
		{[]string{"-p=wyy", "daft"}, []string{"-p=wyy"}, []string{"daft"}},
		{[]string{"2407050"}, nil, []string{"2407050"}},
	}
	for _, tc := range cases {
		flags, rest := splitFlags(tc.args)
		if !reflect.DeepEqual(flags, tc.flags) || !reflect.DeepEqual(rest, tc.rest) {
			t.Fatalf("%v: got flags %v rest %v", tc.args, flags, rest)
		}
	}
}

func TestServeRejectsUnsafeConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-host", "https://example.ts.net"},
		{"-host", "example.ts.net/path"},
		{"-host", "user@example.ts.net"},
		{"-host", ":10000"},
		{"-host", "example.ts.net", "-addr", "0.0.0.0:8765"},
		{"-host", "example.ts.net", "unexpected"},
	} {
		if err := cmdServe(args); err == nil {
			t.Fatalf("serve accepted unsafe args %q", args)
		}
	}
}
