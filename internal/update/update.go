// Package update reports whether a newer release of this application exists.
//
// It only ever reports. Downloading a new version and putting it in place is
// deliberately left to the person: replacing a running application bundle is a
// good deal of machinery to get right, and the whole benefit of it here is
// saving one visit to a web page.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// LatestRelease is GitHub's endpoint for this repository.
const LatestRelease = "https://api.github.com/repos/AleG03/awsm-desktop/releases/latest"

// Development is the version a build that was never released reports.
//
// `make app` leaves it at this; only the packaging run from a tag sets a real
// one. A build with no version cannot be compared against anything, and saying
// so is more use than pretending it is ancient.
const Development = "dev"

// Result is what a check found.
type Result struct {
	// Current is this build's version, or Development.
	Current string `json:"current"`
	// Latest is the most recent published release, without its "v".
	Latest string `json:"latest"`
	// URL is that release's page.
	URL string `json:"url"`
	// Newer reports that Latest is above Current. False for a development
	// build, which has no place on the same scale.
	Newer bool `json:"newer"`
	// Comparable is false when the two versions cannot be placed on one scale
	// -- a development build, or a release tag this cannot read. The panel then
	// says so, rather than implying the running build is current.
	Comparable bool `json:"comparable"`
}

// Checker asks GitHub. The endpoint and the client are fields so a test can
// answer for itself instead of reaching the network.
type Checker struct {
	Endpoint string
	HTTP     *http.Client
}

// Check compares the running version against the latest published release.
func (c Checker) Check(ctx context.Context, current string) (Result, error) {
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = LatestRelease
	}
	client := c.HTTP
	if client == nil {
		// A button somebody pressed: it has to come back or give up while they
		// are still looking at it.
		client = &http.Client{Timeout: 10 * time.Second}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")

	response, err := client.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		// 403 here is nearly always the unauthenticated rate limit, which is
		// worth naming: it passes on its own and is not a fault to chase.
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
			return Result{}, fmt.Errorf("GitHub is rate limiting this machine; try again later")
		}
		return Result{}, fmt.Errorf("GitHub answered %s", response.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return Result{}, fmt.Errorf("could not read GitHub's answer: %w", err)
	}
	if release.TagName == "" {
		return Result{}, fmt.Errorf("GitHub reported no released version")
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	result := Result{
		Current: current,
		Latest:  latest,
		URL:     release.HTMLURL,
	}
	if current != "" && current != Development {
		result.Comparable, result.Newer = compare(latest, strings.TrimPrefix(current, "v"))
	}
	return result, nil
}

// compare places two versions on one scale, reporting whether it could and,
// if so, whether latest is above current.
//
// Deliberately narrow: leading numbers separated by dots, compared in order,
// with a missing part counting as zero so 1.7 and 1.7.0 agree. A version either
// side cannot be read means there is no answer to give -- which is reported as
// such, and not as "you are up to date". Silently calling an unreadable release
// tag "no update" would hide a broken tag for as long as it took somebody to
// notice they had stopped being offered versions.
func compare(latest, current string) (comparable, newer bool) {
	a, ok := numbers(latest)
	if !ok {
		return false, false
	}
	b, ok := numbers(current)
	if !ok {
		return false, false
	}

	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := part(a, i), part(b, i)
		if x != y {
			return true, x > y
		}
	}
	// Equal on numbers. A suffix on either side is a pre-release, and a
	// pre-release of the version already installed is not an upgrade.
	return true, false
}

func numbers(version string) ([]int, bool) {
	// A suffix such as -rc1 or +build is dropped before splitting: what
	// matters here is only whether the numbers ahead of it moved.
	if cut := strings.IndexAny(version, "-+"); cut >= 0 {
		version = version[:cut]
	}
	fields := strings.Split(strings.TrimSpace(version), ".")
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func part(values []int, i int) int {
	if i < len(values) {
		return values[i]
	}
	return 0
}
