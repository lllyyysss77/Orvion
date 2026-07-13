package main

import "testing"

func TestValidateStartupSecurity(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		environment string
		wantErr     bool
	}{
		{name: "开发模式允许空 Token", environment: "development"},
		{name: "未指定环境允许空 Token"},
		{name: "生产模式拒绝空 Token", environment: "production", wantErr: true},
		{name: "生产模式别名拒绝空 Token", environment: " PROD ", wantErr: true},
		{name: "生产模式接受有效 Token", token: "secret", environment: "production"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStartupSecurity(tc.token, tc.environment)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateStartupSecurity() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
