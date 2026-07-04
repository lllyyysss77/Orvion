package intent

import "testing"

func TestDetectTextToImagePositive(t *testing.T) {
	cases := []string{
		"生成一张小猫图片",
		"生成一只大肚皮翻滚的花猫的图片",
		"帮我画一个赛博朋克城市",
		"做一张海报，主题是夏天",
		"来张小猫头像",
		"帮我搞一张雨夜城市壁纸",
		"出图，主题是未来办公室",
		"做个表情包，小狗戴墨镜",
		"设计一个科幻风封面",
		"画张水彩风花园",
		"我想要一张未来感城市壁纸",
		"能不能来个Q版机器人头像",
		"给我一张海边落日电影感照片",
		"整一张猫猫戴墨镜的表情包",
		"make a picture of a cute cat",
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
		"怎么画一张透视准确的城市图",
		"帮我画一个接口调用流程图",
		"图片生成接口怎么用",
		"给我一张数据表",
		"这个 logo 图标加载慢怎么修复",
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
