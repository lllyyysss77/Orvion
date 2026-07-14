package models

import "testing"

func TestNetworkForwardingConfigEffectiveProxyURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  NetworkForwardingConfig
		want string
	}{
		{
			name: "优先使用全局代理",
			cfg: NetworkForwardingConfig{
				GlobalProxyURL:   " http://127.0.0.1:7890 ",
				TelegramProxyURL: "socks5://127.0.0.1:1080",
			},
			want: "http://127.0.0.1:7890",
		},
		{
			name: "兼容旧 TG 代理字段",
			cfg: NetworkForwardingConfig{
				TelegramProxyURL: " socks5://127.0.0.1:1080 ",
			},
			want: "socks5://127.0.0.1:1080",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveProxyURL(); got != tc.want {
				t.Fatalf("EffectiveProxyURL()=%q want=%q", got, tc.want)
			}
		})
	}
}
