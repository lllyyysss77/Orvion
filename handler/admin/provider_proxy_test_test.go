package admin

import "testing"

func TestParseProviderProxyExitResponseIPAPI(t *testing.T) {
	info, err := parseProviderProxyExitResponse([]byte(`{"ip":"203.0.113.8","country_name":"Japan","country_code":"JP"}`))
	if err != nil {
		t.Fatalf("解析 ipapi 响应失败: %v", err)
	}
	if info.IP != "203.0.113.8" || info.Country != "Japan" || info.CountryCode != "JP" {
		t.Fatalf("出口信息解析不正确: %+v", info)
	}
}

func TestParseProviderProxyExitResponseIPInfo(t *testing.T) {
	info, err := parseProviderProxyExitResponse([]byte(`{"ip":"198.51.100.9","country":"US"}`))
	if err != nil {
		t.Fatalf("解析 ipinfo 响应失败: %v", err)
	}
	if info.IP != "198.51.100.9" || info.Country != "" || info.CountryCode != "US" {
		t.Fatalf("出口信息解析不正确: %+v", info)
	}
}

func TestParseProviderProxyExitResponseRequiresIP(t *testing.T) {
	if _, err := parseProviderProxyExitResponse([]byte(`{"country_name":"Japan"}`)); err == nil {
		t.Fatalf("缺少 IP 时应返回错误")
	}
}
