package intent

import "strings"

const (
	IntentChat        = "chat"
	IntentTextToImage = "txt2img"
)

type ImageIntentResult struct {
	Intent     string
	Confidence float64
	Reason     string
}

func DetectTextToImage(text string, hasAttachment bool) ImageIntentResult {
	normalized := normalizeIntentText(text)
	if normalized == "" || hasAttachment {
		return ImageIntentResult{Intent: IntentChat}
	}

	score := 0
	reasons := make([]string, 0, 4)
	for _, rule := range textToImagePositiveRules {
		if strings.Contains(normalized, rule.keyword) {
			score += rule.score
			reasons = append(reasons, rule.reason)
		}
	}
	if scoreTextToImageOrderedPhrase(normalized) {
		score += 75
		reasons = append(reasons, "命中生成图片组合表达")
	}
	for _, rule := range textToImageNegativeRules {
		if strings.Contains(normalized, rule.keyword) {
			score -= rule.score
			reasons = append(reasons, rule.reason)
		}
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	if score < 70 {
		return ImageIntentResult{
			Intent:     IntentChat,
			Confidence: float64(score) / 100,
			Reason:     strings.Join(uniqueReasons(reasons), "；"),
		}
	}
	return ImageIntentResult{
		Intent:     IntentTextToImage,
		Confidence: float64(score) / 100,
		Reason:     strings.Join(uniqueReasons(reasons), "；"),
	}
}

type intentRule struct {
	keyword string
	score   int
	reason  string
}

var textToImagePositiveRules = []intentRule{
	{keyword: "生成图片", score: 90, reason: "命中生成图片"},
	{keyword: "生成一张", score: 60, reason: "命中生成动作"},
	{keyword: "生成一张图", score: 90, reason: "命中生成图片"},
	{keyword: "生成一张图片", score: 90, reason: "命中生成图片"},
	{keyword: "生成个图片", score: 85, reason: "命中生成图片"},
	{keyword: "生成张图片", score: 85, reason: "命中生成图片"},
	{keyword: "文生图", score: 95, reason: "命中文生图"},
	{keyword: "txt2img", score: 95, reason: "命中文生图"},
	{keyword: "画一张", score: 85, reason: "命中绘图动作"},
	{keyword: "画一", score: 70, reason: "命中绘图动作"},
	{keyword: "画一个", score: 80, reason: "命中绘图动作"},
	{keyword: "画个", score: 80, reason: "命中绘图动作"},
	{keyword: "帮我画", score: 80, reason: "命中绘图动作"},
	{keyword: "给我画", score: 80, reason: "命中绘图动作"},
	{keyword: "绘制", score: 80, reason: "命中绘制动作"},
	{keyword: "出一张图", score: 80, reason: "命中出图动作"},
	{keyword: "做一张图", score: 80, reason: "命中制图动作"},
	{keyword: "做一张海报", score: 80, reason: "命中制图动作"},
	{keyword: "设计一张", score: 75, reason: "命中设计图片"},
	{keyword: "生图", score: 90, reason: "命中生图"},
	{keyword: "generateimage", score: 90, reason: "命中英文生成图片"},
	{keyword: "generateanimage", score: 90, reason: "命中英文生成图片"},
	{keyword: "createanimage", score: 85, reason: "命中英文生成图片"},
	{keyword: "texttoimage", score: 95, reason: "命中英文文生图"},
	{keyword: "drawa", score: 75, reason: "命中英文绘图"},
	{keyword: "图片", score: 20, reason: "命中图片对象"},
	{keyword: "图像", score: 20, reason: "命中图片对象"},
}

var textToImageActionWords = []string{"生成", "画", "绘制", "制作", "创建", "做", "设计"}

var textToImageObjectWords = []string{"图片", "图像", "照片", "插画", "海报"}

var textToImageNegativeRules = []intentRule{
	{keyword: "搜索图片", score: 90, reason: "命中搜图排除"},
	{keyword: "查找图片", score: 90, reason: "命中搜图排除"},
	{keyword: "找一张", score: 90, reason: "命中搜图排除"},
	{keyword: "找张", score: 85, reason: "命中搜图排除"},
	{keyword: "下载图片", score: 90, reason: "命中下载图片排除"},
	{keyword: "识别图片", score: 90, reason: "命中识图排除"},
	{keyword: "分析图片", score: 90, reason: "命中看图排除"},
	{keyword: "这张图", score: 80, reason: "命中看图排除"},
	{keyword: "这张图片", score: 80, reason: "命中看图排除"},
	{keyword: "图片是什么", score: 90, reason: "命中看图排除"},
	{keyword: "生成视频", score: 100, reason: "命中视频排除"},
	{keyword: "生视频", score: 100, reason: "命中视频排除"},
	{keyword: "视频", score: 80, reason: "命中视频排除"},
	{keyword: "图标", score: 80, reason: "命中界面资源排除"},
	{keyword: "加载慢", score: 75, reason: "命中问题排查排除"},
	{keyword: "打不开", score: 75, reason: "命中问题排查排除"},
	{keyword: "base64", score: 70, reason: "命中技术讨论排除"},
}

func scoreTextToImageOrderedPhrase(text string) bool {
	for _, action := range textToImageActionWords {
		actionIndex := strings.Index(text, action)
		if actionIndex < 0 {
			continue
		}
		afterAction := text[actionIndex+len(action):]
		for _, object := range textToImageObjectWords {
			if strings.Contains(afterAction, object) {
				return true
			}
		}
	}
	return false
}

func normalizeIntentText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\r", "",
		"\n", "",
		"　", "",
		"，", "",
		"。", "",
		"！", "",
		"?", "",
		"？", "",
		",", "",
		".", "",
		"!", "",
		"-", "",
		"_", "",
	)
	return replacer.Replace(text)
}

func uniqueReasons(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
