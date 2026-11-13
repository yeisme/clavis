package engine

import (
	"sort"
	"strings"
	"unicode/utf8"
)

type emojiDef struct {
	Symbol  string
	Name    string
	Aliases []string
}

var curatedEmojis = []emojiDef{
	{Symbol: "🚀", Name: "Rocket", Aliases: []string{"rocket", "launch", "ship", "deploy", "火箭", "发布", "上线"}},
	{Symbol: "✅", Name: "Check Mark", Aliases: []string{"check", "done", "complete", "yes", "ok", "完成", "对勾", "通过"}},
	{Symbol: "❌", Name: "Cross Mark", Aliases: []string{"cross", "error", "fail", "no", "cancel", "错误", "失败", "取消"}},
	{Symbol: "✨", Name: "Sparkles", Aliases: []string{"sparkles", "magic", "ai", "new", "feature", "闪亮", "新功能", "星星"}},
	{Symbol: "🔥", Name: "Fire", Aliases: []string{"fire", "hot", "lit", "trending", "火", "热门", "燃"}},
	{Symbol: "🐛", Name: "Bug", Aliases: []string{"bug", "fix", "issue", "defect", "漏洞", "缺陷", "修复"}},
	{Symbol: "👍", Name: "Thumbs Up", Aliases: []string{"thumbsup", "+1", "like", "approve", "good", "点赞", "赞", "同意"}},
	{Symbol: "👎", Name: "Thumbs Down", Aliases: []string{"thumbsdown", "-1", "dislike", "踩", "不同意"}},
	{Symbol: "🎉", Name: "Party Popper", Aliases: []string{"tada", "party", "celebrate", "congrats", "庆祝", "礼花", "恭喜"}},
	{Symbol: "❤️", Name: "Red Heart", Aliases: []string{"heart", "love", "爱心", "心", "喜欢"}},
	{Symbol: "⚠️", Name: "Warning", Aliases: []string{"warning", "alert", "caution", "警告", "注意"}},
	{Symbol: "💡", Name: "Light Bulb", Aliases: []string{"bulb", "idea", "tip", "hint", "灵感", "灯泡", "提示"}},
	{Symbol: "📝", Name: "Memo", Aliases: []string{"memo", "note", "write", "doc", "draft", "笔记", "备忘", "文档"}},
	{Symbol: "📌", Name: "Pushpin", Aliases: []string{"pin", "pushpin", "sticky", "置顶", "固定", "图钉"}},
	{Symbol: "📎", Name: "Paperclip", Aliases: []string{"paperclip", "attach", "attachment", "附件", "回形针"}},
	{Symbol: "📅", Name: "Calendar", Aliases: []string{"calendar", "date", "schedule", "日历", "日程", "日期"}},
	{Symbol: "⏰", Name: "Alarm Clock", Aliases: []string{"alarm", "clock", "time", "reminder", "闹钟", "时间", "提醒"}},
	{Symbol: "⏳", Name: "Hourglass", Aliases: []string{"hourglass", "wait", "pending", "loading", "沙漏", "等待", "进行中"}},
	{Symbol: "🔒", Name: "Locked", Aliases: []string{"lock", "secure", "privacy", "secret", "锁", "安全", "加密"}},
	{Symbol: "🔓", Name: "Unlocked", Aliases: []string{"unlock", "open", "解锁", "公开"}},
	{Symbol: "🔑", Name: "Key", Aliases: []string{"key", "token", "auth", "password", "钥匙", "密钥", "认证"}},
	{Symbol: "🛡️", Name: "Shield", Aliases: []string{"shield", "security", "protect", "guard", "盾牌", "防护"}},
	{Symbol: "🔍", Name: "Magnifying Glass", Aliases: []string{"search", "find", "inspect", "zoom", "搜索", "查找", "放大镜"}},
	{Symbol: "⚙️", Name: "Gear", Aliases: []string{"gear", "settings", "config", "preferences", "设置", "配置", "齿轮"}},
	{Symbol: "🔧", Name: "Wrench", Aliases: []string{"wrench", "tool", "patch", "扳手", "工具"}},
	{Symbol: "🔨", Name: "Hammer", Aliases: []string{"hammer", "build", "construct", "锤子", "构建"}},
	{Symbol: "📦", Name: "Package", Aliases: []string{"package", "box", "bundle", "release", "包裹", "打包", "模块"}},
	{Symbol: "📁", Name: "Folder", Aliases: []string{"folder", "directory", "dir", "文件夹", "目录"}},
	{Symbol: "🔗", Name: "Link", Aliases: []string{"link", "url", "chain", "链接", "网址"}},
	{Symbol: "🌐", Name: "Globe", Aliases: []string{"globe", "web", "world", "i18n", "全球", "网络", "国际化"}},
	{Symbol: "💻", Name: "Laptop", Aliases: []string{"laptop", "computer", "code", "dev", "电脑", "笔记本", "编程"}},
	{Symbol: "🖥️", Name: "Desktop Computer", Aliases: []string{"desktop", "monitor", "screen", "显示器", "台式机"}},
	{Symbol: "⌨️", Name: "Keyboard", Aliases: []string{"keyboard", "type", "shortcut", "hotkey", "键盘", "快捷键"}},
	{Symbol: "⚡", Name: "High Voltage", Aliases: []string{"zap", "lightning", "fast", "perf", "bolt", "闪电", "极速", "性能"}},
	{Symbol: "♻️", Name: "Recycling Symbol", Aliases: []string{"recycle", "refactor", "clean", "重构", "循环"}},
	{Symbol: "🎨", Name: "Artist Palette", Aliases: []string{"art", "palette", "design", "style", "ui", "设计", "调色板", "样式"}},
	{Symbol: "📈", Name: "Chart Increasing", Aliases: []string{"chart", "growth", "metrics", "up", "趋势", "增长", "图表"}},
	{Symbol: "📊", Name: "Bar Chart", Aliases: []string{"barchart", "stats", "analytics", "report", "统计", "报表", "数据"}},
	{Symbol: "🎯", Name: "Bullseye", Aliases: []string{"target", "goal", "focus", "accurate", "目标", "靶心", "精准"}},
	{Symbol: "🏆", Name: "Trophy", Aliases: []string{"trophy", "award", "win", "champion", "奖杯", "冠军", "获奖"}},
	{Symbol: "⭐", Name: "Star", Aliases: []string{"star", "favorite", "bookmark", "星标", "收藏"}},
	{Symbol: "🔔", Name: "Bell", Aliases: []string{"bell", "notification", "notify", "铃铛", "通知"}},
	{Symbol: "📣", Name: "Megaphone", Aliases: []string{"megaphone", "announce", "broadcast", "喇叭", "公告"}},
	{Symbol: "💬", Name: "Speech Balloon", Aliases: []string{"chat", "comment", "message", "discuss", "聊天", "评论", "消息"}},
	{Symbol: "🤔", Name: "Thinking Face", Aliases: []string{"thinking", "hmm", "ponder", "question", "思考", "想"}},
	{Symbol: "👀", Name: "Eyes", Aliases: []string{"eyes", "look", "review", "watch", "眼睛", "看", "关注"}},
	{Symbol: "🙌", Name: "Raising Hands", Aliases: []string{"hooray", "praise", "hands", "欢呼", "举手"}},
	{Symbol: "👏", Name: "Clapping Hands", Aliases: []string{"clap", "applause", "bravo", "鼓掌", "拍手"}},
	{Symbol: "🙏", Name: "Folded Hands", Aliases: []string{"pray", "thanks", "please", "thankyou", "感谢", "谢谢", "拜托"}},
	{Symbol: "👋", Name: "Waving Hand", Aliases: []string{"wave", "hello", "hi", "bye", "挥手", "你好", "再见"}},
	{Symbol: "💪", Name: "Flexed Biceps", Aliases: []string{"muscle", "strong", "power", "fighting", "肌肉", "加油", "力量"}},
	{Symbol: "👌", Name: "OK Hand", Aliases: []string{"ok_hand", "okay", "fine", "没问题", "好的"}},
	{Symbol: "☕", Name: "Hot Beverage", Aliases: []string{"coffee", "tea", "break", "cafe", "咖啡", "茶", "休息"}},
	{Symbol: "🧠", Name: "Brain", Aliases: []string{"brain", "smart", "memory", "mind", "大脑", "智能", "思考"}},
	{Symbol: "🤖", Name: "Robot", Aliases: []string{"robot", "bot", "agent", "auto", "机器人", "智能体"}},
	{Symbol: "🔬", Name: "Microscope", Aliases: []string{"microscope", "test", "science", "analyze", "显微镜", "测试", "分析"}},
	{Symbol: "🧹", Name: "Broom", Aliases: []string{"broom", "sweep", "cleanup", "扫帚", "清理"}},
	{Symbol: "🚩", Name: "Triangular Flag", Aliases: []string{"flag", "milestone", "mark", "旗帜", "标记", "里程碑"}},
	{Symbol: "🧭", Name: "Compass", Aliases: []string{"compass", "navigation", "guide", "指南针", "导航"}},
	{Symbol: "💎", Name: "Gem Stone", Aliases: []string{"gem", "diamond", "premium", "宝石", "钻石"}},
	{Symbol: "→", Name: "Right Arrow", Aliases: []string{"arrow_right", "rightarrow", "next", "右箭头", "箭头"}},
	{Symbol: "←", Name: "Left Arrow", Aliases: []string{"arrow_left", "leftarrow", "prev", "左箭头"}},
	{Symbol: "⌘", Name: "Command Symbol", Aliases: []string{"cmd", "command_key", "mac", "命令键"}},
	{Symbol: "⌥", Name: "Option Symbol", Aliases: []string{"opt", "option_key", "alt_key", "选项键"}},
}

// SearchEmojis searches curated emojis and productivity symbols by English or Chinese aliases.
// Supports ":query", "emoji query", or direct keyword queries.
func SearchEmojis(query string) []OmniboxItem {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil
	}

	explicitTrigger := false
	clean := trimmed
	if strings.HasPrefix(clean, "：") {
		clean = ":" + strings.TrimPrefix(clean, "：")
	}
	if strings.HasSuffix(clean, "：") {
		clean = strings.TrimSuffix(clean, "：") + ":"
	}
	lower := strings.ToLower(clean)

	if strings.HasPrefix(clean, ":") {
		explicitTrigger = true
		clean = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(clean, ":"), ":"))
	} else if lower == "emoji" || strings.HasPrefix(lower, "emoji ") {
		explicitTrigger = true
		clean = strings.TrimSpace(clean[5:])
	}

	if explicitTrigger && clean == "" {
		limit := 16
		if len(curatedEmojis) < limit {
			limit = len(curatedEmojis)
		}
		out := make([]OmniboxItem, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, makeEmojiOmniboxItem(curatedEmojis[i], 900-i))
		}
		return out
	}

	if clean == "" {
		return nil
	}

	// Avoid matching multi-word freeform sentences unless explicitly triggered with ':' or 'emoji '.
	if !explicitTrigger && strings.ContainsRune(clean, ' ') {
		return nil
	}

	cleanLower := strings.ToLower(clean)
	hasNonASCII := false
	for _, r := range clean {
		if r > 127 {
			hasNonASCII = true
			break
		}
	}
	runeCount := utf8.RuneCountInString(clean)
	if !explicitTrigger && !hasNonASCII && runeCount < 2 {
		return nil
	}

	type scoredEmoji struct {
		idx   int
		score int
		def   emojiDef
	}
	var matches []scoredEmoji

	for idx, def := range curatedEmojis {
		best := 0
		nameLower := strings.ToLower(def.Name)
		if nameLower == cleanLower {
			best = 960
		} else if strings.HasPrefix(nameLower, cleanLower) && (explicitTrigger || hasNonASCII || runeCount >= 3) {
			best = 860
		}

		for _, alias := range def.Aliases {
			aLower := strings.ToLower(alias)
			if aLower == cleanLower {
				if 980 > best {
					best = 980
				}
			} else if strings.HasPrefix(aLower, cleanLower) && (explicitTrigger || hasNonASCII || runeCount >= 3) {
				if 890 > best {
					best = 890
				}
			} else if (explicitTrigger || hasNonASCII) && strings.Contains(aLower, cleanLower) {
				if 800 > best {
					best = 800
				}
			} else if (explicitTrigger || runeCount >= 3) && !hasNonASCII && ContainsHanzi(alias) {
				if pyPts := MatchPinyinKeyword(cleanLower, alias); pyPts > 0 {
					cand := 760 + pyPts
					if cand > best {
						best = cand
					}
				}
			}
		}

		if best > 0 {
			matches = append(matches, scoredEmoji{idx: idx, score: best, def: def})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].idx < matches[j].idx
	})

	out := make([]OmniboxItem, len(matches))
	for i, m := range matches {
		out[i] = makeEmojiOmniboxItem(m.def, m.score)
	}
	return out
}

func makeEmojiOmniboxItem(def emojiDef, score int) OmniboxItem {
	aliasPreview := strings.Join(def.Aliases, ", ")
	return OmniboxItem{
		ID:           "emoji:" + def.Aliases[0],
		Title:        def.Symbol + "  " + def.Name,
		Subtitle:     "Emoji • " + aliasPreview,
		KeyHint:      ":" + def.Aliases[0] + ":",
		Icon:         "face.smiling",
		Keywords:     def.Aliases,
		Score:        score,
		Kind:         ItemEmoji,
		UtilityValue: def.Symbol,
	}
}
