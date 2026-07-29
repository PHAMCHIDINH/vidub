#!/usr/bin/env python3
"""Extract YouTube transcript and save as JSON.

Usage:
    python scripts/extract.py <video_id> <url> <output_dir>

Writes: {output_dir}/transcript.json
Exits 0 on success, 1 on failure.
"""

import json
import os
import re
import sys
import time

from youtube_transcript_api import YouTubeTranscriptApi

_ytt_api = YouTubeTranscriptApi()


def extract_video_id(url: str) -> str | None:
    url = url.strip()
    patterns = [
        r"(?:youtube\.com/watch\?(?:.*&)?v=|youtube\.com/watch/)([A-Za-z0-9_-]{11})",
        r"youtu\.be/([A-Za-z0-9_-]{11})",
        r"youtube\.com/embed/([A-Za-z0-9_-]{11})",
        r"youtube\.com/shorts/([A-Za-z0-9_-]{11})",
        r"youtube\.com/v/([A-Za-z0-9_-]{11})",
        r"youtube\.com/live/([A-Za-z0-9_-]{11})",
    ]
    for pattern in patterns:
        m = re.search(pattern, url)
        if m:
            return m.group(1)
    if re.match(r"^[A-Za-z0-9_-]{11}$", url):
        return url
    return None


def fetch_transcript(video_id: str):
    try:
        transcript_list = _ytt_api.list(video_id)
        manuals = [t for t in transcript_list if not t.is_generated]
        autos = [t for t in transcript_list if t.is_generated]
        target = manuals[0] if manuals else (autos[0] if autos else None)
        if target is None:
            return None, "unknown", "", "Video has no transcripts."
        fetched = target.fetch()
        return (
            fetched.to_raw_data(),
            "auto" if target.is_generated else "manual",
            target.language_code,
            "",
        )
    except Exception as e:
        return None, "unknown", "", str(e)


def classify_error(msg: str) -> str:
    msg_l = msg.lower()
    if "video unavailable" in msg_l or "private" in msg_l:
        return "Video unavailable (deleted/private)"
    if "age" in msg_l or "restricted" in msg_l or "login" in msg_l:
        return "Video age-restricted or requires login"
    if "disabled" in msg_l:
        return "Transcripts disabled for this video"
    if "too many requests" in msg_l or "rate" in msg_l:
        return "Rate-limited by YouTube"
    if "no transcript" in msg_l or "not available" in msg_l:
        return "No transcript available"
    return msg


def main():
    if len(sys.argv) != 4:
        print(f"Usage: {sys.argv[0]} <video_id> <url> <output_dir>", file=sys.stderr)
        sys.exit(1)

    video_id = sys.argv[1]
    url = sys.argv[2]
    output_dir = sys.argv[3]

    os.makedirs(output_dir, exist_ok=True)
    output_path = os.path.join(output_dir, "transcript.json")

    max_retries = 3
    base_delay = 2.0
    non_retryable = ["unavailable", "private", "age-restricted", "login", "disabled", "no transcript"]

    last_error = ""
    for attempt in range(max_retries + 1):
        segments, ttype, lang, err = fetch_transcript(video_id)

        if segments is not None:
            output = {
                "video_id": video_id,
                "video_url": url,
                "language": lang,
                "transcript_type": ttype,
                "source": "youtube_api",
                "transcript": segments,
            }
            with open(output_path, "w", encoding="utf-8") as f:
                json.dump(output, f, ensure_ascii=False, indent=2)
            print(f"Transcript saved: {len(segments)} segments, language={lang}", file=sys.stderr)
            sys.exit(0)

        last_error = classify_error(err)
        if any(phrase in last_error.lower() for phrase in non_retryable):
            break

        if attempt < max_retries:
            wait = base_delay * (2 ** attempt)
            print(f"Retry {attempt+1}/{max_retries}, waiting {wait:.0f}s: {last_error}", file=sys.stderr)
            time.sleep(wait)

    print(f"ERROR: {last_error}", file=sys.stderr)
    sys.exit(1)


if __name__ == "__main__":
    main()
