#!/usr/bin/env bash
# Packages the speech model for hosting: Kokoro-82M for MLX and the English
# text-processing resources, from exact upstream revisions, laid out as
# mlx-audio-swift expects. Prints the archive's SHA-256, which goes in
# internal/speech/model.go.
#
#   scripts/package-speech-model.sh OUT.tar.gz
#
# Upload it with: gh release create speech-model-vN OUT.tar.gz --repo brettmostert/homebrew-tap
set -euo pipefail

out="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
kokoro_repo="mlx-community/Kokoro-82M-bf16"
kokoro_rev="a71e4d38b236d968966a2002c4c895dbd12b1c3c"
g2p_repo="beshkenadze/kitten-tts-g2p"
g2p_rev="9c692b92682d959d9013a9cfe6a49541997add18"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fetch() { # repo revision folder file
	mkdir -p "$(dirname "$work/mlx-audio/$3/$4")"
	curl -fsSL --retry 3 -o "$work/mlx-audio/$3/$4" "https://huggingface.co/$1/resolve/$2/$4"
}

kokoro_dir="mlx-audio/${kokoro_repo/\//_}"
g2p_dir="mlx-audio/${g2p_repo/\//_}"
fetch "$kokoro_repo" "$kokoro_rev" "${kokoro_repo/\//_}" config.json
fetch "$kokoro_repo" "$kokoro_rev" "${kokoro_repo/\//_}" kokoro-v1_0.safetensors
curl -fsSL "https://huggingface.co/api/models/$kokoro_repo/tree/$kokoro_rev/voices" |
	python3 -c 'import json,sys; [print(f["path"]) for f in json.load(sys.stdin)]' |
	while read -r voice; do
		fetch "$kokoro_repo" "$kokoro_rev" "${kokoro_repo/\//_}" "$voice"
	done
for file in us_gold.json us_silver.json us_bart.safetensors us_bart_config.json; do
	fetch "$g2p_repo" "$g2p_rev" "${g2p_repo/\//_}" "$file"
done

# This repo has no config.json, so mlx-audio-swift only treats it as complete,
# and stays offline, with its record of what a finished download contains.
python3 - "$work/$g2p_dir" "$g2p_rev" <<'PY'
import json, os, sys
folder, revision = sys.argv[1:3]
files = [{"rfilename": n, "size": os.path.getsize(os.path.join(folder, n))} for n in sorted(os.listdir(folder))]
with open(os.path.join(folder, ".mlx-audio-snapshot.json"), "w") as f:
    json.dump({"revision": revision, "files": files}, f)
PY

cat > "$work/NOTICE" <<NOTICE
Speech model files for Agent Command, unmodified copies of:

$kokoro_repo at revision $kokoro_rev
https://huggingface.co/$kokoro_repo
Kokoro-82M by hexgrad, https://huggingface.co/hexgrad/Kokoro-82M
License: Apache-2.0, https://www.apache.org/licenses/LICENSE-2.0

$g2p_repo at revision $g2p_rev
https://huggingface.co/$g2p_repo
Ported from Misaki, https://github.com/hexgrad/misaki
License: MIT
NOTICE

COPYFILE_DISABLE=1 tar -czf "$out" --no-xattrs -C "$work" NOTICE "$kokoro_dir" "$g2p_dir"
ls -lh "$out"
shasum -a 256 "$out"
