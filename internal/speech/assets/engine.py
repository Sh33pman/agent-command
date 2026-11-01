"""acmd's speech engine (SPEC §8.9): text to speech with an MLX model
(Kokoro by default) through mlx-audio. acmd starts it, sends it text and
plays what it writes.

  engine.py --prefetch --model REPO --voice SPEC
      download the model and the voices it needs, then exit
  engine.py --serve SOCKET --model REPO --voice SPEC [--idle SECONDS]
      load the model once and answer requests on a unix socket until idle

A request is one JSON line: {"op": "synth", "text": ..., "out": WAV path,
"speed": 1.0} or {"op": "ping"}; the reply is one JSON line {"ok": true,
"seconds": ...} or {"ok": false, "error": ...}.

A voice SPEC is a voice name or a weighted blend "af_heart:0.6,af_bella:0.4".
"""

import argparse
import json
import os
import socket
import sys
import threading
import time
import types
import wave

# espeak-ng is only Kokoro's last resort for words its dictionary lacks, and
# the espeakng-loader wheel for macOS can't find its data: without it those
# rare words are skipped.
_stub = types.ModuleType("misaki.espeak")


class EspeakFallback:  # noqa: D101 - stand-in
    def __init__(self, *a, **k):
        raise RuntimeError("espeak-ng is not installed")


_stub.EspeakFallback = EspeakFallback
sys.modules["misaki.espeak"] = _stub

SAMPLE_RATE = 24000


def parse_voice(spec):
    """'af_heart:0.6,af_bella:0.4' -> [('af_heart', 0.6), ('af_bella', 0.4)]."""
    out = []
    for part in spec.split(","):
        part = part.strip()
        if not part:
            continue
        name, _, weight = part.partition(":")
        out.append((name.strip(), float(weight) if weight else 1.0))
    if not out:
        raise ValueError("no voice given")
    total = sum(w for _, w in out)
    if total <= 0:
        raise ValueError("voice weights must add up to more than 0")
    return [(n, w / total) for n, w in out]


def fetch(model, voices, offline=False):
    """The model's local folder: its top-level files and the voices in use
    (Kokoro has ~50 voices; only these are downloaded). Offline: what's
    already downloaded."""
    from huggingface_hub import HfApi, snapshot_download

    if offline:
        # Only part of the repo was downloaded, so find the folder through
        # a file that always is.
        from huggingface_hub import try_to_load_from_cache

        cfg = try_to_load_from_cache(repo_id=model, filename="config.json")
        if not isinstance(cfg, str):
            raise RuntimeError(f"{model} isn't downloaded: run `acmd speech install`")
        return os.path.dirname(cfg)
    names = {n for n, _ in voices}
    files = [
        f for f in HfApi().list_repo_files(model)
        if "/" not in f or (f.startswith("voices/") and f.split("/", 1)[1].rsplit(".", 1)[0] in names)
    ]
    return snapshot_download(repo_id=model, allow_patterns=files)


class Engine:
    def __init__(self, model, voice_spec):
        from mlx_audio.tts.utils import load_model

        self.voices = parse_voice(voice_spec)
        self.path = fetch(model, self.voices, offline=os.environ.get("HF_HUB_OFFLINE") == "1")
        from mlx_audio.utils import get_model_name_parts

        # The local folder is named after a commit: name the model by its
        # repo so mlx-audio knows its architecture.
        self.model = load_model(self.path, model_name_parts=get_model_name_parts(model))
        # Kokoro voices start with their language: a(merican), b(ritish), …
        first = self.voices[0][0][:1]
        self.lang = first if first in "abefhijpz" else "a"
        self.pipeline = self.model._get_pipeline(self.lang)
        blend = None
        for name, weight in self.voices:
            v = self.pipeline.load_single_voice(name) * weight
            blend = v if blend is None else blend + v
        self.pipeline.voices["acmd"] = blend

    def synth(self, text, out, speed=1.0):
        import numpy as np

        chunks = []
        for _, _, audio in self.pipeline(text, voice="acmd", speed=speed):
            if audio is not None:
                chunks.append(np.array(audio).flatten())
        if not chunks:
            raise ValueError("nothing to say")
        a = np.concatenate(chunks)
        pcm = (np.clip(a, -1, 1) * 32767).astype(np.int16)
        tmp = out + ".tmp"
        with wave.open(tmp, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(2)
            w.setframerate(SAMPLE_RATE)
            w.writeframes(pcm.tobytes())
        os.replace(tmp, out)
        return len(a) / SAMPLE_RATE


def serve(engine, path, idle):
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(path)
    os.chmod(path, 0o600)
    srv.listen(4)
    srv.settimeout(5)
    last = time.time()
    lock = threading.Lock()  # one synthesis at a time (one GPU)
    try:
        while time.time() - last < idle:
            try:
                conn, _ = srv.accept()
            except socket.timeout:
                continue
            last = time.time()
            with conn:
                f = conn.makefile("rwb")
                line = f.readline()
                try:
                    req = json.loads(line)
                    if req.get("op") == "ping":
                        reply = {"ok": True}
                    else:
                        with lock:
                            secs = engine.synth(req["text"], req["out"], float(req.get("speed", 1.0)))
                        reply = {"ok": True, "seconds": secs}
                except Exception as e:  # report, keep serving
                    reply = {"ok": False, "error": str(e)}
                f.write((json.dumps(reply) + "\n").encode())
                f.flush()
            last = time.time()
    finally:
        srv.close()
        try:
            os.unlink(path)
        except FileNotFoundError:
            pass


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", required=True)
    ap.add_argument("--voice", required=True)
    ap.add_argument("--prefetch", action="store_true")
    ap.add_argument("--serve")
    ap.add_argument("--idle", type=float, default=600)
    args = ap.parse_args()
    if args.prefetch:
        print(fetch(args.model, parse_voice(args.voice)))
        return
    if not args.serve:
        ap.error("--prefetch or --serve")
    serve(Engine(args.model, args.voice), args.serve, args.idle)


if __name__ == "__main__":
    main()
