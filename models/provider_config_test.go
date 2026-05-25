package models

import (
	"encoding/json"
	"testing"
)

func TestNormalizeProviderConfigConvertsValuesToStrings(t *testing.T) {
	normalized, err := NormalizeProviderConfig(`{
		"base_url": "https://api.example.com",
		"api_key": "key-a,key-b",
		"enabled": true,
		"retries": 3,
		"extra": {"foo":"bar"}
	}`)
	if err != nil {
		t.Fatalf("NormalizeProviderConfig 返回错误: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal([]byte(normalized), &got); err != nil {
		t.Fatalf("规范化结果不是字符串键值 JSON: %v", err)
	}

	want := map[string]string{
		"base_url": "https://api.example.com",
		"api_key":  "key-a,key-b",
		"enabled":  "true",
		"retries":  "3",
		"extra":    `{"foo":"bar"}`,
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("字段 %s 异常: got=%q want=%q", key, got[key], value)
		}
	}
}

func TestNormalizeProviderConfigRejectsEmptyKey(t *testing.T) {
	if _, err := NormalizeProviderConfig(`{"  ":"value"}`); err == nil {
		t.Fatal("空键名应返回错误")
	}
}

func TestNormalizeProviderConfigRejectsDuplicateKeyAfterTrim(t *testing.T) {
	if _, err := NormalizeProviderConfig(`{"api_key":"key-a"," api_key ":"key-b"}`); err == nil {
		t.Fatal("trim 后重复键名应返回错误")
	}
}
