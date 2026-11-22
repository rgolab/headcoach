package version

import (
	"runtime/debug"
	"testing"
)

func TestFromModule(t *testing.T) {
	module := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	for _, tc := range []struct {
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{module("v0.2.0"), true, "v0.2.0"},
		{module("(devel)"), true, "dev"},
		{module(""), true, "dev"},
		{nil, false, "dev"},
	} {
		if got := fromModule(tc.info, tc.ok); got != tc.want {
			t.Errorf("fromModule(%v, %v) = %q, want %q", tc.info, tc.ok, got, tc.want)
		}
	}
}
