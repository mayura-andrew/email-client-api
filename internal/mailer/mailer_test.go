package mailer

import (
	"errors"
	"io"
	"net/textproto"
	"strings"
	"testing"
)

func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unknown mailbox", &textproto.Error{Code: 550, Msg: "5.1.1 user unknown"}, true},
		{"greylisted", &textproto.Error{Code: 451, Msg: "4.7.1 try again later"}, false},
		{"bad credentials", &textproto.Error{Code: 535, Msg: "5.7.8 Username and Password not accepted"}, false},
		{"gmail daily quota", &textproto.Error{Code: 550, Msg: "5.4.5 Daily user sending quota exceeded"}, false},
		{"connection dropped", io.EOF, false},
		{"other", errors.New("dial tcp: i/o timeout"), false},
	}
	for _, tc := range cases {
		if got := IsPermanent(tc.err); got != tc.want {
			t.Errorf("%s: IsPermanent = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !IsQuotaExceeded(&textproto.Error{Code: 550, Msg: "5.4.5 Daily user sending quota exceeded"}) {
		t.Error("Gmail quota reply should be detected")
	}
}

func TestRenderTrackedUsesPublicURL(t *testing.T) {
	html, err := RenderTracked(TrackedEmailData{Recipient: "a@example.com", Body: "<b>hi</b>", EmailId: 42, URL: "https://mail.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "https://mail.example.org/api/v1/redirect?id=42") {
		t.Fatal("tracking link must use the configured public URL")
	}
	if strings.Contains(html, "<b>hi</b>") {
		t.Fatal("body must be HTML-escaped")
	}
}
