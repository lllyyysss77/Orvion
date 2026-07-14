package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type versionCheckRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn versionCheckRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestIsLatestVersionGreater(t *testing.T) {
	cases := []struct {
		name    string
		latest  string
		current string
		want    bool
	}{
		{name: "主版本升级", latest: "v2.0.0", current: "v1.9.9", want: true},
		{name: "次版本升级", latest: "1.10.0", current: "1.9.9", want: true},
		{name: "补丁升级", latest: "1.2.4", current: "1.2.3", want: true},
		{name: "版本相同", latest: "1.2.3", current: "1.2.3", want: false},
		{name: "低版本不提示", latest: "1.2.2", current: "1.2.3", want: false},
		{name: "当前 dev 视为可升级", latest: "1.2.3", current: "dev", want: true},
		{name: "当前 dev 且最新版本非语义也提示", latest: "latest", current: "dev", want: true},
		{name: "当前 dev 但最新版本为空不提示", latest: "", current: "dev", want: false},
		{name: "最新版本非法不提示", latest: "latest", current: "1.2.3", want: false},
		{name: "正式版大于预发布", latest: "1.2.3", current: "1.2.3-beta", want: true},
		{name: "预发布小于正式版", latest: "1.2.3-beta", current: "1.2.3", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isLatestVersionGreater(tc.latest, tc.current)
			if got != tc.want {
				t.Fatalf("latest=%q current=%q got=%v want=%v", tc.latest, tc.current, got, tc.want)
			}
		})
	}
}

func TestParseNormalizedVersion(t *testing.T) {
	ver := parseNormalizedVersion("v1.2")
	if !ver.valid {
		t.Fatalf("expected valid version")
	}
	if ver.major != 1 || ver.minor != 2 || ver.patch != 0 {
		t.Fatalf("unexpected parsed result: %+v", ver)
	}
}

func TestBuildTagUpdateBodyPreferCommitMessage(t *testing.T) {
	got := buildTagUpdateBody("v0.8.0", "93c279e0d6e3", "feat: 优化版本提示交互")
	if got != "feat: 优化版本提示交互" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestBuildVersionUpdateCheckRespSuccess(t *testing.T) {
	latest := &githubLatestTagInfo{
		TagName:       "v1.2.3",
		CommitSHA:     "93c279e0d6e3",
		CommitMessage: "feat: add cache",
		HTMLURL:       "https://github.com/raciott/llmio/commit/93c279e0d6e3",
	}

	resp := buildVersionUpdateCheckResp("1.2.2", latest, nil)
	if !resp.BackendFetchSuccess {
		t.Fatalf("expected backendFetchSuccess=true")
	}
	if resp.SuggestBrowserFetch {
		t.Fatalf("expected suggestBrowserFetch=false")
	}
	if !resp.HasUpdate {
		t.Fatalf("expected hasUpdate=true")
	}
	if resp.LatestVersion != "v1.2.3" {
		t.Fatalf("unexpected latestVersion: %q", resp.LatestVersion)
	}
	if resp.Release == nil || resp.Release.Body != "feat: add cache" {
		t.Fatalf("unexpected release body: %+v", resp.Release)
	}
}

func TestBuildVersionUpdateCheckRespErrorForDev(t *testing.T) {
	resp := buildVersionUpdateCheckResp("dev", nil, errTestDummy)
	if resp.BackendFetchSuccess {
		t.Fatalf("expected backendFetchSuccess=false")
	}
	if !resp.SuggestBrowserFetch {
		t.Fatalf("expected suggestBrowserFetch=true")
	}
	if !resp.HasUpdate {
		t.Fatalf("expected hasUpdate=true for dev")
	}
	if resp.Release == nil || resp.Release.TagName != "latest" {
		t.Fatalf("unexpected release fallback: %+v", resp.Release)
	}
}

func TestBuildVersionUpdateCheckRespErrorForStableVersion(t *testing.T) {
	resp := buildVersionUpdateCheckResp("1.2.3", nil, errTestDummy)
	if resp.BackendFetchSuccess {
		t.Fatalf("expected backendFetchSuccess=false")
	}
	if !resp.SuggestBrowserFetch {
		t.Fatalf("expected suggestBrowserFetch=true")
	}
	if resp.HasUpdate {
		t.Fatalf("expected hasUpdate=false for stable version on fetch error")
	}
	if resp.Release != nil {
		t.Fatalf("expected release=nil, got %+v", resp.Release)
	}
}

func TestDisabledVersionUpdateCheckResp(t *testing.T) {
	resp := disabledVersionUpdateCheckResp()
	if !resp.Disabled {
		t.Fatalf("expected disabled=true")
	}
	if resp.HasUpdate {
		t.Fatalf("expected hasUpdate=false")
	}
	if resp.SuggestBrowserFetch {
		t.Fatalf("expected suggestBrowserFetch=false")
	}
	if resp.FetchSource != "disabled" {
		t.Fatalf("unexpected fetchSource: %q", resp.FetchSource)
	}
}

func TestLatestTagInfoFromVersionServiceResponse(t *testing.T) {
	raw := `{
		"success": true,
		"repository": "raciott/Orvion",
		"latest": {
			"tag": "v1.2.7",
			"title": "feat(core): 强化核心能力",
			"description": "- 更新说明",
			"message": "feat(core): 强化核心能力\n\n- 更新说明",
			"published_at": "2026-07-13T15:00:48Z",
			"commit_sha": "70d877379cc91a90831b4ace979f8c6f395c26f8",
			"commit_url": "https://github.com/raciott/Orvion/commit/70d877379cc91a90831b4ace979f8c6f395c26f8",
			"source_url": "https://github.com/raciott/Orvion/tree/v1.2.7"
		}
	}`

	var payload githubVersionServiceResp
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	latest, err := latestTagInfoFromServiceResp(payload)
	if err != nil {
		t.Fatalf("map response: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest version")
	}
	if latest.TagName != "v1.2.7" || latest.Title != "feat(core): 强化核心能力" {
		t.Fatalf("unexpected version info: %+v", latest)
	}
	if latest.Description != "- 更新说明" || latest.PublishedAt != "2026-07-13T15:00:48Z" {
		t.Fatalf("unexpected release info: %+v", latest)
	}
	if latest.HTMLURL != "https://github.com/raciott/Orvion/tree/v1.2.7" {
		t.Fatalf("unexpected release url: %q", latest.HTMLURL)
	}

	resp := buildVersionUpdateCheckResp("v1.2.6", latest, nil)
	if !resp.HasUpdate || resp.Release == nil {
		t.Fatalf("expected update response: %+v", resp)
	}
	if resp.Release.Name != latest.Title || resp.Release.Body != latest.Description || resp.Release.PublishedAt != latest.PublishedAt {
		t.Fatalf("unexpected release mapping: %+v", resp.Release)
	}
}

func TestLatestTagInfoFromVersionServiceRejectsFailure(t *testing.T) {
	if _, err := latestTagInfoFromServiceResp(githubVersionServiceResp{}); err == nil {
		t.Fatal("expected success=false to return an error")
	}
}

func TestGetVersionServiceJSONFallsBackToProxy(t *testing.T) {
	directCalls := 0
	proxyCalls := 0
	directClient := &http.Client{Transport: versionCheckRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		directCalls++
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad gateway"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	proxyClient := &http.Client{Transport: versionCheckRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		proxyCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"repository":"raciott/Orvion","latest":{"tag":"v1.2.7"}}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	var payload githubVersionServiceResp
	err := getVersionServiceJSONWithFallback("https://version.example.test", &payload, directClient, proxyClient)
	if err != nil {
		t.Fatalf("expected proxy fallback success, got %v", err)
	}
	if directCalls != githubRequestRetryMax {
		t.Fatalf("direct calls=%d want=%d", directCalls, githubRequestRetryMax)
	}
	if proxyCalls != 1 {
		t.Fatalf("proxy calls=%d want=1", proxyCalls)
	}
	if !payload.Success || payload.Latest == nil || payload.Latest.Tag != "v1.2.7" {
		t.Fatalf("unexpected proxy payload: %+v", payload)
	}
}

func TestGetVersionServiceJSONDoesNotUseProxyAfterDirectSuccess(t *testing.T) {
	proxyCalls := 0
	directClient := &http.Client{Transport: versionCheckRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"latest":{"tag":"v1.2.7"}}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	proxyClient := &http.Client{Transport: versionCheckRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		proxyCalls++
		return nil, errors.New("proxy should not be called")
	})}

	var payload githubVersionServiceResp
	if err := getVersionServiceJSONWithFallback("https://version.example.test", &payload, directClient, proxyClient); err != nil {
		t.Fatalf("expected direct success, got %v", err)
	}
	if proxyCalls != 0 {
		t.Fatalf("proxy calls=%d want=0", proxyCalls)
	}
}

var errTestDummy = testDummyError("dummy")

type testDummyError string

func (e testDummyError) Error() string { return string(e) }
