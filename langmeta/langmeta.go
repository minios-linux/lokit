// Package langmeta provides a shared language metadata registry
// (native names and emoji flags) used across output formats and CLI UI.
package langmeta

import "strings"

// Meta describes language display metadata.
type Meta struct {
	Name string
	Flag string
}

// Registry contains canonical language metadata.
// Locale variants are resolved in Resolve() via normalization and base fallback.
var Registry = map[string]Meta{
	"af":    {Name: "Afrikaans", Flag: "🇿🇦"},
	"am":    {Name: "አማርኛ", Flag: "🇪🇹"},
	"ar":    {Name: "العربية", Flag: "🇸🇦"},
	"ar-EG": {Name: "العربية (مصر)", Flag: "🇪🇬"},
	"az":    {Name: "Azərbaycanca", Flag: "🇦🇿"},
	"be":    {Name: "Беларуская", Flag: "🇧🇾"},
	"bg":    {Name: "Български", Flag: "🇧🇬"},
	"bn":    {Name: "বাংলা", Flag: "🇧🇩"},
	"bs":    {Name: "Bosanski", Flag: "🇧🇦"},
	"ca":    {Name: "Català", Flag: "🇪🇸"},
	"cs":    {Name: "Čeština", Flag: "🇨🇿"},
	"cy":    {Name: "Cymraeg", Flag: "🇬🇧"},
	"da":    {Name: "Dansk", Flag: "🇩🇰"},
	"de":    {Name: "Deutsch", Flag: "🇩🇪"},
	"de-AT": {Name: "Deutsch (Österreich)", Flag: "🇦🇹"},
	"de-CH": {Name: "Deutsch (Schweiz)", Flag: "🇨🇭"},
	"el":    {Name: "Ελληνικά", Flag: "🇬🇷"},
	"en":    {Name: "English", Flag: "🇺🇸"},
	"en-AU": {Name: "English (Australia)", Flag: "🇦🇺"},
	"en-CA": {Name: "English (Canada)", Flag: "🇨🇦"},
	"en-GB": {Name: "English (UK)", Flag: "🇬🇧"},
	"en-IN": {Name: "English (India)", Flag: "🇮🇳"},
	"en-US": {Name: "English (US)", Flag: "🇺🇸"},
	"es":    {Name: "Español", Flag: "🇪🇸"},
	"es-AR": {Name: "Español (Argentina)", Flag: "🇦🇷"},
	"es-MX": {Name: "Español (México)", Flag: "🇲🇽"},
	"et":    {Name: "Eesti", Flag: "🇪🇪"},
	"eu":    {Name: "Euskara", Flag: "🇪🇸"},
	"fa":    {Name: "فارسی", Flag: "🇮🇷"},
	"fi":    {Name: "Suomi", Flag: "🇫🇮"},
	"fr":    {Name: "Français", Flag: "🇫🇷"},
	"fr-BE": {Name: "Français (Belgique)", Flag: "🇧🇪"},
	"fr-CA": {Name: "Français (Canada)", Flag: "🇨🇦"},
	"fr-CH": {Name: "Français (Suisse)", Flag: "🇨🇭"},
	"ga":    {Name: "Gaeilge", Flag: "🇮🇪"},
	"gl":    {Name: "Galego", Flag: "🇪🇸"},
	"gu":    {Name: "ગુજરાતી", Flag: "🇮🇳"},
	"he":    {Name: "עברית", Flag: "🇮🇱"},
	"hi":    {Name: "हिन्दी", Flag: "🇮🇳"},
	"hr":    {Name: "Hrvatski", Flag: "🇭🇷"},
	"hu":    {Name: "Magyar", Flag: "🇭🇺"},
	"hy":    {Name: "Հայերեն", Flag: "🇦🇲"},
	"id":    {Name: "Bahasa Indonesia", Flag: "🇮🇩"},
	"is":    {Name: "Íslenska", Flag: "🇮🇸"},
	"it":    {Name: "Italiano", Flag: "🇮🇹"},
	"ja":    {Name: "日本語", Flag: "🇯🇵"},
	"ka":    {Name: "ქართული", Flag: "🇬🇪"},
	"kk":    {Name: "Қазақ тілі", Flag: "🇰🇿"},
	"km":    {Name: "ខ្មែរ", Flag: "🇰🇭"},
	"ko":    {Name: "한국어", Flag: "🇰🇷"},
	"lo":    {Name: "ລາວ", Flag: "🇱🇦"},
	"lt":    {Name: "Lietuvių", Flag: "🇱🇹"},
	"lv":    {Name: "Latviešu", Flag: "🇱🇻"},
	"mk":    {Name: "Македонски", Flag: "🇲🇰"},
	"ml":    {Name: "മലയാളം", Flag: "🇮🇳"},
	"mn":    {Name: "Монгол", Flag: "🇲🇳"},
	"mr":    {Name: "मराठी", Flag: "🇮🇳"},
	"ms":    {Name: "Bahasa Melayu", Flag: "🇲🇾"},
	"mt":    {Name: "Malti", Flag: "🇲🇹"},
	"my":    {Name: "မြန်မာ", Flag: "🇲🇲"},
	"ne":    {Name: "नेपाली", Flag: "🇳🇵"},
	"nl":    {Name: "Nederlands", Flag: "🇳🇱"},
	"nl-BE": {Name: "Nederlands (België)", Flag: "🇧🇪"},
	"nb":    {Name: "Norsk bokmål", Flag: "🇳🇴"},
	"nn":    {Name: "Norsk nynorsk", Flag: "🇳🇴"},
	"no":    {Name: "Norsk", Flag: "🇳🇴"},
	"pa":    {Name: "ਪੰਜਾਬੀ", Flag: "🇮🇳"},
	"pl":    {Name: "Polski", Flag: "🇵🇱"},
	"ps":    {Name: "پښتو", Flag: "🇦🇫"},
	"pt":    {Name: "Português", Flag: "🇵🇹"},
	"pt-BR": {Name: "Português (Brasil)", Flag: "🇧🇷"},
	"pt-PT": {Name: "Português (Portugal)", Flag: "🇵🇹"},
	"ro":    {Name: "Română", Flag: "🇷🇴"},
	"ru":    {Name: "Русский", Flag: "🇷🇺"},
	"si":    {Name: "සිංහල", Flag: "🇱🇰"},
	"sk":    {Name: "Slovenčina", Flag: "🇸🇰"},
	"sl":    {Name: "Slovenščina", Flag: "🇸🇮"},
	"sq":    {Name: "Shqip", Flag: "🇦🇱"},
	"sr":    {Name: "Српски", Flag: "🇷🇸"},
	"sv":    {Name: "Svenska", Flag: "🇸🇪"},
	"sw":    {Name: "Kiswahili", Flag: "🇹🇿"},
	"ta":    {Name: "தமிழ்", Flag: "🇮🇳"},
	"te":    {Name: "తెలుగు", Flag: "🇮🇳"},
	"th":    {Name: "ไทย", Flag: "🇹🇭"},
	"tr":    {Name: "Türkçe", Flag: "🇹🇷"},
	"uk":    {Name: "Українська", Flag: "🇺🇦"},
	"ur":    {Name: "اردو", Flag: "🇵🇰"},
	"uz":    {Name: "O'zbek", Flag: "🇺🇿"},
	"vi":    {Name: "Tiếng Việt", Flag: "🇻🇳"},
	"xh":    {Name: "isiXhosa", Flag: "🇿🇦"},
	"yo":    {Name: "Yorùbá", Flag: "🇳🇬"},
	"zh":    {Name: "中文", Flag: "🇨🇳"},
	"zh-CN": {Name: "简体中文", Flag: "🇨🇳"},
	"zh-TW": {Name: "繁體中文", Flag: "🇹🇼"},
	"zu":    {Name: "isiZulu", Flag: "🇿🇦"},
}

func canonicalize(lang string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(lang), "_", "-")
	if normalized == "" {
		return ""
	}
	parts := strings.Split(normalized, "-")
	parts[0] = strings.ToLower(parts[0])
	if len(parts) >= 2 {
		parts[1] = strings.ToUpper(parts[1])
	}
	return strings.Join(parts, "-")
}

// Resolve returns best-effort language metadata for language codes,
// supporting variants like pt_BR, pt-BR, and locale fallbacks.
func Resolve(lang string) Meta {
	if m, ok := Registry[lang]; ok {
		return m
	}
	normalized := canonicalize(lang)
	if m, ok := Registry[normalized]; ok {
		return m
	}
	if parts := strings.SplitN(normalized, "-", 2); len(parts) == 2 {
		if m, ok := Registry[parts[0]]; ok {
			return m
		}
	}
	return Meta{Name: lang, Flag: ""}
}
