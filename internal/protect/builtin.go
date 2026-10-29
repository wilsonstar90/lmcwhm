package protect

// Built-in dedicated rules (as aaPanel's 專屬規則), WordPress for now. The
// default switch follows aaPanel; editing one can be undone with Reset.

func c(field, op string, values ...string) Cond { return Cond{Field: field, Op: op, Values: values} }

var builtins = []Rule{
	{Code: "wp-1001", CMS: "wordpress", Name: "Block /wp-json/wp/v2/users (user enumeration)", Enabled: true,
		Description: "Stops outsiders reading the administrators' user names through the REST API.",
		Conds:       []Cond{c("path", "prefix", "/wp-json/wp/v2/users"), c("query", "contains", "rest_route=/wp/v2/users", "rest_route=%2Fwp%2Fv2%2Fusers")}},
	{Code: "wp-1002", CMS: "wordpress", Name: "Block xmlrpc.php", Enabled: true, All: true,
		Description: "XML-RPC is used for password guessing and pingback floods; keep it on unless an app (e.g. Jetpack) needs it.",
		Conds:       []Cond{c("path", "suffix", "/xmlrpc.php")}},
	{Code: "wp-1003", CMS: "wordpress", Name: "Block ?author= user enumeration", Enabled: true, All: true,
		Description: "Stops outsiders finding user names from /?author=1 redirects (the admin pages are not affected).",
		Conds:       []Cond{c("query", "regex", "(^|&)author=[0-9]+"), {Field: "path", Op: "prefix", Negate: true, Values: []string{"/wp-admin/"}}}},
	{Code: "wp-1004", CMS: "wordpress", Name: "Block the plugin file editor", All: true,
		Description: "Recommended: stops a stolen admin login from planting code through plugin files.",
		Conds:       []Cond{c("path", "suffix", "/wp-admin/plugin-editor.php")}},
	{Code: "wp-1005", CMS: "wordpress", Name: "Block the theme file editor", All: true,
		Description: "Recommended: stops a stolen admin login from planting code through theme files.",
		Conds:       []Cond{c("path", "suffix", "/wp-admin/theme-editor.php")}},
	{Code: "wp-1006", CMS: "wordpress", Name: "Block uploading plugins and themes", All: true,
		Description: "Recommended: stops a stolen admin login from installing a backdoored plugin or theme as a zip.",
		Conds:       []Cond{c("path", "suffix", "/wp-admin/update.php"), c("query", "contains", "action=upload-plugin", "action=upload-theme")}},
	{Code: "wp-1007", CMS: "wordpress", Name: "Block PHP in wp-content/uploads", Enabled: true, All: true,
		Description: "Uploads are media: a PHP file there is almost always a web shell.",
		Conds:       []Cond{c("path", "regex", `^/wp-content/uploads/.*\.(php[0-9]?|phtml|phar)$`)}},
	{Code: "wp-1008", CMS: "wordpress", Name: "Block wp-config.php backups", Enabled: true, All: true,
		Description: "Editor and backup copies (wp-config.php.bak, ~, .save, …) would show the database password.",
		Conds:       []Cond{c("path", "regex", `/wp-config\.php.+`)}},
	{Code: "wp-1009", CMS: "wordpress", Name: "Block wp-content/debug.log", Enabled: true, All: true,
		Description: "The debug log can contain paths, queries and secrets.",
		Conds:       []Cond{c("path", "suffix", "/wp-content/debug.log")}},
	{Code: "wp-1010", CMS: "wordpress", Name: "Hide readme.html and license.txt", All: true,
		Description: "They reveal the WordPress version to scanners.",
		Conds:       []Cond{c("path", "equals", "/readme.html", "/license.txt")}},
	{Code: "wp-1011", CMS: "wordpress", Name: "CVE-2024-5932 (GiveWP object injection)", Enabled: true, All: true,
		Description: "GiveWP ≤ 3.14.1: unauthenticated remote code execution through a serialized object in give_title.",
		Conds:       []Cond{c("body", "contains", "give_title"), c("body", "regex", `O:[0-9]+:"`)}},
	{Code: "wp-1012", CMS: "wordpress", Name: "CVE-2023-4634 (Media Library Assistant)", Enabled: true, All: true,
		Description: "Media Library Assistant ≤ 3.09: remote code execution through mla-stream-image.php.",
		Conds:       []Cond{c("path", "contains", "/wp-content/plugins/media-library-assistant/includes/mla-stream-image.php"), c("query", "contains", "mla_stream_file")}},
}

// defaultCrawlers are scraper and bad-bot User-Agents (爬蟲防護).
var defaultCrawlers = []string{
	"python-requests", "python-urllib", "aiohttp", "httpx", "Go-http-client", "okhttp", "Java/", "Apache-HttpClient", "libwww-perl",
	"Wget", "curl/", "Scrapy", "HeadlessChrome", "PhantomJS", "MJ12bot", "SemrushBot", "AhrefsBot", "DotBot", "BLEXBot",
	"DataForSeoBot", "MegaIndex", "Barkrowler", "serpstatbot", "PetalBot", "ZoominfoBot", "MauiBot", "Nimbostratus", "zgrab", "masscan",
}

// defaultIDC are cloud / hosting networks (禁 IDC) by AS number.
var defaultIDC = []ASN{
	{16509, "Amazon AWS"}, {14618, "Amazon AWS"}, {8075, "Microsoft Azure"}, {396982, "Google Cloud"}, {45102, "Alibaba Cloud"},
	{37963, "Alibaba Cloud (CN)"}, {45090, "Tencent Cloud"}, {132203, "Tencent Cloud"}, {136907, "Huawei Cloud"}, {14061, "DigitalOcean"},
	{63949, "Akamai / Linode"}, {20473, "Vultr"}, {16276, "OVH"}, {24940, "Hetzner"}, {31898, "Oracle Cloud"}, {51167, "Contabo"},
	{60781, "Leaseweb"}, {9009, "M247"}, {212238, "Datacamp"}, {53667, "FranTech / BuyVM"},
}

// spiderUA is the User-Agent token of each trusted crawler list: a request
// claiming it from outside the trusted ranges is a fake spider (假蜘蛛).
var spiderUA = map[string][]string{
	"googlebot": {"Googlebot"}, "google_special": {"AdsBot-Google", "Mediapartners-Google"}, "bingbot": {"bingbot"},
	"baidu": {"Baiduspider"}, "yandexbot": {"YandexBot"}, "duckduckbot": {"DuckDuckBot"}, "applebot": {"Applebot"},
	"sogou": {"Sogou web spider", "Sogou inst spider"}, "so360": {"360Spider"}, "toutiao": {"Bytespider"},
	"gptbot": {"GPTBot"}, "oai_searchbot": {"OAI-SearchBot"}, "chatgpt_user": {"ChatGPT-User"}, "claudebot": {"ClaudeBot"},
	"perplexitybot": {"PerplexityBot"}, "perplexity_user": {"Perplexity-User"},
	"all_ai": {"GPTBot", "OAI-SearchBot", "ChatGPT-User", "ClaudeBot", "PerplexityBot", "Perplexity-User"},
}
