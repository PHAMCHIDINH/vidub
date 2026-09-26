#!/usr/bin/env python3
"""Generate TTS audio (narrative + dubbing) from translated transcript JSON.

Usage:
    python scripts/tts.py <video_id> <work_dir> <voice>

Reads:  {work_dir}/translated.json
Writes: {work_dir}/narrative.mp3
        {work_dir}/dub.mp3
Exits 0 on success, 1 on failure.
"""

import asyncio
import json
import math
import os
import re
import shutil
import sys
import tempfile
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

import edge_tts
import librosa
import numpy as np
from pydub import AudioSegment

# ─── Config ────────────────────────────────────────────────────
TTS_CHUNK_MAX_CHARS = 2000
SILENCE_BETWEEN_CHUNKS = 200
# Rough Vietnamese Edge TTS speech rate. Only used for the log estimate:
# fitting decisions measure the real synthesized audio instead.
VN_CHARS_PER_SECOND = 15.0
MAX_SPEEDUP_PERCENT = 25
MAX_STRETCH_PERCENT = 15
MAX_CONCURRENT_DUB = 10
# Dub mode speaks sentence-level units made of consecutive caption segments.
# Captions are ~2s fragments of a sentence; speaking each on its own leaves
# no room to balance a long fragment against a short one.
MAX_UNIT_SECONDS = 15.0   # bounds how far speech can drift from its caption
UNIT_GAP_SECONDS = 0.6    # a pause at least this long always ends a unit
SENTENCE_END = (".", "?", "!", "…", "。", "？", "！")
TTS_PITCH = "+0Hz"
TTS_RETRIES = 3


# ─── Helpers ───────────────────────────────────────────────────

def export_atomic(audio: AudioSegment, output_path: str) -> None:
    """Export via a temporary file, so an interrupted run never leaves a
    truncated MP3 that the pipeline would take as up to date."""
    tmp = output_path + ".tmp"
    audio.export(tmp, format="mp3", bitrate="64k")
    os.replace(tmp, output_path)


def filter_speech_text(text: str) -> str:
    if re.match(r'^[♪♫🎵🎶\[\]\(\)\s]+$', text.strip()):
        return ""
    text = re.sub(r'\[.*?\]', '', text)
    text = re.sub(r'\(.*?\)', '', text)
    text = re.sub(r'[♪♫🎵🎶]', '', text)
    text = re.sub(r'\*\*.*?\*\*', '', text)
    text = re.sub(r'\s+', ' ', text).strip()
    return text


async def _tts_single(text: str, voice: str, rate: str, pitch: str, output_path: str) -> bool:
    try:
        communicate = edge_tts.Communicate(text=text, voice=voice, rate=rate, pitch=pitch)
        await communicate.save(output_path)
        return True
    except Exception as e:
        print(f"TTS error: {str(e)[:100]}", file=sys.stderr)
        return False


def tts_synthesize(text: str, output_path: str, voice: str, rate: str) -> bool:
    """Synthesize with retries. Edge TTS fails transiently under load."""
    for attempt in range(TTS_RETRIES):
        if attempt:
            time.sleep(2 ** attempt)
        if asyncio.run(_tts_single(text, voice, rate, TTS_PITCH, output_path)) \
                and os.path.exists(output_path) and os.path.getsize(output_path) > 0:
            return True
    return False


def split_text_for_tts(segments: list[dict]) -> list[str]:
    clean = [filter_speech_text(s.get("text", "")) for s in segments]
    clean = [t for t in clean if t]
    full = " ".join(clean)
    if len(full) <= TTS_CHUNK_MAX_CHARS:
        return [full] if full.strip() else []
    sentences = re.split(r'(?<=[.!?。！？])\s+', full)
    chunks = []
    current = ""
    for sent in sentences:
        test = (current + " " + sent).strip() if current else sent
        if len(test) <= TTS_CHUNK_MAX_CHARS:
            current = test
        else:
            if current:
                chunks.append(current)
            current = sent
    if current:
        chunks.append(current)
    return chunks


def apply_time_stretch(audio: AudioSegment, speedup: float) -> AudioSegment:
    """Shorten audio by `speedup` (0.10 = 10% faster) without changing pitch."""
    orig_channels = audio.channels
    sr = audio.frame_rate
    full_scale = float(1 << (8 * audio.sample_width - 1))
    samples = np.array(audio.set_channels(1).get_array_of_samples(), dtype=np.float32) / full_scale
    # librosa: rate > 1 speeds up (shorter output).
    y = librosa.effects.time_stretch(samples, rate=1.0 + speedup)
    y_int16 = (np.clip(y, -1.0, 1.0) * 32767.0).astype(np.int16)
    stretched = AudioSegment(y_int16.tobytes(), frame_rate=sr, sample_width=2, channels=1)
    if orig_channels > 1:
        stretched = stretched.set_channels(orig_channels)
    return stretched


def fit_to_window(text: str, window_ms: int, tmp_path: str, voice: str) -> tuple[AudioSegment | None, str, int]:
    """Synthesize text so it fits in window_ms. Returns (audio, how, natural_ms),
    natural_ms being the length at normal speed.

    Decisions use the measured length of the synthesized audio:
      1. natural rate
      2. Edge TTS rate up to +25%
      3. time-stretch up to +15% more
      4. cut + fade as the last resort
    """
    if not tts_synthesize(text, tmp_path, voice, "+0%"):
        return None, "tts failed", 0
    audio = AudioSegment.from_file(tmp_path)
    natural_ms = len(audio)
    if natural_ms <= window_ms:
        return audio, "natural", natural_ms

    how = "natural"
    ratio = len(audio) / window_ms
    pct = min(MAX_SPEEDUP_PERCENT, math.ceil((ratio - 1.0) * 100))
    if tts_synthesize(text, tmp_path, voice, f"+{pct}%"):
        audio = AudioSegment.from_file(tmp_path)
        how = f"rate +{pct}%"

    if len(audio) > window_ms:
        speedup = min(MAX_STRETCH_PERCENT / 100.0, len(audio) / window_ms - 1.0)
        try:
            audio = apply_time_stretch(audio, speedup)
            how += f", stretch +{speedup * 100:.0f}%"
        except Exception as e:
            print(f"  stretch failed: {e}", file=sys.stderr)

    if len(audio) > window_ms:
        audio = audio[:window_ms].fade_out(min(200, window_ms // 4))
        how += ", cut+fade"
    return audio, how, natural_ms


def speech_windows(segments: list[dict]) -> list[tuple[int, int]]:
    """(start_ms, window_ms) for each segment, in input order.

    YouTube captions often overlap: a segment's duration runs past the next
    segment's start. Speech must end before the next segment begins, otherwise
    every overlap pushes the rest of the track later and the dub drifts.
    """
    starts = [int(round(float(s.get("start", 0.0)) * 1000)) for s in segments]
    ordered = sorted(set(starts))
    next_start = {a: b for a, b in zip(ordered, ordered[1:])}
    windows = []
    for seg, start_ms in zip(segments, starts):
        window = int(round(float(seg.get("duration", 0.0)) * 1000))
        if start_ms in next_start:
            window = min(window, next_start[start_ms] - start_ms)
        windows.append((start_ms, max(window, 0)))
    return windows


def ends_sentence(seg: dict) -> bool:
    """Sentence punctuation in the source text (translations of a fragment
    often lose it), falling back to the translated text."""
    for key in ("original_text", "text"):
        text = (seg.get(key) or "").strip()
        if text:
            return text.endswith(SENTENCE_END)
    return False


def speech_units(segments: list[dict]) -> list[dict]:
    """Group caption segments into sentence-level units to speak.

    A unit ends at sentence punctuation, at a pause of UNIT_GAP_SECONDS, at a
    non-speech segment, or when it would pass MAX_UNIT_SECONDS. Its window
    runs from its first segment's start to its last segment's window end, so
    speech may use the short pauses inside a sentence.

    Returns dicts with "text", "start_ms", "window_ms" and "segments" (the
    indexes of the caption segments it covers), ordered by start.
    """
    windows = speech_windows(segments)
    gap_ms = int(UNIT_GAP_SECONDS * 1000)
    max_ms = int(MAX_UNIT_SECONDS * 1000)

    units = []
    cur = None

    def close():
        nonlocal cur
        if cur:
            units.append(cur)
            cur = None

    for i in sorted(range(len(segments)), key=lambda i: windows[i][0]):
        start_ms, window_ms = windows[i]
        text = filter_speech_text(segments[i].get("text", ""))
        if not text or window_ms <= 0:
            close()  # music or an empty line acts as a pause
            continue
        end_ms = start_ms + window_ms
        if cur and (start_ms - cur["end_ms"] >= gap_ms or end_ms - cur["start_ms"] > max_ms):
            close()
        if cur is None:
            cur = {"texts": [], "start_ms": start_ms, "end_ms": end_ms, "segments": []}
        cur["texts"].append(text)
        cur["segments"].append(i)
        cur["end_ms"] = max(cur["end_ms"], end_ms)
        if ends_sentence(segments[i]):
            close()
    close()

    for u in units:
        u["text"] = " ".join(u.pop("texts"))
        u["window_ms"] = u.pop("end_ms") - u["start_ms"]
    return units


# ─── Mode B: Narrative ────────────────────────────────────────

def generate_narrative(segments: list[dict], video_id: str, work_dir: str, voice: str) -> str | None:
    chunks = split_text_for_tts(segments)
    if not chunks:
        print("No valid text for TTS.", file=sys.stderr)
        return None

    total_chars = sum(len(c) for c in chunks)
    est_min = total_chars / VN_CHARS_PER_SECOND / 60
    print(f"Narrative: {len(chunks)} chunks, ~{total_chars} chars, ~{est_min:.1f} min", file=sys.stderr)

    tmp_dir = tempfile.mkdtemp(prefix="tts_narrative_")
    try:
        audio_parts = []
        for i, chunk in enumerate(chunks):
            tmp_path = os.path.join(tmp_dir, f"chunk_{i:04d}.mp3")
            print(f"  Chunk {i+1}/{len(chunks)} ({len(chunk)} chars)...", end=" ", file=sys.stderr)
            # A missing chunk silently drops minutes of narration, so any
            # failure (after retries) fails the whole narrative.
            if not tts_synthesize(chunk, tmp_path, voice, "+0%"):
                print("FAIL", file=sys.stderr)
                return None
            audio = AudioSegment.from_file(tmp_path)
            audio_parts.append(audio)
            print(f"OK {len(audio)/1000:.1f}s", file=sys.stderr)
    finally:
        shutil.rmtree(tmp_dir, ignore_errors=True)

    silence = AudioSegment.silent(duration=SILENCE_BETWEEN_CHUNKS)
    combined = audio_parts[0]
    for part in audio_parts[1:]:
        combined = combined + silence + part

    output_path = os.path.join(work_dir, "narrative.mp3")
    export_atomic(combined, output_path)
    return output_path


# ─── Mode A: Dubbing ──────────────────────────────────────────

def process_dub_unit(unit: dict, idx: int, total: int, voice: str) -> dict:
    tmp_dir = tempfile.mkdtemp(prefix="tts_dub_")
    try:
        audio, how, natural_ms = fit_to_window(
            unit["text"], unit["window_ms"], os.path.join(tmp_dir, f"unit_{idx:04d}.mp3"), voice)
    finally:
        shutil.rmtree(tmp_dir, ignore_errors=True)

    if audio is None:
        print(f"  Unit {idx+1}/{total}: TTS failed", file=sys.stderr)
        return {"failed": True}
    if how != "natural":
        print(f"  Unit {idx+1}/{total} ({unit['window_ms'] / 1000:.1f}s): {how}", file=sys.stderr)
    return {"audio": audio, "start_ms": unit["start_ms"], "how": how,
            "chars": len(unit["text"]), "natural_ms": natural_ms}


def generate_dubbing(segments: list[dict], video_id: str, work_dir: str, voice: str) -> str | None:
    units = speech_units(segments)
    if not units:
        print("No valid segments for dubbing.", file=sys.stderr)
        return None
    print(f"Dubbing: {len(segments)} segments in {len(units)} units", file=sys.stderr)

    results = [None] * len(units)
    with ThreadPoolExecutor(max_workers=MAX_CONCURRENT_DUB) as executor:
        future_to_idx = {executor.submit(process_dub_unit, u, i, len(units), voice): i
                         for i, u in enumerate(units)}
        for future in as_completed(future_to_idx):
            idx = future_to_idx[future]
            try:
                results[idx] = future.result()
            except Exception as e:
                print(f"Unit {idx+1} error: {e}", file=sys.stderr)
                results[idx] = {"failed": True}

    failed = sum(1 for r in results if r.get("failed"))
    if failed:
        print(f"WARNING: {failed}/{len(units)} units failed TTS and are silent", file=sys.stderr)

    valid_results = [r for r in results if not r.get("failed")]
    valid_results.sort(key=lambda r: r["start_ms"])
    if not valid_results:
        return None

    # Summary, to tune translation length and the constants above.
    cut = sum(1 for r in valid_results if "cut" in r["how"])
    squeezed = sum(1 for r in valid_results if r["how"] != "natural")
    chars = sum(r["chars"] for r in valid_results)
    natural_s = sum(r["natural_ms"] for r in valid_results) / 1000
    print(f"Dubbing fit: {len(valid_results) - squeezed} natural, {squeezed - cut} sped up, "
          f"{cut} cut (of {len(valid_results)} units)", file=sys.stderr)
    if natural_s > 0:
        print(f"Measured speech rate: {chars / natural_s:.1f} chars/s at normal speed", file=sys.stderr)

    ref = valid_results[0]["audio"]
    ref_sr = ref.frame_rate
    ref_ch = ref.channels
    ref_sw = ref.sample_width

    max_end = max(s.get("start", 0) + s.get("duration", 0) for s in segments) + 1
    total_ms = int(max_end * 1000)

    timeline = AudioSegment.silent(duration=0, frame_rate=ref_sr)
    if ref_ch > 1:
        timeline = timeline.set_channels(ref_ch)
    timeline = timeline.set_sample_width(ref_sw)
    cursor = 0

    for r in valid_results:
        # Audio never exceeds its window, so it normally starts exactly on time.
        target_ms = r["start_ms"]
        audio = r["audio"]
        if audio.frame_rate != ref_sr:
            audio = audio.set_frame_rate(ref_sr)
        if audio.channels != ref_ch:
            audio = audio.set_channels(ref_ch)
        if audio.sample_width != ref_sw:
            audio = audio.set_sample_width(ref_sw)

        if target_ms > cursor:
            gap = target_ms - cursor
            sil = AudioSegment.silent(duration=gap, frame_rate=ref_sr)
            if ref_ch > 1:
                sil = sil.set_channels(ref_ch)
            sil = sil.set_sample_width(ref_sw)
            timeline += sil
        timeline += audio
        # Track the real end of the timeline. If this audio had to start late
        # (two segments with the same start), the delay is absorbed by the
        # next silence gap instead of shifting everything after it.
        cursor = max(cursor, target_ms) + len(audio)

    if cursor < total_ms:
        pad = total_ms - cursor
        sil = AudioSegment.silent(duration=pad, frame_rate=ref_sr)
        if ref_ch > 1:
            sil = sil.set_channels(ref_ch)
        sil = sil.set_sample_width(ref_sw)
        timeline += sil

    output_path = os.path.join(work_dir, "dub.mp3")
    export_atomic(timeline, output_path)
    return output_path


# ─── Main ─────────────────────────────────────────────────────

def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} <video_id> <work_dir> [voice] [--mode narrative|dub]", file=sys.stderr)
        sys.exit(1)

    video_id = sys.argv[1]
    work_dir = sys.argv[2]
    voice = sys.argv[3] if len(sys.argv) > 3 and not sys.argv[3].startswith("--") else "vi-VN-HoaiMyNeural"

    # Parse optional --mode flag: "narrative", "dub", or both (default).
    mode = "both"
    for i, arg in enumerate(sys.argv):
        if arg == "--mode" and i + 1 < len(sys.argv):
            mode = sys.argv[i + 1]
            if i + 2 < len(sys.argv) and not sys.argv[i + 2].startswith("--"):
                voice = sys.argv[i + 2]
            break

    translated_path = os.path.join(work_dir, "translated.json")
    if not os.path.exists(translated_path):
        print(f"ERROR: translated.json not found at {translated_path}", file=sys.stderr)
        sys.exit(1)

    with open(translated_path, "r", encoding="utf-8") as f:
        data = json.load(f)

    segments = data.get("segments", [])
    if not segments:
        segments = data.get("transcript", [])

    if not segments:
        print("ERROR: No segments in translated.json", file=sys.stderr)
        sys.exit(1)

    print(f"Loaded {len(segments)} segments", file=sys.stderr)

    ok = 0
    if mode in ("both", "narrative"):
        narrative_path = generate_narrative(segments, video_id, work_dir, voice)
        if narrative_path:
            print(f"Narrative: {narrative_path}", file=sys.stderr)
            ok += 1
    if mode in ("both", "dub"):
        dub_path = generate_dubbing(segments, video_id, work_dir, voice)
        if dub_path:
            print(f"Dubbing: {dub_path}", file=sys.stderr)
            ok += 1

    if ok == 0:
        sys.exit(1)
    sys.exit(0)


if __name__ == "__main__":
    main()
