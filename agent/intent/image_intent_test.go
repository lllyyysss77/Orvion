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
		"生成一张佛山暴雨天气海报",
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
		"图片生成skills怎么用",
		"帮我优化一下图片生成 skill",
		"请检查生图技能的规则",
		"使用 text-to-image skill 生成一张猫图",
		"不要生成图片，只回答问题",
		"先别生图，帮我分析一下需求",
		"生图失败怎么排查",
		"解释一下文生图的工作原理",
		"优化图片生成的路由规则",
		"把“生成图片”翻译成英文",
		"给我一张数据表",
		"这个 logo 图标加载慢怎么修复",
		"先获取当前日期（以系统当前日期为准），明确今天是几月几号；然后查询佛山当天的天气情况。天气结果必须与当前日期一致，若搜索结果或天气页面显示的日期不是今天，需继续查找或说明无法确认，不能把前一天/旧日期天气当作今日天气。请详细推送佛山今天的天气，包括天气现象、气温范围、降雨概率/雨势、风向风力、空气质量（如可获取）、生活指数与出行建议，并在结果开头标明“当前日期”和“天气数据日期/更新时间”。",
		"为什么这个提示词：先获取当前日期，然后查询佛山当天的天气情况，会生成图片",
	}
	for _, tc := range cases {
		result := DetectTextToImage(tc, false)
		if result.Intent != IntentChat {
			t.Fatalf("期望识别为普通对话，input=%q result=%+v", tc, result)
		}
	}
}

func TestDetectTextToImageDiscussionWordsDoNotBlockExplicitCreation(t *testing.T) {
	cases := []string{
		"不要解释，直接生成图片：一只橘猫",
		"生成一张图片，画面里不要出现文字",
		"画一张测试实验室的插画",
	}
	for _, tc := range cases {
		result := DetectTextToImage(tc, false)
		if result.Intent != IntentTextToImage {
			t.Fatalf("明确创作请求不应被讨论词误伤，input=%q result=%+v", tc, result)
		}
	}
}

func TestDetectTextToImageIgnoresImageAttachment(t *testing.T) {
	result := DetectTextToImage("画一张同款图片", true)
	if result.Intent != IntentChat {
		t.Fatalf("带附件时不应进入文生图，result=%+v", result)
	}
}
