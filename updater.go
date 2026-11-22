package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	defaultReleaseAPIEndpoint = "https://api.github.com/repos/zuoban/z-pic/releases/latest"
	defaultReleasePageURL     = "https://github.com/zuoban/z-pic/releases"
	maxReleaseResponseBytes   = 512 << 10
)

type updateCheckState int

const (
	updateIdle updateCheckState = iota
	updateChecking
	updateUpToDate
	updateAvailable
	updateFailed
)

type updateCheckResult struct {
	CurrentVersion string
	LatestVersion  string
	TagName        string
	ReleaseName    string
	ReleaseURL     string
	PublishedAt    string
	HasUpdate      bool
	NoReleaseYet   bool
}

type updateCheckerConfig struct {
	endpoint       string
	currentVersion string
	client         *http.Client
}

type githubReleasePayload struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
}

type semverVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
}

func parseSemver(raw string) (semverVersion, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if s == "" {
		return semverVersion{}, fmt.Errorf("无效的版本号：%q", raw)
	}
	if plus := strings.IndexByte(s, '+'); plus >= 0 {
		s = s[:plus]
	}
	var prerelease []string
	if dash := strings.IndexByte(s, '-'); dash >= 0 {
		pre := s[dash+1:]
		if pre == "" {
			return semverVersion{}, fmt.Errorf("无效的版本号：%q", raw)
		}
		prerelease = strings.Split(pre, ".")
		for _, part := range prerelease {
			if part == "" {
				return semverVersion{}, fmt.Errorf("无效的版本号：%q", raw)
			}
		}
		s = s[:dash]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semverVersion{}, fmt.Errorf("无效的版本号：%q", raw)
	}
	var nums [3]int
	for i, part := range parts {
		if !isASCIIUnsignedDigits(part) {
			return semverVersion{}, fmt.Errorf("无效的版本号：%q", raw)
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return semverVersion{}, fmt.Errorf("无效的版本号：%q", raw)
		}
		nums[i] = v
	}
	return semverVersion{
		major:      nums[0],
		minor:      nums[1],
		patch:      nums[2],
		prerelease: prerelease,
	}, nil
}

func isASCIIUnsignedDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func compareSemver(a, b string) (int, error) {
	va, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	if va.major != vb.major {
		return cmpInt(va.major, vb.major), nil
	}
	if va.minor != vb.minor {
		return cmpInt(va.minor, vb.minor), nil
	}
	if va.patch != vb.patch {
		return cmpInt(va.patch, vb.patch), nil
	}
	aPre, bPre := len(va.prerelease) > 0, len(vb.prerelease) > 0
	if !aPre && !bPre {
		return 0, nil
	}
	if !aPre && bPre {
		return 1, nil
	}
	if aPre && !bPre {
		return -1, nil
	}
	n := min(len(va.prerelease), len(vb.prerelease))
	for i := range n {
		pa, pb := va.prerelease[i], vb.prerelease[i]
		if pa == pb {
			continue
		}
		na, nb := isASCIIUnsignedDigits(pa), isASCIIUnsignedDigits(pb)
		switch {
		case na && nb:
			ia, errA := strconv.Atoi(pa)
			ib, errB := strconv.Atoi(pb)
			if errA == nil && errB == nil {
				return cmpInt(ia, ib), nil
			}
			return strings.Compare(pa, pb), nil
		case na && !nb:
			return -1, nil
		case !na && nb:
			return 1, nil
		default:
			return strings.Compare(pa, pb), nil
		}
	}
	return cmpInt(len(va.prerelease), len(vb.prerelease)), nil
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func normalizeVersionLabel(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	return s
}

func sanitizeReleaseURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || !strings.EqualFold(parsed.Host, "github.com") {
		return defaultReleasePageURL
	}
	if strings.Contains(parsed.Path, "..") {
		return defaultReleasePageURL
	}
	cleanPath := path.Clean(parsed.Path)
	if cleanPath != "/zuoban/z-pic/releases" && !strings.HasPrefix(cleanPath, "/zuoban/z-pic/releases/") {
		return defaultReleasePageURL
	}
	return parsed.String()
}

func checkForUpdates(ctx context.Context, cfg updateCheckerConfig) (updateCheckResult, error) {
	current := normalizeVersionLabel(cfg.currentVersion)
	if current == "" {
		current = normalizeVersionLabel(appVersion)
	}
	endpoint := strings.TrimSpace(cfg.endpoint)
	if endpoint == "" {
		endpoint = defaultReleaseAPIEndpoint
	}
	client := cfg.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return updateCheckResult{}, fmt.Errorf("无法创建更新请求：%w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "z-pic/"+current)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return updateCheckResult{}, context.Canceled
		}
		return updateCheckResult{}, fmt.Errorf("检查更新失败：无法连接更新服务器")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return updateCheckResult{
			CurrentVersion: current,
			LatestVersion:  current,
			ReleaseURL:     defaultReleasePageURL,
			HasUpdate:      false,
			NoReleaseYet:   true,
		}, nil
	case http.StatusForbidden, http.StatusTooManyRequests:
		return updateCheckResult{}, errors.New("检查更新失败：请求过于频繁，请稍后再试")
	default:
		return updateCheckResult{}, fmt.Errorf("检查更新失败：服务器返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseResponseBytes+1))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return updateCheckResult{}, context.Canceled
		}
		return updateCheckResult{}, fmt.Errorf("读取更新响应失败：%w", err)
	}
	if len(body) > maxReleaseResponseBytes {
		return updateCheckResult{}, errors.New("检查更新失败：更新响应超出大小限制")
	}

	var payload githubReleasePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return updateCheckResult{}, errors.New("检查更新失败：无法解析版本信息")
	}
	if payload.Draft || payload.Prerelease || strings.TrimSpace(payload.TagName) == "" {
		return updateCheckResult{
			CurrentVersion: current,
			LatestVersion:  current,
			ReleaseURL:     defaultReleasePageURL,
			HasUpdate:      false,
			NoReleaseYet:   true,
		}, nil
	}

	cmp, err := compareSemver(payload.TagName, current)
	if err != nil {
		return updateCheckResult{}, fmt.Errorf("检查更新失败：%w", err)
	}
	latest := normalizeVersionLabel(payload.TagName)
	return updateCheckResult{
		CurrentVersion: current,
		LatestVersion:  latest,
		TagName:        payload.TagName,
		ReleaseName:    strings.TrimSpace(payload.Name),
		ReleaseURL:     sanitizeReleaseURL(payload.HTMLURL),
		PublishedAt:    strings.TrimSpace(payload.PublishedAt),
		HasUpdate:      cmp > 0,
	}, nil
}
