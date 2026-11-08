package oauthx

import "testing"

func TestParseCodePaste(t *testing.T) {
	for _, in := range []string{
		"https://platform.claude.com/oauth/code/callback?code=c1&state=s1",
		" c1#s1 ",
		"code=c1&state=s1",
	} {
		got, ok := ParseCodePaste(in)
		if !ok || got != (CodePaste{"c1", "s1"}) {
			t.Errorf("%q -> %+v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "c1", "a#b#c", "code=c1"} {
		if _, ok := ParseCodePaste(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}
