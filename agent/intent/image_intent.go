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

	score, reasons := classifyTextToImageIntent(normalized)
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

func classifyTextToImageIntent(normalized string) (int, []string) {
	score := 0
	reasons := make([]string, 0, 8)
	if isTextToImageMetaQuestion(normalized) {
		return 0, []string{"命中生图规则讨论排除"}
	}
	if isTextToImageSkillReference(normalized) {
		return 0, []string{"命中生图 Skill 引用排除"}
	}
	if reason, excluded := textToImageNonCreationReason(normalized); excluded {
		return 0, []string{reason}
	}
	if isWeatherQueryPrompt(normalized) {
		return 0, []string{"命中天气查询排除"}
	}
	for _, rule := range textToImageHardNegativeRules {
		if strings.Contains(normalized, rule.keyword) {
			reasons = append(reasons, rule.reason)
			return 0, reasons
		}
	}
	for _, rule := range textToImagePositiveRules {
		if strings.Contains(normalized, rule.keyword) {
			score += rule.score
			reasons = append(reasons, rule.reason)
		}
	}
	if scoreTextToImageOrderedPhrase(normalized) {
		score += 55
		reasons = append(reasons, "动作与视觉产物同句匹配")
	}
	if hasTextToImageCreationTone(normalized) {
		score += 25
		reasons = append(reasons, "命中创作请求语气")
	}
	if hasTextToImageVisualProduct(normalized) {
		score += 25
		reasons = append(reasons, "命中视觉产物类型")
	}
	if hasTextToImageStyleHint(normalized) {
		score += 15
		reasons = append(reasons, "命中图像风格线索")
	}
	if hasTextToImageSubjectHint(normalized) {
		score += 15
		reasons = append(reasons, "命中图像主体线索")
	}
	if hasTextToImageQuantityCreation(normalized) {
		score += 40
		reasons = append(reasons, "命中数量化创作请求")
	}
	for _, rule := range textToImageNegativeRules {
		if strings.Contains(normalized, rule.keyword) {
			score -= rule.score
			reasons = append(reasons, rule.reason)
		}
	}
	return score, reasons
}

type intentRule struct {
	keyword string
	score   int
	reason  string
}

var textToImagePositiveRules = []intentRule{
	{keyword: "生成图片", score: 90, reason: "命中生成图片"},
	{keyword: "生成图像", score: 90, reason: "命中生成图片"},
	{keyword: "生成照片", score: 90, reason: "命中生成图片"},
	{keyword: "生成壁纸", score: 90, reason: "命中生成图片"},
	{keyword: "生成头像", score: 90, reason: "命中生成图片"},
	{keyword: "生成海报", score: 90, reason: "命中生成图片"},
	{keyword: "生成插画", score: 90, reason: "命中生成图片"},
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
	{keyword: "画张", score: 85, reason: "命中绘图动作"},
	{keyword: "画幅", score: 80, reason: "命中绘图动作"},
	{keyword: "帮我画", score: 80, reason: "命中绘图动作"},
	{keyword: "给我画", score: 80, reason: "命中绘图动作"},
	{keyword: "绘制", score: 80, reason: "命中绘制动作"},
	{keyword: "帮我绘制", score: 85, reason: "命中绘制动作"},
	{keyword: "出一张图", score: 80, reason: "命中出图动作"},
	{keyword: "出张图", score: 85, reason: "命中出图动作"},
	{keyword: "出图", score: 85, reason: "命中出图动作"},
	{keyword: "做一张图", score: 80, reason: "命中制图动作"},
	{keyword: "做一张海报", score: 80, reason: "命中制图动作"},
	{keyword: "做个图", score: 80, reason: "命中制图动作"},
	{keyword: "做张图", score: 80, reason: "命中制图动作"},
	{keyword: "做个头像", score: 85, reason: "命中制图动作"},
	{keyword: "做个壁纸", score: 85, reason: "命中制图动作"},
	{keyword: "做个海报", score: 85, reason: "命中制图动作"},
	{keyword: "制作头像", score: 85, reason: "命中制图动作"},
	{keyword: "制作壁纸", score: 85, reason: "命中制图动作"},
	{keyword: "设计一张", score: 75, reason: "命中设计图片"},
	{keyword: "设计头像", score: 85, reason: "命中设计图片"},
	{keyword: "设计海报", score: 85, reason: "命中设计图片"},
	{keyword: "设计壁纸", score: 85, reason: "命中设计图片"},
	{keyword: "来一张", score: 75, reason: "命中出图动作"},
	{keyword: "来张", score: 75, reason: "命中出图动作"},
	{keyword: "来个", score: 65, reason: "命中出图动作"},
	{keyword: "整一张", score: 75, reason: "命中出图动作"},
	{keyword: "整张", score: 75, reason: "命中出图动作"},
	{keyword: "搞一张", score: 75, reason: "命中出图动作"},
	{keyword: "搞张", score: 75, reason: "命中出图动作"},
	{keyword: "想要一张", score: 65, reason: "命中创作请求"},
	{keyword: "要一张", score: 65, reason: "命中创作请求"},
	{keyword: "生图", score: 90, reason: "命中生图"},
	{keyword: "generateimage", score: 90, reason: "命中英文生成图片"},
	{keyword: "generateanimage", score: 90, reason: "命中英文生成图片"},
	{keyword: "generateapicture", score: 90, reason: "命中英文生成图片"},
	{keyword: "createanimage", score: 85, reason: "命中英文生成图片"},
	{keyword: "createapicture", score: 85, reason: "命中英文生成图片"},
	{keyword: "makeanimage", score: 85, reason: "命中英文生成图片"},
	{keyword: "makeapicture", score: 85, reason: "命中英文生成图片"},
	{keyword: "texttoimage", score: 95, reason: "命中英文文生图"},
	{keyword: "drawa", score: 75, reason: "命中英文绘图"},
	{keyword: "drawme", score: 75, reason: "命中英文绘图"},
	{keyword: "图片", score: 20, reason: "命中图片对象"},
	{keyword: "图像", score: 20, reason: "命中图片对象"},
	{keyword: "头像", score: 25, reason: "命中图片对象"},
	{keyword: "壁纸", score: 25, reason: "命中图片对象"},
	{keyword: "插画", score: 25, reason: "命中图片对象"},
	{keyword: "海报", score: 25, reason: "命中图片对象"},
}

var textToImageActionWords = []string{"生成", "画", "绘制", "制作", "创建", "做", "设计", "给我", "帮我", "想要"}

var textToImageObjectWords = []string{
	"图片", "图像", "照片", "插画", "海报", "头像", "壁纸", "封面",
	"表情包", "漫画", "立绘", "角色图", "场景图", "写真", "logo", "背景图",
}

var textToImageStyleHintWords = []string{
	"写实", "真实", "超现实", "摄影", "电影感", "胶片", "赛博朋克", "水彩",
	"油画", "国风", "二次元", "动漫", "卡通", "q版", "像素", "极简",
	"梦幻", "暗黑", "未来感", "高清", "4k", "插画风", "海报风",
}

var textToImageSubjectHintWords = []string{
	"猫", "狗", "小猫", "小狗", "人物", "角色", "女孩", "男孩", "美女",
	"机器人", "城市", "房间", "花园", "风景", "海边", "宇宙",
	"汽车", "机甲", "动物", "办公室", "头像", "表情包",
}

var textToImageQuantityWords = []string{"一张", "张", "一幅", "幅", "一个", "个", "一套", "套"}

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
	{keyword: "怎么画", score: 90, reason: "命中绘图教程排除"},
	{keyword: "如何画", score: 90, reason: "命中绘图教程排除"},
	{keyword: "怎样画", score: 90, reason: "命中绘图教程排除"},
	{keyword: "画法", score: 80, reason: "命中绘图教程排除"},
	{keyword: "图标", score: 180, reason: "命中界面资源排除"},
	{keyword: "图表", score: 160, reason: "命中图表排除"},
	{keyword: "流程图", score: 180, reason: "命中图表排除"},
	{keyword: "架构图", score: 180, reason: "命中图表排除"},
	{keyword: "时序图", score: 180, reason: "命中图表排除"},
	{keyword: "接口", score: 90, reason: "命中技术讨论排除"},
	{keyword: "加载慢", score: 75, reason: "命中问题排查排除"},
	{keyword: "打不开", score: 75, reason: "命中问题排查排除"},
	{keyword: "base64", score: 70, reason: "命中技术讨论排除"},
}

var textToImageHardNegativeRules = []intentRule{
	{keyword: "怎么画", score: 100, reason: "命中绘图教程排除"},
	{keyword: "如何画", score: 100, reason: "命中绘图教程排除"},
	{keyword: "怎样画", score: 100, reason: "命中绘图教程排除"},
	{keyword: "画法", score: 100, reason: "命中绘图教程排除"},
	{keyword: "图片生成接口", score: 100, reason: "命中技术讨论排除"},
	{keyword: "生成图片接口", score: 100, reason: "命中技术讨论排除"},
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

func isTextToImageMetaQuestion(text string) bool {
	if !containsAnyIntentWord(text, []string{"为什么", "为何", "怎么会", "怎么又", "误触发", "走生图", "触发生图", "生图规则"}) {
		return false
	}
	return containsAnyIntentWord(text, []string{"生成图片", "生成图像", "生图", "文生图", "图片"})
}

func isTextToImageSkillReference(text string) bool {
	if !containsAnyIntentWord(text, []string{"skill", "技能"}) {
		return false
	}
	return containsAnyIntentWord(text, []string{
		"图片生成", "图像生成", "生成图片", "生成图像", "生图", "文生图",
		"imagegen", "generateimage", "texttoimage", "txt2img",
	})
}

func textToImageNonCreationReason(text string) (string, bool) {
	if !hasTextToImageTopic(text) {
		return "", false
	}
	if containsAnyIntentWord(text, []string{
		"不要生成图片", "不要生成图像", "不用生成图片", "无需生成图片",
		"别生成图片", "别生成图像", "不要生图", "不用生图", "别生图",
		"停止生图", "取消生图", "关闭生图",
	}) {
		return "命中取消或否定生图请求", true
	}
	if containsAnyIntentWord(text, []string{
		"生图怎么用", "文生图怎么用", "图片生成怎么用", "图像生成怎么用",
		"如何使用生图", "如何使用文生图", "图片生成使用方法", "生图教程",
		"生图原理", "文生图原理", "图片生成原理", "图像生成原理",
		"介绍生图", "介绍文生图", "解释生图", "解释文生图",
		"生图区别", "文生图区别", "生图对比", "文生图对比",
		"优化生图", "优化图片生成", "优化图像生成", "修改生图", "检查生图",
		"测试生图", "调试生图", "修复生图", "配置生图",
		"生图规则", "文生图规则", "图片生成规则", "生成图片规则",
		"生图路由", "文生图路由", "图片生成路由", "图像生成路由",
		"生图误判", "生图误触发", "图片生成误判", "图片生成误触发",
		"生图失败", "文生图失败", "生成图片失败", "生成图像失败",
		"生图报错", "文生图报错", "生成图片报错", "生成图像报错",
		"生成图片翻译", "生成图像翻译",
	}) || (containsAnyIntentWord(text, []string{"解释", "介绍"}) &&
		!containsAnyIntentWord(text, []string{"不要解释", "无需解释", "不用解释", "别解释"})) ||
		(strings.Contains(text, "翻译") && hasQuotedTextToImageTopic(text)) {
		return "命中生图能力或技术讨论排除", true
	}
	return "", false
}

func hasQuotedTextToImageTopic(text string) bool {
	return containsAnyIntentWord(text, []string{
		"“生成图片”", "‘生成图片’", "\"生成图片\"",
		"“生成图像”", "‘生成图像’", "\"生成图像\"",
		"“生图”", "‘生图’", "\"生图\"",
	})
}

func hasTextToImageTopic(text string) bool {
	return containsAnyIntentWord(text, []string{
		"生成图片", "生成图像", "图片生成", "图像生成", "生图", "文生图",
		"imagegen", "generateimage", "texttoimage", "txt2img",
	})
}

func isWeatherQueryPrompt(text string) bool {
	if !strings.Contains(text, "天气") {
		return false
	}
	if hasExplicitTextToImageProductRequest(text) {
		return false
	}
	return containsAnyIntentWord(text, []string{
		"查询", "查找", "搜索", "获取", "推送", "今天", "当前日期", "当天",
		"天气情况", "天气结果", "天气数据", "气温", "降雨", "雨势", "风向",
		"风力", "空气质量", "生活指数", "出行建议",
	})
}

func hasExplicitTextToImageProductRequest(text string) bool {
	return containsAnyIntentWord(text, []string{
		"生成图片", "生成图像", "生成照片", "生成壁纸", "生成头像", "生成海报", "生成插画",
		"生成一张图片", "生成张图片", "文生图", "txt2img", "生图",
		"画一张图片", "画张图片", "出图", "出张图", "做张图",
	})
}

func hasTextToImageCreationTone(text string) bool {
	return containsAnyIntentWord(text, textToImageActionWords)
}

func hasTextToImageVisualProduct(text string) bool {
	return containsAnyIntentWord(text, textToImageObjectWords)
}

func hasTextToImageStyleHint(text string) bool {
	return containsAnyIntentWord(text, textToImageStyleHintWords)
}

func hasTextToImageSubjectHint(text string) bool {
	return containsAnyIntentWord(text, textToImageSubjectHintWords)
}

func hasTextToImageQuantityCreation(text string) bool {
	if !containsAnyIntentWord(text, textToImageActionWords) {
		return false
	}
	if !containsAnyIntentWord(text, textToImageQuantityWords) {
		return false
	}
	return hasTextToImageVisualProduct(text) ||
		hasTextToImageStyleHint(text) ||
		hasTextToImageSubjectHint(text)
}

func containsAnyIntentWord(text string, words []string) bool {
	for _, word := range words {
		if strings.Contains(text, word) {
			return true
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
