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
