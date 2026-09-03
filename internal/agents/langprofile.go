package agents

import "strings"

// langprofile.go 对应 trans_novel/agents/langprofile.py（主规格 §9.2 + 分册 05 §3）。

var langLabels = map[string]string{
	"ja": "日文", "en": "英文", "zh": "中文", "ru": "俄文", "ko": "韩文",
	"fr": "法文", "de": "德文", "es": "西班牙文", "it": "意大利文", "pt": "葡萄牙文",
}

// LangLabel 对应 langprofile.label：未知代码 → "{src}文"；空串 → "原文"。
func LangLabel(src string) string {
	if v, ok := langLabels[src]; ok {
		return v
	}
	if src == "" {
		return "原文"
	}
	return src + "文"
}

// HonorificRule 对应 langprofile.honorific_rule。
func HonorificRule(strategy string) string {
	switch strategy {
	case "keep_style":
		return "体现敬称所含的人物关系与语气（如 先輩→前辈、ちゃん→小X）；根据具体人物关系确定“君”等称呼的译法，确定后同一关系全书沿用。"
	case "normalize":
		return "按统一规则处理敬称，避免同一敬称多种译法。"
	case "drop":
		return "在不影响语义和人物关系的前提下省略敬称。"
	default:
		return "体现敬称语气并保持全书统一。"
	}
}

// TranslateGuidance 对应 langprofile.translate_guidance（ja/en 各 4 条要点；其它 1 条）。
func TranslateGuidance(src, honorificStrategy string) string {
	if src == "ja" {
		return strings.Join([]string{
			"- 敬称：" + HonorificRule(honorificStrategy),
			"- 依据【角色信息】与第一人称（私/僕/俺/あたし 等）体现的语域，正确选择“他/她”等代词与说话口吻。",
			"- 拟声拟态词按中文小说习惯自然表达，不生硬直译。",
			"- 汉字词不等于中文词，按语义译，勿照搬日文汉字写法。",
		}, "\n")
	}
	if src == "en" {
		return strings.Join([]string{
			"- 英文无敬称体系；Mr./Ms./Sir 等称谓按中文习惯自然处理，全书统一。",
			"- 依据人名性别与上下文正确选择“他/她/它”；英文不显性别处须联系上下文判断。",
			"- 时态、关系从句、长句按中文表达重组断句；被动语态酌情转主动，避免翻译腔。",
			"- 英文专有名词按通行译名规范音译/意译，并沿用对照表，全书统一。",
		}, "\n")
	}
	return "- 忠实传达原意，符合中文小说表达习惯。"
}

// TermGuidance 对应 langprofile.term_guidance。
func TermGuidance(src string) string {
	switch src {
	case "ja":
		return "reading 填假名读音（用于音译消歧）；人物依语气/第一人称判断性别。"
	case "en":
		return "reading 可留空（英文无需读音）；人物依姓名常识与上下文判断性别。"
	default:
		return "reading 可留空；人物依上下文判断性别。"
	}
}
