package pipeline

import (
	"fmt"
	"os"
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

func (p *Pipeline) emitStepLog(jobID, step, status, message string, elapsed float64) {
	data := fmt.Sprintf(`{"step":"%s","status":"%s","time":"%s","message":"%s","elapsed":%.1f}`,
		step, status, time.Now().Format("15:04:05"), message, elapsed)
	p.broker.Publish(jobID, "step_log", data)
}

func (p *Pipeline) emitStepStart(jobID, step string, allSteps []string) {
	stepHTML := buildStepIndicators(step, allSteps)
	progressHTML := fmt.Sprintf(
		`<progress value="0" max="100" id="job-progress" hx-swap-oob="true"></progress>
		 <p class="muted" id="pct-label" hx-swap-oob="true" style="margin-top:0.3rem">%s</p>`,
		step,
	)
	html := stepHTML + progressHTML
	p.broker.Publish(jobID, "step_start", html)
}

func (p *Pipeline) emitProgress(jobID, step string, pct float64, allSteps []string) {
	data := fmt.Sprintf(`{"pct":%.0f,"step":"%s","done":true}`, pct, step)
	p.broker.Publish(jobID, "progress", data)
}

func (p *Pipeline) emitComplete(jobID string, result *JobResult) {
	html := fmt.Sprintf(
		`<progress value="100" max="100" id="job-progress" hx-swap-oob="true"></progress>
		 <p class="muted" id="pct-label" hx-swap-oob="true" style="margin-top:0.3rem">100%% — Done!</p>
		 <div class="downloads">
		   <a href="/jobs/%s/files/voiceover.mp4" class="download-btn">⬇ Voiceover (.mp4)</a>
		   <a href="/jobs/%s/files/dub.mp4" class="download-btn">⬇ Replace (.mp4)</a>
		   <a href="/jobs/%s/files/narrative.mp3" class="download-btn">⬇ Narrative Audio (.mp3)</a>
		   <a href="/jobs/%s/files/dub.mp3" class="download-btn">⬇ Dubbing Audio (.mp3)</a>
		   <a href="/jobs/%s/files/subtitle.srt" class="download-btn">⬇ Subtitle (.srt)</a>
		 </div>`,
		jobID, jobID, jobID, jobID, jobID,
	)

	if result.DriveUploaded {
		html += fmt.Sprintf(
			`<div class="downloads" style="margin-top:0.5rem">
			   <span class="muted" style="margin-right:0.5rem">☁ Google Drive:</span>`,
		)
		if result.VoiceoverGDrive != "" {
			html += fmt.Sprintf(`<a href="%s" target="_blank" class="download-btn" style="background:#1f6feb;border-color:#1f6feb">voiceover.mp4</a>`, result.VoiceoverGDrive)
		}
		if result.DubGDrive != "" {
			html += fmt.Sprintf(`<a href="%s" target="_blank" class="download-btn" style="background:#1f6feb;border-color:#1f6feb">dub.mp4</a>`, result.DubGDrive)
		}
		if result.SubtitleGDrive != "" {
			html += fmt.Sprintf(`<a href="%s" target="_blank" class="download-btn" style="background:#1f6feb;border-color:#1f6feb">subtitle.srt</a>`, result.SubtitleGDrive)
		}
		html += `</div>`
	}

	p.broker.Publish(jobID, "job_complete", html)
	p.emitStepLog(jobID, "pipeline", "completed", "All steps finished!", 0)
}

func (p *Pipeline) emitError(jobID, event, message string) {
	html := fmt.Sprintf(`<div class="card" style="border-color:#da3633"><p class="error">❌ %s</p></div>`, message)
	p.broker.Publish(jobID, event, html)
}

func buildStepIndicators(current string, allSteps []string) string {
	var b strings.Builder
	b.WriteString(`<div class="steps" id="steps" hx-swap-oob="true">`)
	passed := true
	for _, s := range allSteps {
		cls := ""
		if s == current {
			cls = "active"
			passed = false
		} else if passed {
			cls = "done"
		}
		b.WriteString(fmt.Sprintf(`<span class="step %s">%s</span>`, cls, s))
	}
	b.WriteString(`</div>`)
	return b.String()
}
