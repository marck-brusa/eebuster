package report

import (
	"bytes"
	"encoding/json"
	"html/template"
	"strings"
	"time"
)

// The stylesheet, the script and the embedded run data are spliced in after template
// execution, through these placeholders: they are constants or encoded JSON, and keeping them
// out of html/template avoids its JavaScript and CSS context rewriting.
const (
	cssPlaceholder  = "REPORT_CSS_PLACEHOLDER"
	jsPlaceholder   = "REPORT_JS_PLACEHOLDER"
	dataPlaceholder = "REPORT_DATA_PLACEHOLDER"
)

var htmlTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"lower": strings.ToLower,
}).Parse(reportTemplate))

// HTML renders the run as one self-contained page: no external fonts, scripts, images or
// links. The run's JSON is embedded, so the report can be re-rendered or re-imported from
// the file alone.
func HTML(r *Run) ([]byte, error) {
	var page bytes.Buffer
	err := htmlTemplate.Execute(&page, buildView(r, time.Now()))
	var out []byte
	if err == nil {
		var data []byte
		// json.Marshal escapes <, > and &, so the data cannot close its script element.
		data, err = json.Marshal(r)
		if err == nil {
			text := page.String()
			text = strings.Replace(text, cssPlaceholder, reportCSS+printHeaderCSS(r), 1)
			text = spliceLast(text, jsPlaceholder, reportJS)
			text = spliceLast(text, dataPlaceholder, string(data))
			out = []byte(text)
		}
	}
	return out, err
}

// printHeaderCSS puts the run id and device on every printed page, where the browser
// supports page margin boxes.
// spliceLast replaces the last occurrence of a placeholder: the script elements sit at the
// end of the page, after any run text (notes, step logs) that could spell the placeholder.
func spliceLast(text, placeholder, replacement string) string {
	if i := strings.LastIndex(text, placeholder); i >= 0 {
		text = text[:i] + replacement + text[i+len(placeholder):]
	}
	return text
}

func printHeaderCSS(r *Run) string {
	header := cssString("Run " + r.ID + " · " + deviceTitle(r))
	return "@page { @top-left { content: " + header + "; font: 8pt system-ui, sans-serif; color: #555; } " +
		"@bottom-right { content: \"Page \" counter(page) \" of \" counter(pages); font: 8pt system-ui, sans-serif; color: #555; } " +
		"@bottom-left { content: \"eebus-testbench use-case test report\"; font: 8pt system-ui, sans-serif; color: #555; } }\n"
}

func cssString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		case c < 0x20 || c == '<' || c == '>':
			b.WriteByte(' ')
		default:
			b.WriteRune(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

const reportTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="eebus-testbench {{.Version}}">
<title>EEBUS test report {{.ID}}</title>
<style>REPORT_CSS_PLACEHOLDER</style>
</head>
<body>
<header class="masthead">
  <div class="eyebrow">EEBUS use-case test report</div>
  <div class="title-row">
    <div>
      <h1>{{.Title}}</h1>
      {{if .Subtitle}}<div class="subtitle mono">{{.Subtitle}}</div>{{end}}
    </div>
    <div class="verdict verdict-{{.Status}}">{{.StatusWord}}</div>
  </div>
  <p class="headline">{{.Headline}}</p>
  <div class="tiles">
    <div class="tile"><span class="tile-n">{{.Summary.Total}}</span><span class="tile-l">test cases</span></div>
    <div class="tile tile-passed"><span class="tile-n">{{.Summary.Passed}}</span><span class="tile-l">passed</span></div>
    <div class="tile tile-failed"><span class="tile-n">{{.Summary.Failed}}</span><span class="tile-l">failed</span></div>
    <div class="tile tile-skipped"><span class="tile-n">{{.Summary.Skipped}}</span><span class="tile-l">skipped</span></div>
  </div>
  {{if .SkipReasons}}<div class="skip-reasons">Skipped: {{range $i, $r := .SkipReasons}}{{if $i}}, {{end}}{{$r.Value}} {{lower $r.Key}}{{end}}</div>{{end}}
  {{range .Notices}}<div class="notice">{{.}}</div>{{end}}
  <nav class="toolbar no-print" aria-label="Report controls">
    <div class="seg" role="group" aria-label="Filter by verdict">
      <button type="button" class="active" data-filter="all">All</button>
      <button type="button" data-filter="failed">Failed</button>
      <button type="button" data-filter="skipped">Skipped</button>
      <button type="button" data-filter="passed">Passed</button>
    </div>
    <select id="group-filter" aria-label="Filter by use case">
      <option value="">All use cases</option>
      {{range .GroupKeys}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
    <input id="search" type="search" placeholder="Search test cases" aria-label="Search test cases">
    <button type="button" id="expand-all">Expand all</button>
    <button type="button" id="collapse-all">Collapse all</button>
    <button type="button" id="print">Print / PDF</button>
  </nav>
</header>
<main>
<section id="identification">
  <h2>Identification</h2>
  <div class="cards">
    {{range .Cards}}<div class="card"><h3>{{.Title}}</h3><dl class="kv">{{range .Rows}}<dt>{{.Key}}</dt><dd{{if .Mono}} class="mono"{{end}}>{{.Value}}</dd>{{end}}</dl>{{if .Note}}<p class="card-note">{{.Note}}</p>{{end}}</div>{{end}}
  </div>
  {{if .Vehicles}}<h3 class="sub">Vehicles</h3>
  <div class="cards">
    {{range .Vehicles}}<div class="card"><h3>{{.Title}}</h3><dl class="kv">{{range .Rows}}<dt>{{.Key}}</dt><dd{{if .Mono}} class="mono"{{end}}>{{.Value}}</dd>{{end}}</dl></div>{{end}}
  </div>{{else}}<p class="muted">No vehicle was connected at the start or the end of the run.</p>{{end}}
</section>

{{if .Conditions}}<section id="conditions">
  <h2>Conditions</h2>
  <p class="muted">The device's state before the first and after the last test case. Changed values are marked.</p>
  <div class="scroll"><table class="grid"><thead><tr><th>Value</th><th>At start</th><th>At end</th></tr></thead><tbody>
  {{range .Conditions}}<tr{{if .Changed}} class="changed"{{end}}><td>{{.Field}}</td><td class="mono">{{.Start}}</td><td class="mono">{{.End}}</td></tr>{{end}}
  </tbody></table></div>
</section>{{end}}

<section id="use-cases">
  <h2>Use cases</h2>
  <p class="muted">Chips are the scenarios of each use case with the device's requirement level (M mandatory, R recommended, O optional). Filled: advertised. Red: mandatory and not advertised.</p>
  <div class="scroll"><table class="grid"><thead><tr><th>Use case</th><th>Advertised</th><th>Scenarios</th><th class="num">Tests</th><th class="num">Passed</th><th class="num">Failed</th><th class="num">Skipped</th></tr></thead><tbody>
  {{range .UseCases}}<tr>
    <td><strong>{{.Acronym}}</strong><div class="muted small">{{.Title}}</div></td>
    <td>{{.Advertised}}{{if .Detail}}<div class="muted small">{{.Detail}}</div>{{end}}</td>
    <td>{{range .Chips}}<span class="chip {{.Class}}" title="{{.Title}}">{{.Label}}</span>{{end}}</td>
    <td class="num">{{.Total}}</td><td class="num n-passed">{{.Passed}}</td><td class="num n-failed">{{.Failed}}</td><td class="num n-skipped">{{.Skipped}}</td>
  </tr>{{end}}
  </tbody></table></div>
</section>

{{if .Checks}}<section id="checks">
  <h2>Run-level checks</h2>
  <div class="scroll"><table class="grid"><thead><tr><th>Check</th><th>Result</th><th>Detail</th></tr></thead><tbody>
  {{range .Checks}}<tr><td>{{.Title}}</td><td><span class="pill pill-{{.Status}}">{{.Status}}</span></td><td>{{.Detail}}</td></tr>{{end}}
  </tbody></table></div>
</section>{{end}}

<section id="test-cases">
  <h2>Test cases</h2>
  {{range .Groups}}<div class="group" data-group="{{.Key}}">
    <h3 class="group-title">{{.Title}} <span class="group-counts"><span class="n-passed">{{.Passed}} passed</span> · <span class="n-failed">{{.Failed}} failed</span> · <span class="n-skipped">{{.Skipped}} skipped</span></span></h3>
    {{range .Cases}}<details class="tc tc-{{.Status}}" id="{{.Anchor}}" data-status="{{.Status}}" data-group="{{.Group}}" data-search="{{.Search}}">
      <summary>
        <span class="pill pill-{{.Status}}">{{.StatusWord}}</span>
        <span class="tc-main"><span class="tc-title">{{.Title}}</span><span class="tc-sub mono">{{.ID}}{{if .SpecLine}} · {{.SpecLine}}{{end}}</span></span>
        <span class="tc-dur">{{.Duration}}</span>
      </summary>
      <div class="tc-body">
        {{if .Reason}}<p class="outcome outcome-skipped"><strong>{{.Reason}}.</strong> {{.Failure}}</p>{{else if .Failure}}<p class="outcome outcome-failed">{{.Failure}}</p>{{end}}
        <dl class="kv facts">{{range .Facts}}<dt{{if .ScreenOnly}} class="screen-only"{{end}}>{{.Key}}</dt><dd class="{{if .Mono}}mono{{end}}{{if .ScreenOnly}} screen-only{{end}}">{{.Value}}</dd>{{end}}</dl>
        <table class="steps"><thead><tr><th class="num">#</th><th>Step</th><th>Result</th><th class="num">Time</th></tr></thead><tbody>
        {{range .Steps}}<tr class="step step-{{.Status}}"><td class="num">{{.Index}}</td><td><div class="mono step-name">{{.Name}}</div>{{if .Detail}}<div class="step-detail">{{.Detail}}</div>{{end}}
          {{if .Assertions}}<table class="asserts"><colgroup><col class="c-key"><col class="c-op"><col class="c-val"><col class="c-val"><col class="c-ok"></colgroup><thead><tr><th>Value</th><th>Check</th><th>Expected</th><th>Actual</th><th></th></tr></thead><tbody>
          {{range .Assertions}}<tr class="{{if .OK}}ok{{else}}bad{{end}}"><td class="mono">{{.Key}}</td><td>{{.Op}}</td><td class="mono">{{.Expected}}</td><td class="mono">{{.Actual}}</td><td>{{if .OK}}✓{{else}}✗{{end}}</td></tr>{{end}}
          </tbody></table>{{end}}
          {{if .Request}}<details class="evidence"><summary class="mono">{{.Request.Line}}</summary>
            {{if .Request.Body}}<div class="ev-label">Sent</div><pre>{{.Request.Body}}</pre>{{end}}
            {{if .Request.Response}}<div class="ev-label">Received{{if .Request.Truncated}} (first 4 KiB){{end}}</div><pre>{{.Request.Response}}</pre>{{end}}
          </details>{{end}}
          {{if .Events}}<div class="step-meta">Events: <span class="mono">{{.Events}}</span></div>{{end}}
          {{if .Findings}}<div class="step-meta findings">Conformance: {{range .Findings}}<div class="mono">{{.}}</div>{{end}}{{if .MoreFinding}}<div>and {{.MoreFinding}} more</div>{{end}}</div>{{end}}
          {{if .Trace}}<div class="step-meta">Trace frames <span class="mono">{{.Trace}}</span></div>{{end}}
        </td><td><span class="pill pill-{{.Status}}">{{.Status}}</span></td><td class="num">{{.Duration}}</td></tr>{{end}}
        </tbody></table>
        {{if .Frames}}<details class="evidence frames"><summary class="mono">Wire frames: {{len .Frames}} recorded during this test case</summary>
        <table class="frames"><thead><tr><th class="num">#</th><th>Time</th><th>Direction</th><th>Function</th><th>Classifier</th><th class="num">Bytes</th><th>Findings</th></tr></thead><tbody>
        {{range .Frames}}<tr class="frame{{if .Findings}} bad{{end}}"><td class="num mono">{{.Seq}}</td><td class="mono">{{.Time}}</td><td>{{.Dir}}</td><td class="mono">{{.Function}}</td><td>{{.Classifier}}</td><td class="num">{{.Size}}</td><td>{{.Findings}}</td></tr>
        <tr class="frame-raw"><td colspan="7"><details><summary class="mono">payload</summary><pre>{{.Raw}}</pre></details></td></tr>{{end}}
        </tbody></table></details>{{end}}
      </div>
    </details>{{end}}
  </div>{{end}}
  <p class="muted no-results" hidden>No test case matches the filter.</p>
</section>

<section id="conformance">
  <h2>Wire conformance</h2>
  <p>{{.Conformance.Frames}} frames during the run; {{.Conformance.ErrorFrames}} with conformance errors, {{.Conformance.WarningFrames}} with warnings only. The checks run on each frame exactly as it arrived, before the stack repairs malformed JSON.</p>
  {{if .Conformance.Rules}}<div class="scroll"><table class="grid"><thead><tr><th>Rule</th><th>Severity</th><th class="num">Frames</th><th>First</th><th>Reference</th></tr></thead><tbody>
  {{range .Conformance.Rules}}<tr><td class="mono">{{.Rule}}{{if .Example}}<div class="muted small">{{.Example}}</div>{{end}}</td><td>{{.Severity}}</td><td class="num">{{.Count}}</td><td class="mono">#{{.FirstSeq}}</td><td class="small">{{.SpecRef}}</td></tr>{{end}}
  </tbody></table></div>{{end}}
</section>

<section id="appendix">
  <h2>Appendix</h2>
  {{if .Parameters}}<h3 class="sub">Device parameters</h3>
  <dl class="kv">{{range .Parameters}}<dt>{{.Key}}</dt><dd class="mono">{{.Value}}</dd>{{end}}</dl>
  {{range .ParamNotes}}<p class="muted small">{{.}}</p>{{end}}{{end}}
  <h3 class="sub">Verdicts</h3>
  <dl class="kv">{{range .Glossary}}<dt>{{.Key}}</dt><dd>{{.Value}}</dd>{{end}}</dl>
  <p class="muted small">The run's complete record is embedded in this file as JSON (element <span class="mono">report-data</span>); <span class="mono">eebus-testbench report</span> renders it again in every format.</p>
</section>
</main>
<footer class="foot">
  <p>{{.Disclaimer}}</p>
  <p class="muted small">eebus-testbench {{.Version}} · generated {{.Generated}}</p>
</footer>
<script type="application/json" id="report-data">REPORT_DATA_PLACEHOLDER</script>
<script>REPORT_JS_PLACEHOLDER</script>
</body>
</html>
`

const reportCSS = `
:root {
  --bg: #f6f7f9; --panel: #ffffff; --text: #1b2430; --muted: #5d6877; --line: #dde2e8; --soft: #eef1f5;
  --pass: #1a7f45; --pass-bg: #e3f4ea; --fail: #b42318; --fail-bg: #fde8e6; --skip: #8a5a00; --skip-bg: #fdf1d8;
  --accent: #2557c7; --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --bg: #11151b; --panel: #181e26; --text: #e3e8ef; --muted: #98a3b3; --line: #2b3440; --soft: #202833;
    --pass: #5cc98a; --pass-bg: #163524; --fail: #ff8a80; --fail-bg: #3d1a18; --skip: #f2c46b; --skip-bg: #3a2d12; --accent: #8fb0ff;
  }
}
* { box-sizing: border-box; }
html { -webkit-text-size-adjust: 100%; }
body { margin: 0; background: var(--bg); color: var(--text); font: 14px/1.45 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
.mono, pre, code { font-family: var(--mono); font-size: 12.5px; }
.muted { color: var(--muted); }
.small { font-size: 12px; }
.masthead, main, .foot { max-width: 1180px; margin: 0 auto; padding: 0 16px; }
.masthead { padding-top: 28px; padding-bottom: 8px; }
.eyebrow { text-transform: uppercase; letter-spacing: .08em; font-size: 11px; color: var(--muted); font-weight: 600; }
.title-row { display: flex; gap: 16px; align-items: flex-start; justify-content: space-between; flex-wrap: wrap; }
h1 { margin: 4px 0 2px; font-size: 26px; line-height: 1.2; overflow-wrap: anywhere; }
.subtitle { color: var(--muted); overflow-wrap: anywhere; }
.verdict { font-weight: 700; padding: 6px 14px; border-radius: 999px; font-size: 15px; border: 1px solid transparent; white-space: nowrap; }
.verdict-passed { color: var(--pass); background: var(--pass-bg); }
.verdict-failed, .verdict-aborted { color: var(--fail); background: var(--fail-bg); }
.verdict-running, .verdict-cancelled { color: var(--skip); background: var(--skip-bg); }
.headline { font-size: 19px; font-weight: 600; margin: 14px 0 12px; }
.tiles { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; }
.tile { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; padding: 10px 14px; display: flex; flex-direction: column; }
.tile-n { font-size: 26px; font-weight: 700; }
.tile-l { color: var(--muted); font-size: 12px; }
.tile-passed .tile-n, .n-passed { color: var(--pass); }
.tile-failed .tile-n, .n-failed { color: var(--fail); }
.tile-skipped .tile-n, .n-skipped { color: var(--skip); }
.skip-reasons { margin-top: 8px; color: var(--muted); font-size: 13px; }
.notice { margin-top: 8px; padding: 8px 12px; border-left: 3px solid var(--skip); background: var(--skip-bg); border-radius: 4px; }
.toolbar { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin: 18px 0 4px; position: sticky; top: 0; background: var(--bg); padding: 8px 0; z-index: 2; }
.toolbar button, .toolbar select, .toolbar input { font: inherit; font-size: 13px; padding: 6px 10px; border-radius: 6px; border: 1px solid var(--line); background: var(--panel); color: var(--text); }
.toolbar input { min-width: 0; flex: 1 1 160px; }
.toolbar button { cursor: pointer; }
.seg { display: inline-flex; }
.seg button { border-radius: 0; margin-left: -1px; }
.seg button:first-child { border-radius: 6px 0 0 6px; margin-left: 0; }
.seg button:last-child { border-radius: 0 6px 6px 0; }
.seg button.active { background: var(--accent); color: #fff; border-color: var(--accent); }
section { margin: 26px 0; }
h2 { font-size: 18px; margin: 0 0 10px; padding-bottom: 6px; border-bottom: 1px solid var(--line); }
h3 { font-size: 15px; margin: 0 0 8px; }
h3.sub { margin-top: 16px; }
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 12px; }
.card { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; padding: 12px 14px; min-width: 0; }
.card-note { color: var(--muted); font-size: 12px; margin: 8px 0 0; }
dl.kv { display: grid; grid-template-columns: fit-content(42%) minmax(0, 1fr); gap: 3px 12px; margin: 0; }
dl.kv dt { color: var(--muted); }
dl.kv dd { margin: 0; overflow-wrap: anywhere; }
table { border-collapse: collapse; width: 100%; }
.scroll { background: var(--panel); border: 1px solid var(--line); border-radius: 10px; overflow-x: auto; }
.grid { width: 100%; }
.grid th, .grid td { text-align: left; padding: 7px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
.grid th { background: var(--soft); font-size: 12px; color: var(--muted); font-weight: 600; }
.grid tr:last-child td { border-bottom: 0; }
.num { text-align: right; white-space: nowrap; }
tr.changed td { background: var(--skip-bg); }
.chip { display: inline-block; font-size: 11px; font-family: var(--mono); padding: 1px 6px; margin: 1px 3px 1px 0; border-radius: 4px; border: 1px solid var(--line); color: var(--muted); }
.chip-on { background: var(--pass-bg); color: var(--pass); border-color: transparent; }
.chip-missing { background: var(--fail-bg); color: var(--fail); border-color: var(--fail); }
.pill { display: inline-block; font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: .03em; padding: 2px 8px; border-radius: 999px; white-space: nowrap; }
.pill-passed { color: var(--pass); background: var(--pass-bg); }
.pill-failed { color: var(--fail); background: var(--fail-bg); }
.pill-skipped { color: var(--skip); background: var(--skip-bg); }
.group { margin-bottom: 18px; }
.group-title { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 6px; font-size: 15px; margin-top: 18px; }
.group-counts { font-weight: 400; font-size: 12px; }
details.tc { background: var(--panel); border: 1px solid var(--line); border-left: 4px solid var(--line); border-radius: 8px; margin: 6px 0; }
details.tc.tc-passed { border-left-color: var(--pass); }
details.tc.tc-failed { border-left-color: var(--fail); }
details.tc.tc-skipped { border-left-color: var(--skip); }
details.tc > summary { list-style: none; cursor: pointer; display: flex; gap: 10px; align-items: center; padding: 9px 12px; }
details.tc > summary::-webkit-details-marker { display: none; }
.tc-main { display: flex; flex-direction: column; min-width: 0; flex: 1; }
.tc-title { font-weight: 600; overflow-wrap: anywhere; }
.tc-sub { color: var(--muted); font-size: 11.5px; overflow-wrap: anywhere; }
.tc-dur { color: var(--muted); font-size: 12px; white-space: nowrap; }
.tc-body { padding: 4px 14px 14px; border-top: 1px solid var(--line); }
.outcome { padding: 8px 10px; border-radius: 6px; margin: 10px 0; overflow-wrap: anywhere; }
.outcome-failed { background: var(--fail-bg); color: var(--fail); }
.outcome-skipped { background: var(--skip-bg); color: var(--skip); }
.facts { margin: 10px 0; }
table.steps { margin-top: 8px; }
table.steps > thead th { font-size: 12px; color: var(--muted); text-align: left; padding: 4px 6px; border-bottom: 1px solid var(--line); }
table.steps > tbody > tr > td { padding: 6px; border-bottom: 1px solid var(--line); vertical-align: top; }
.step-name { overflow-wrap: anywhere; }
.step-detail { color: var(--muted); font-size: 12.5px; margin-top: 2px; overflow-wrap: anywhere; }
.step-failed .step-detail { color: var(--fail); }
.step-meta { color: var(--muted); font-size: 12px; margin-top: 4px; overflow-wrap: anywhere; }
table.asserts { margin: 6px 0; font-size: 12.5px; background: var(--soft); border-radius: 6px; table-layout: fixed; }
table.asserts col.c-key { width: 38%; } table.asserts col.c-op { width: 16%; } table.asserts col.c-val { width: 21%; } table.asserts col.c-ok { width: 4%; }
table.asserts th { text-align: left; font-size: 11px; color: var(--muted); padding: 3px 6px; }
table.asserts td { padding: 3px 6px; border-top: 1px solid var(--line); overflow-wrap: anywhere; }
table.asserts tr.bad td { color: var(--fail); }
details.evidence { margin-top: 6px; }
details.evidence > summary { cursor: pointer; color: var(--accent); font-size: 12px; overflow-wrap: anywhere; }
.ev-label { font-size: 11px; color: var(--muted); margin-top: 6px; text-transform: uppercase; letter-spacing: .04em; }
table.frames { margin-top: 6px; font-size: 12px; width: 100%; }
table.frames th { text-align: left; font-size: 11px; color: var(--muted); padding: 3px 6px; border-bottom: 1px solid var(--line); }
table.frames td { padding: 3px 6px; border-top: 1px solid var(--line); overflow-wrap: anywhere; vertical-align: top; }
table.frames tr.bad td { color: var(--fail); }
table.frames tr.frame-raw td { border-top: none; padding-top: 0; }
table.frames tr.frame-raw summary { cursor: pointer; color: var(--accent); font-size: 11px; }
pre { background: var(--soft); border-radius: 6px; padding: 8px; margin: 4px 0; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 360px; overflow: auto; }
.hidden { display: none !important; }
.foot { padding-top: 10px; padding-bottom: 40px; border-top: 1px solid var(--line); margin-top: 30px; }
@media (max-width: 640px) {
  .tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  dl.kv { grid-template-columns: minmax(0, 1fr); }
  dl.kv dd { margin-bottom: 4px; }
  h1 { font-size: 21px; }
  details.tc > summary { flex-wrap: wrap; }
}
@media print {
  :root { --bg: #fff; --panel: #fff; --text: #000; --muted: #444; --line: #ccc; --soft: #f3f3f3; }
  body { font-size: 10pt; background: #fff; }
  .no-print, .toolbar { display: none !important; }
  .hidden { display: block !important; }
  details.tc.hidden { display: block !important; }
  .masthead, main, .foot { max-width: none; padding: 0; }
  details.tc::details-content { content-visibility: visible; display: block; }
  details.tc > summary, table.steps tr, table.asserts tr, dl.kv dt, dl.kv dd { break-inside: avoid; }
  .screen-only { display: none !important; }
  details.evidence { display: none; }
  details.tc.tc-skipped .facts, details.tc.tc-skipped table.steps { display: none; }
  #test-cases { break-before: page; }
  .group-title { break-after: avoid-page; }
  pre { max-height: none; overflow: visible; }
  .scroll { overflow: visible; }
  .cards { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  a { color: inherit; text-decoration: none; }
}
`

const reportJS = `
(function () {
  var cases = Array.prototype.slice.call(document.querySelectorAll("details.tc"));
  var groups = Array.prototype.slice.call(document.querySelectorAll(".group"));
  var state = { status: "all", group: "", text: "" };
  function apply() {
    var shown = 0;
    cases.forEach(function (el) {
      var visible = (state.status === "all" || el.dataset.status === state.status) &&
        (!state.group || el.dataset.group === state.group) &&
        (!state.text || el.dataset.search.indexOf(state.text) >= 0);
      el.classList.toggle("hidden", !visible);
      if (visible) { shown++; }
    });
    groups.forEach(function (g) {
      g.classList.toggle("hidden", !g.querySelector("details.tc:not(.hidden)"));
    });
    var none = document.querySelector(".no-results");
    if (none) { none.hidden = shown > 0; }
  }
  document.querySelectorAll("[data-filter]").forEach(function (button) {
    button.addEventListener("click", function () {
      document.querySelectorAll("[data-filter]").forEach(function (b) { b.classList.remove("active"); });
      button.classList.add("active");
      state.status = button.dataset.filter;
      apply();
    });
  });
  var groupFilter = document.getElementById("group-filter");
  if (groupFilter) { groupFilter.addEventListener("change", function () { state.group = groupFilter.value; apply(); }); }
  var search = document.getElementById("search");
  if (search) { search.addEventListener("input", function () { state.text = search.value.trim().toLowerCase(); apply(); }); }
  function setOpen(open) {
    document.querySelectorAll("details").forEach(function (d) { d.open = open; });
  }
  var expand = document.getElementById("expand-all");
  if (expand) { expand.addEventListener("click", function () { setOpen(true); }); }
  var collapse = document.getElementById("collapse-all");
  if (collapse) { collapse.addEventListener("click", function () { setOpen(false); }); }
  var remembered = null;
  window.addEventListener("beforeprint", function () {
    remembered = Array.prototype.map.call(document.querySelectorAll("details"), function (d) { return d.open; });
    document.querySelectorAll("details.tc").forEach(function (d) { d.open = true; });
  });
  window.addEventListener("afterprint", function () {
    if (remembered) {
      document.querySelectorAll("details").forEach(function (d, i) { d.open = remembered[i]; });
      remembered = null;
    }
  });
  var printButton = document.getElementById("print");
  if (printButton) { printButton.addEventListener("click", function () { window.print(); }); }
  cases.forEach(function (el) { if (el.dataset.status === "failed") { el.open = true; } });
  if (location.hash === "#print") { document.querySelectorAll("details.tc").forEach(function (d) { d.open = true; }); }
  else if (location.hash.indexOf("#tc-") === 0) {
    var target = document.getElementById(location.hash.slice(1));
    if (target) { target.open = true; }
  }
})();
`
