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
import sys
import tempfile
from concurrent.futures import ThreadPoolExecutor, as_completed

import edge_tts
import librosa
import numpy as np
from pydub import AudioSegment

# ─── Config ────────────────────────────────────────────────────
TTS_CHUNK_MAX_CHARS = 2000
SILENCE_BETWEEN_CHUNKS = 200
VN_CHARS_PER_SECOND = 4.0
MAX_SPEEDUP_PERCENT = 25
MAX_STRETCH_PERCENT = 15
MAX_CONCURRENT_DUB = 10
TTS_PITCH = "+0Hz"


# ─── Helpers ───────────────────────────────────────────────────

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
    return asyncio.run(_tts_single(text, voice, rate, TTS_PITCH, output_path))


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


def compute_rate_for_duration(text: str, target_duration: float) -> tuple[str, float | None]:
    natural = len(text) / VN_CHARS_PER_SECOND
    if natural <= target_duration:
        return "+0%", 0.0
    ratio = natural / target_duration
    max_speedup = 1.0 + MAX_SPEEDUP_PERCENT / 100.0
    max_stretch = MAX_STRETCH_PERCENT / 100.0
    if ratio <= max_speedup:
        pct = int((ratio - 1.0) * 100)
        return f"+{pct}%", 0.0
    remaining = ratio / max_speedup
    stretch_needed = remaining - 1.0
    if stretch_needed <= max_stretch:
        return f"+{MAX_SPEEDUP_PERCENT}%", stretch_needed
    return f"+{MAX_SPEEDUP_PERCENT}%", None


def apply_time_stretch(audio: AudioSegment, stretch_ratio: float) -> AudioSegment:
    orig_channels = audio.channels
    sr = audio.frame_rate
    samples = np.array(audio.set_channels(1).get_array_of_samples(), dtype=np.float32)
    peak = np.max(np.abs(samples))
    if peak > 0:
        samples = samples / peak
    stretch_rate = 1.0 / (1.0 + stretch_ratio)
    y = librosa.effects.time_stretch(samples, rate=stretch_rate)
    y_int16 = (y * 32767.0).astype(np.int16)
    stretched = AudioSegment(y_int16.tobytes(), frame_rate=sr, sample_width=2, channels=1)
    if orig_channels > 1:
        stretched = stretched.set_channels(orig_channels)
    return stretched


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
    audio_parts = []
    for i, chunk in enumerate(chunks):
        tmp_path = os.path.join(tmp_dir, f"chunk_{i:04d}.mp3")
        print(f"  Chunk {i+1}/{len(chunks)} ({len(chunk)} chars)...", end=" ", file=sys.stderr)
        if tts_synthesize(chunk, tmp_path, voice, "+0%"):
            try:
                audio = AudioSegment.from_file(tmp_path)
                audio_parts.append(audio)
                print(f"OK {len(audio)/1000:.1f}s", file=sys.stderr)
            except Exception as e:
                print(f"read error: {e}", file=sys.stderr)
        else:
            print("FAIL", file=sys.stderr)

    if not audio_parts:
        return None

    silence = AudioSegment.silent(duration=SILENCE_BETWEEN_CHUNKS)
    combined = audio_parts[0]
    for part in audio_parts[1:]:
        combined = combined + silence + part

    output_path = os.path.join(work_dir, "narrative.mp3")
    combined.export(output_path, format="mp3", bitrate="64k")
    import shutil
    shutil.rmtree(tmp_dir, ignore_errors=True)
    return output_path


# ─── Mode A: Dubbing ──────────────────────────────────────────

def process_dub_segment(seg: dict, idx: int, total: int, work_dir: str, voice: str) -> dict | None:
    text = filter_speech_text(seg.get("text", ""))
    if not text:
        return None
    start = seg.get("start", 0.0)
    target = seg.get("duration", 0.0)
    if target <= 0:
        return None

    tmp_dir = tempfile.mkdtemp(prefix="tts_dub_")
    tmp_path = os.path.join(tmp_dir, f"seg_{idx:04d}.mp3")

    rate, stretch_ratio = compute_rate_for_duration(text, target)
    overflow = False
    stretched = False

    if not tts_synthesize(text, tmp_path, voice, rate):
        import shutil
        shutil.rmtree(tmp_dir, ignore_errors=True)
        return None

    try:
        audio = AudioSegment.from_file(tmp_path)
    except Exception:
        import shutil
        shutil.rmtree(tmp_dir, ignore_errors=True)
        return None

    if stretch_ratio is not None and stretch_ratio > 0:
        try:
            audio = apply_time_stretch(audio, stretch_ratio)
            stretched = True
        except Exception:
            pass

    audio_dur = len(audio) / 1000.0
    if audio_dur > target:
        overflow = True
        cutoff_ms = int(target * 1000)
        fade_ms = min(200, cutoff_ms // 4)
        audio = audio[:cutoff_ms].fade_out(fade_ms)

    if overflow:
        print(f"  Seg {idx+1}/{total}: overflow, cut+fade", file=sys.stderr)

    import shutil
    shutil.rmtree(tmp_dir, ignore_errors=True)

    return {"audio": audio, "start": start, "target_duration": target}


def generate_dubbing(segments: list[dict], video_id: str, work_dir: str, voice: str) -> str | None:
    print(f"Dubbing: {len(segments)} segments", file=sys.stderr)

    valid_idx = [i for i, s in enumerate(segments) if filter_speech_text(s.get("text", "")) and s.get("duration", 0) > 0]
    if not valid_idx:
        print("No valid segments for dubbing.", file=sys.stderr)
        return None

    results = [None] * len(segments)
    with ThreadPoolExecutor(max_workers=MAX_CONCURRENT_DUB) as executor:
        future_to_idx = {}
        for idx in valid_idx:
            future = executor.submit(process_dub_segment, segments[idx], idx, len(segments), work_dir, voice)
            future_to_idx[future] = idx
        for future in as_completed(future_to_idx):
            idx = future_to_idx[future]
            try:
                results[idx] = future.result()
            except Exception as e:
                print(f"Seg {idx+1} error: {e}", file=sys.stderr)

    valid_results = [r for r in results if r is not None]
    valid_results.sort(key=lambda r: r["start"])
    if not valid_results:
        return None

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
        target_ms = int(r["start"] * 1000)
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
        cursor = target_ms + len(audio)

    if cursor < total_ms:
        pad = total_ms - cursor
        sil = AudioSegment.silent(duration=pad, frame_rate=ref_sr)
        if ref_ch > 1:
            sil = sil.set_channels(ref_ch)
        sil = sil.set_sample_width(ref_sw)
        timeline += sil

    output_path = os.path.join(work_dir, "dub.mp3")
    timeline.export(output_path, format="mp3", bitrate="64k")
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
