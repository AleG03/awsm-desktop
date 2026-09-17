package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serving(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCheckFindsANewerRelease(t *testing.T) {
	server := serving(t, http.StatusOK,
		`{"tag_name":"v0.5.0","html_url":"https://github.com/AleG03/awsm-desktop/releases/tag/v0.5.0"}`)

	got, err := Checker{Endpoint: server.URL}.Check(context.Background(), "0.4.1")
	if err != nil {
		t.Fatalf("Check() = %v", err)
	}
	if !got.Newer {
		t.Error("0.5.0 should be newer than 0.4.1")
	}
	if got.Latest != "0.5.0" {
		t.Errorf("Latest = %q, want the tag without its v", got.Latest)
	}
	if !strings.HasPrefix(got.URL, "https://github.com/") {
		t.Errorf("URL = %q", got.URL)
	}
	if !got.Comparable {
		t.Error("a real version is comparable")
	}
}

func TestCheckOnTheCurrentVersion(t *testing.T) {
	server := serving(t, http.StatusOK, `{"tag_name":"v0.4.1","html_url":"https://example.com"}`)

	got, err := Checker{Endpoint: server.URL}.Check(context.Background(), "v0.4.1")
	if err != nil {
		t.Fatalf("Check() = %v", err)
	}
	if got.Newer {
		t.Error("the version already installed is not an update")
	}
}

// TestCheckOnADevelopmentBuild: `make app` produces a build with no version.
// Comparing it would either nag on every press or claim it is up to date, and
// both are worse than saying which case this is.
func TestCheckOnADevelopmentBuild(t *testing.T) {
	server := serving(t, http.StatusOK, `{"tag_name":"v9.9.9","html_url":"https://example.com"}`)

	got, err := Checker{Endpoint: server.URL}.Check(context.Background(), Development)
	if err != nil {
		t.Fatalf("Check() = %v", err)
	}
	if got.Comparable {
		t.Error("a development build has no place on the release scale")
	}
	if got.Newer {
		t.Error("a development build must not be reported as out of date")
	}
	if got.Latest != "9.9.9" {
		t.Errorf("Latest = %q: the newest release is still worth showing", got.Latest)
	}
}

// TestCheckNamesTheRateLimit: unauthenticated GitHub allows sixty requests an
// hour, and hitting it is not a fault to chase.
func TestCheckNamesTheRateLimit(t *testing.T) {
	server := serving(t, http.StatusForbidden, `{"message":"API rate limit exceeded"}`)

	_, err := Checker{Endpoint: server.URL}.Check(context.Background(), "0.4.1")
	if err == nil {
		t.Fatal("Check() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error = %q, want it to name the rate limit", err)
	}
}

func TestCheckOnABadAnswer(t *testing.T) {
	for name, body := range map[string]string{
		"not json":    `<html>nope</html>`,
		"no tag name": `{"html_url":"https://example.com"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := serving(t, http.StatusOK, body)
			if _, err := (Checker{Endpoint: server.URL}).Check(context.Background(), "0.4.1"); err == nil {
				t.Error("Check() = nil, want an error")
			}
		})
	}
}

// TestCheckOnAReleaseTagItCannotRead: a tag that is not a version means there
// is no comparison to make. Reporting "no update" would hide a broken tag for
// as long as it took somebody to notice they had stopped being offered any.
func TestCheckOnAReleaseTagItCannotRead(t *testing.T) {
	server := serving(t, http.StatusOK, `{"tag_name":"nightly","html_url":"https://example.com"}`)

	got, err := Checker{Endpoint: server.URL}.Check(context.Background(), "0.4.1")
	if err != nil {
		t.Fatalf("Check() = %v", err)
	}
	if got.Comparable {
		t.Error("claimed to have compared against a tag that is not a version")
	}
	if got.Newer {
		t.Error("reported an update it could not establish")
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.5.0", "0.4.1", true},
		{"1.0.0", "0.9.9", true},
		{"0.4.2", "0.4.1", true},
		{"0.4.1", "0.4.1", false},
		{"0.4.0", "0.4.1", false},
		{"0.9.9", "1.0.0", false},
		// A missing part counts as zero, so these are the same version.
		{"1.7", "1.7.0", false},
		{"1.7.0", "1.7", false},
		{"1.7.1", "1.7", true},
		// Two digits must not be compared as text, where "10" sorts below "9".
		{"0.10.0", "0.9.0", true},
		{"0.9.0", "0.10.0", false},
		// A pre-release of what is installed is not an upgrade.
		{"1.7.0-rc1", "1.7.0", false},
		{"1.8.0-rc1", "1.7.0", true},
		// Nothing readable: never tell somebody they are out of date on a
		// version string this does not understand.
		{"banana", "0.4.1", false},
		{"0.5.0", "banana", false},
		{"", "0.4.1", false},
	}

	for _, c := range cases {
		t.Run(c.latest+" over "+c.current, func(t *testing.T) {
			if _, got := compare(c.latest, c.current); got != c.want {
				t.Errorf("compare(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
			}
		})
	}
}
