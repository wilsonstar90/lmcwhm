package errpages

// Built-in pages. They use nginx SSI: the status, the request and, for a
// refusal, its reason are filled in when the page is served.

const style = `<style>
:root{--bg:#f6f7f9;--card:#fff;--fg:#1f2329;--mute:#6b7280;--line:#e5e7eb;--accent:#20a53a}
@media (prefers-color-scheme:dark){:root{--bg:#16181d;--card:#1f2228;--fg:#e5e7eb;--mute:#9ca3af;--line:#2d3139}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;min-height:100dvh;display:grid;place-items:center;background:var(--bg);color:var(--fg);
font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue","PingFang HK","Microsoft JhengHei",sans-serif;padding:16px}
main{max-width:520px;width:100%;background:var(--card);border:1px solid var(--line);border-radius:12px;padding:32px}
.code{font-size:56px;font-weight:700;line-height:1;color:var(--accent)}h1{font-size:20px;margin:12px 0 8px}p{margin:0 0 8px;color:var(--mute)}
dl{display:grid;grid-template-columns:auto 1fr;gap:4px 12px;margin:20px 0 0;font-size:13px;color:var(--mute)}dd{margin:0;word-break:break-all}
</style>`

func page(title, body string) string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title><!--# echo var="status" --> ` + title + `</title>
` + style + `
</head>
<body>
<main>
<div class="code"><!--# echo var="status" --></div>
` + body + `
<dl>
<dt>URL</dt><dd><!--# echo var="host" --><!--# echo var="request_uri" --></dd>
<dt>IP</dt><dd><!--# echo var="remote_addr" --></dd>
<dt>Time</dt><dd><!--# echo var="time_local" --></dd>
<dt>ID</dt><dd><!--# echo var="request_id" --></dd>
</dl>
</main>
</body>
</html>
`
}

var builtins = []Page{
	{Code: "generic", Name: "General error", HTML: page("Error", `<h1>Something went wrong</h1>
<p>The server could not complete this request.</p>`)},
	{Code: "blocked", Name: "Request blocked", HTML: page("Blocked", `<h1>This request was blocked</h1>
<p>The website's security rules stopped it. If you think this is a mistake, contact the site owner with the details below.</p>
<p>Reason: <!--# echo var="lmcwhm_block_reason" default="security policy" --></p>`)},
	{Code: "notfound", Name: "Page not found", HTML: page("Not found", `<h1>Page not found</h1>
<p>The address may be mistyped, or the page has moved.</p>`)},
	{Code: "limit", Name: "Too many requests", HTML: page("Slow down", `<h1>Too many requests</h1>
<p>Please wait a moment and try again.</p>`)},
	{Code: "maintenance", Name: "Unavailable / maintenance", HTML: page("Unavailable", `<h1>Temporarily unavailable</h1>
<p>The website is being updated or its server is busy. Please try again shortly.</p>`)},
}
