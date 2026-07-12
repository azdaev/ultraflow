package agent

import "testing"

func TestMergedPATH(t *testing.T) {
	cases := []struct {
		name           string
		ambient, login string
		want           string
	}{
		{
			// The real bug: the daemon's PATH has the Superset shim but not nvm;
			// the login shell supplies nvm. The merge must surface nvm first while
			// keeping the shim dir, so codex resolves to the real binary.
			name:    "adds login dirs ahead of ambient, keeps ambient",
			ambient: "/x/.superset/bin:/usr/bin",
			login:   "/x/.nvm/bin:/x/.superset/bin:/usr/bin",
			want:    "/x/.nvm/bin:/x/.superset/bin:/usr/bin",
		},
		{
			name:    "dedups repeated dirs, drops empties",
			ambient: "/a::/b:/a",
			login:   "/b:/c",
			want:    "/b:/c:/a",
		},
		{
			name:    "empty login leaves ambient order",
			ambient: "/a:/b",
			login:   "",
			want:    "/a:/b",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mergedPATH(c.ambient, c.login); got != c.want {
				t.Fatalf("mergedPATH(%q, %q) = %q, want %q", c.ambient, c.login, got, c.want)
			}
		})
	}
}

func TestReplaceEnv(t *testing.T) {
	got := replaceEnv([]string{"A=1", "PATH=/old", "B=2"}, "PATH", "/new")
	want := []string{"A=1", "PATH=/new", "B=2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replaceEnv replace: got %v, want %v", got, want)
		}
	}

	got = replaceEnv([]string{"A=1"}, "PATH", "/new")
	if len(got) != 2 || got[1] != "PATH=/new" {
		t.Fatalf("replaceEnv append: got %v", got)
	}
}
