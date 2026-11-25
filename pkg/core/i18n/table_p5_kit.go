package i18n

func init() {
	registerTable(map[string][2]string{
		"settings.configs.desc.identities": {"AI personas, environment profiles, and device sync identity", "AI 角色身份、环境配置组与多设备同步身份"},

		"common.back": {"Back", "返回"},

		"kit.filter.all":       {"All", "全部"},
		"kit.filter.favorites": {"Favorites", "收藏"},
		"kit.filter.clear":     {"Clear", "清除"},

		"kit.tag.remove":          {"Remove tag %s", "移除标签 %s"},
		"kit.tag.ns.env":          {"Env", "环境"},
		"kit.tag.ns.proj":         {"Proj", "项目"},
		"kit.tag.ns.type":         {"Type", "类型"},
		"kit.tag.add_placeholder": {"+ Tag", "+ 标签"},
		"kit.tag.add":             {"Add", "添加"},
		"kit.tag.suggestions":     {"Suggestions", "常用"},
		"kit.tag.limit":           {"Max %d tags", "最多 %d 个标签"},
		"kit.tag.invalid":         {"Invalid or unsafe tag", "标签无效或包含敏感词"},

		"kit.favorite.add":    {"Add to favorites", "加入收藏"},
		"kit.favorite.remove": {"Remove from favorites", "取消收藏"},

		"kit.prop.copied": {"Copied", "已复制"},
	})
}
