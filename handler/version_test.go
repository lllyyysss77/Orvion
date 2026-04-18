package handler

import "testing"

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
