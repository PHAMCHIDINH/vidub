package pipeline

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// fresh reports whether output exists and is at least as new as every input
// that exists. Steps skip their work only when their output is fresh, so
// re-running a step (a force refresh, or a retry after a failure) makes every
// later step run again instead of reusing output built from old data.
func fresh(output string, inputs ...string) bool {
	out, err := os.Stat(output)
	if err != nil {
		return false
	}
	for _, in := range inputs {
		if st, err := os.Stat(in); err == nil && st.ModTime().After(out.ModTime()) {
			return false
		}
	}
	return true
}

// ResultFile is a job output offered for download.
type ResultFile struct {
	Name  string
	Label string
}

// resultFiles lists every downloadable output, in display order.
var resultFiles = []ResultFile{
	{"voiceover.mp4", "Voiceover (.mp4)"},
	{"dub.mp4", "Replace (.mp4)"},
	{"narrative.mp3", "Narrative Audio (.mp3)"},
	{"dub.mp3", "Dubbing Audio (.mp3)"},
	{"subtitle.srt", "Subtitle (.srt)"},
}

// IsResultFile reports whether name may be downloaded from a job directory.
// Anything else there (source video, JSON) is not exposed.
func IsResultFile(name string) bool {
	for _, f := range resultFiles {
		if f.Name == name {
			return true
		}
	}
	return false
}

// ResultFiles returns the outputs that exist in workDir. A job only produces
// the files of its Mix Mode, and older jobs may have others.
func ResultFiles(workDir string) []ResultFile {
	var out []ResultFile
	for _, f := range resultFiles {
		if fileExists(filepath.Join(workDir, f.Name)) {
			out = append(out, f)
		}
	}
	return out
}

// The emit* helpers push HTML fragments over SSE. The job page swaps them in
// with sse-swap (see templates/partials/pipeline_view.html):
//   step_log            appended to #timeline
//   progress            replaces the content of #progress-box
//   job_complete/error  replaces the content of #results

func (p *Pipeline) emitStepLog(jobID, step, status, message string, elapsed float64) {
	icon := "&#9654;"
	cls := ""
	switch status {
	case "completed":
		icon = "&#10003;"
		cls = "done"
	case "failed":
		icon = "&#10007;"
		cls = "failed"
	case "warning":
		icon = "&#9888;"
		cls = "warn"
	}

	elapsedHTML := ""
	if elapsed > 0 {
		elapsedHTML = fmt.Sprintf(`<span class="tl-elapsed">%.1fs</span>`, elapsed)
	}

	fragment := fmt.Sprintf(
		`<div class="tl-entry %s" data-step="%s">`+
			`<span class="tl-time">%s</span>`+
			`<div class="tl-body">`+
			`<span class="tl-step">%s %s%s</span>`+
			`<div class="tl-msg">%s</div>`+
			`</div>`+
			`</div>`,
		cls, step, time.Now().Format("15:04:05"), icon, step, elapsedHTML, html.EscapeString(message),
	)
	p.broker.Publish(jobID, "step_log", fragment)
}

// progressHTML is the content of #progress-box.
func progressHTML(pct float64, label string) string {
	return fmt.Sprintf(
		`<div class="pipeline-header"><span class="muted" id="pct-label">%s</span></div>`+
			`<progress value="%.0f" max="100" id="job-progress"></progress>`,
		label, pct,
	)
}

func (p *Pipeline) emitProgress(jobID string, pct float64) {
	p.broker.Publish(jobID, "progress", progressHTML(pct, fmt.Sprintf("%.0f%%", pct)))
}

func (p *Pipeline) emitComplete(jobID, workDir string) {
	var links strings.Builder
	for _, f := range ResultFiles(workDir) {
		// Names and labels are fixed strings and jobID is a validated video ID.
		fmt.Fprintf(&links, `<a href="/jobs/%s/files/%s" class="download-btn">⬇ %s</a>`, jobID, f.Name, f.Label)
	}
	fragment := fmt.Sprintf(
		`<div id="progress-box" hx-swap-oob="innerHTML">%s</div>`+
			`<div class="card"><div class="downloads">%s</div></div>`,
		progressHTML(100, "100% — Done!"), links.String(),
	)
	p.broker.Publish(jobID, "job_complete", fragment)
}

func (p *Pipeline) emitError(jobID, message string) {
	fragment := fmt.Sprintf(
		`<div class="card" style="border-color:#da3633"><p>❌ %s</p></div>`,
		html.EscapeString(message),
	)
	p.broker.Publish(jobID, "job_error", fragment)
}
