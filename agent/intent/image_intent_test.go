package intent

import "testing"

func TestDetectTextToImagePositive(t *testing.T) {
	cases := []string{
		"生成一张小猫图片",
		"生成一只大肚皮翻滚的花猫的图片",
		"帮我画一个赛博朋克城市",
		"做一张海报，主题是夏天",
		"txt2img: a cute cat",
	}
	for _, tc := range cases {
		result := DetectTextToImage(tc, false)
		if result.Intent != IntentTextToImage || result.Confidence < 0.7 {
			t.Fatalf("期望识别为文生图，input=%q result=%+v", tc, result)
		}
	}
}

func TestDetectTextToImageNegative(t *testing.T) {
	cases := []string{
		"帮我找一张猫图",
		"搜索图片生成接口怎么用",
		"识别这张图片里面有什么",
		"生成视频给我",
		"图标加载慢怎么修复",
	}
	for _, tc := range cases {
		result := DetectTextToImage(tc, false)
		if result.Intent != IntentChat {
			t.Fatalf("期望识别为普通对话，input=%q result=%+v", tc, result)
		}
	}
}

func TestDetectTextToImageIgnoresImageAttachment(t *testing.T) {
	result := DetectTextToImage("画一张同款图片", true)
	if result.Intent != IntentChat {
		t.Fatalf("带附件时不应进入文生图，result=%+v", result)
	}
}
