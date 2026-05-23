package secrets

import "testing"

func TestCanonicalURI(t *testing.T) {
	tests := []struct {
		name    string
		rawPath string
		want    string
	}{
		{
			name:    "root path",
			rawPath: "/",
			want:    "/",
		},
		{
			name:    "empty path normalises to slash",
			rawPath: "",
			want:    "/",
		},
		{
			name:    "simple path with no special chars",
			rawPath: "/foo/bar",
			want:    "/foo/bar",
		},
		{
			name:    "unreserved chars are never encoded",
			rawPath: "/ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~",
			want:    "/ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~",
		},
		{
			name:    "colon must be encoded",
			rawPath: "/bucket:name/key",
			want:    "/bucket%3Aname/key",
		},
		{
			name:    "at-sign must be encoded",
			rawPath: "/user@host/key",
			want:    "/user%40host/key",
		},
		{
			name:    "exclamation mark must be encoded",
			rawPath: "/path/file!name",
			want:    "/path/file%21name",
		},
		{
			name:    "dollar sign must be encoded",
			rawPath: "/a/$b",
			want:    "/a/%24b",
		},
		{
			name:    "ampersand must be encoded",
			rawPath: "/a&b",
			want:    "/a%26b",
		},
		{
			name:    "single quote must be encoded",
			rawPath: "/it's",
			want:    "/it%27s",
		},
		{
			name:    "parentheses must be encoded",
			rawPath: "/f(x)/g(y)",
			want:    "/f%28x%29/g%28y%29",
		},
		{
			name:    "asterisk must be encoded",
			rawPath: "/a*b",
			want:    "/a%2Ab",
		},
		{
			name:    "plus must be encoded",
			rawPath: "/a+b",
			want:    "/a%2Bb",
		},
		{
			name:    "comma must be encoded",
			rawPath: "/a,b",
			want:    "/a%2Cb",
		},
		{
			name:    "semicolon must be encoded",
			rawPath: "/a;b",
			want:    "/a%3Bb",
		},
		{
			name:    "equals must be encoded",
			rawPath: "/a=b",
			want:    "/a%3Db",
		},
		{
			name:    "space must be encoded as %20 not plus",
			rawPath: "/hello world",
			want:    "/hello%20world",
		},
		{
			name:    "double slash is preserved",
			rawPath: "/a//b",
			want:    "/a//b",
		},
		{
			name:    "trailing slash is preserved",
			rawPath: "/foo/",
			want:    "/foo/",
		},
		{
			name:    "multibyte UTF-8 character each byte encoded",
			rawPath: "/caf\xc3\xa9",
			want:    "/caf%C3%A9",
		},
		{
			name:    "hex digits in encoding are uppercase",
			rawPath: "/\xde\xad\xbe\xef",
			want:    "/%DE%AD%BE%EF",
		},
		{
			name:    "mixed unreserved and reserved in same segment",
			rawPath: "/foo:bar-baz.qux~quux/ok",
			want:    "/foo%3Abar-baz.qux~quux/ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := canonicalURI(tc.rawPath)
			if got != tc.want {
				t.Errorf("canonicalURI(%q) = %q, want %q", tc.rawPath, got, tc.want)
			}
		})
	}
}
